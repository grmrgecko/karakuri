package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/grmrgecko/karakuri/pkg/access"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
	"github.com/grmrgecko/karakuri/pkg/providers"
)

// pluginHost is the callback channel served to one plugin process. It embeds
// the shared exec-context service — so a plugin's nodes reach state, vars, the
// node store, and the clock exactly as built-ins do — and adds the callbacks
// that belong to the plugin rather than to a run. Every registration made
// here is tagged with the plugin's name, which is what lets unload reverse it.
type pluginHost struct {
	*hostService

	name string
	log  *zap.Logger

	mu sync.Mutex
	// rpc is set once the plugin finishes initializing. Subscribe can be
	// called during Init, before there is anything to deliver to, so the
	// handler resolves the client at delivery time rather than capturing it.
	rpc pb.KarakuriPluginClient
	// subs maps a subscribed topic to its broker subscription ID.
	subs map[string]string
	// pending holds the messages not yet handed to the plugin, oldest first,
	// and draining says a goroutine is working through them. Only one does at
	// a time, so the plugin handles messages in the order they were published.
	pending  []*pb.MessageEvent
	draining bool
}

// maxPendingMessages bounds the messages queued for a plugin that has stopped
// answering. Past it new messages are dropped.
const maxPendingMessages = 1024

func newPluginHost(shared *hostService, name string, log *zap.Logger) *pluginHost {
	return &pluginHost{
		hostService: shared,
		name:        name,
		log:         log.With(zap.String("plugin", name)),
		subs:        make(map[string]string),
	}
}

// attach hands the plugin's client over once Init succeeded, so subscribed
// topics start being delivered.
func (p *pluginHost) attach(rpc pb.KarakuriPluginClient) {
	p.mu.Lock()
	p.rpc = rpc
	p.mu.Unlock()
}

// releaseAll withdraws everything the plugin registered through this channel.
func (p *pluginHost) releaseAll() {
	p.mu.Lock()
	subs := p.subs
	p.subs = make(map[string]string)
	p.rpc = nil
	p.pending = nil
	p.mu.Unlock()

	for _, id := range subs {
		mq.B.Unsubscribe(id)
	}
	permissions.R.UnregisterBySource(p.name)
	providers.R.UnregisterBySource(p.name)
	mq.WS.UnregisterTopicPermissionsBySource(p.name)
}

// settingKey namespaces a plugin's settings so one plugin cannot read or
// overwrite another's.
func (p *pluginHost) settingKey(key string) string {
	return "plugins." + p.name + "." + key
}

func (p *pluginHost) GetConfig(_ context.Context, req *pb.ConfigKey) (*pb.ConfigValue, error) {
	if db.DB == nil {
		return &pb.ConfigValue{}, nil
	}
	setting, err := models.GetSetting(p.settingKey(req.Key))
	if err != nil {
		return &pb.ConfigValue{}, nil
	}
	return &pb.ConfigValue{Value: setting.Value, Found: true}, nil
}

func (p *pluginHost) SetConfig(_ context.Context, req *pb.ConfigSet) (*pb.ErrorResponse, error) {
	if db.DB == nil {
		return &pb.ErrorResponse{Error: "no settings store"}, nil
	}
	if err := models.SetSetting(p.settingKey(req.Key), req.Value); err != nil {
		return &pb.ErrorResponse{Error: err.Error()}, nil
	}
	return &pb.ErrorResponse{}, nil
}

func (p *pluginHost) Publish(_ context.Context, req *pb.PublishRequest) (*pb.Empty, error) {
	// The payload crosses the wire as JSON bytes but the broker speaks Go
	// values, so decode before publishing or every subscriber sees base64.
	var payload any
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			payload = string(req.Payload)
		}
	}
	mq.B.Publish(req.Topic, req.MsgType, payload)
	return &pb.Empty{}, nil
}

func (p *pluginHost) Subscribe(_ context.Context, req *pb.TopicRequest) (*pb.ErrorResponse, error) {
	p.mu.Lock()
	if _, ok := p.subs[req.Topic]; ok {
		p.mu.Unlock()
		return &pb.ErrorResponse{}, nil
	}
	p.mu.Unlock()

	id := mq.B.Subscribe(req.Topic, func(msg *mq.Message) {
		p.deliver(msg)
	})

	p.mu.Lock()
	p.subs[req.Topic] = id
	p.mu.Unlock()
	return &pb.ErrorResponse{}, nil
}

func (p *pluginHost) Unsubscribe(_ context.Context, req *pb.TopicRequest) (*pb.Empty, error) {
	p.mu.Lock()
	id, ok := p.subs[req.Topic]
	delete(p.subs, req.Topic)
	p.mu.Unlock()
	if ok {
		mq.B.Unsubscribe(id)
	}
	return &pb.Empty{}, nil
}

// deliver queues one broker message for the plugin. It runs on the broker's
// goroutine, so it never waits on the plugin: a slow or dead one fills its own
// queue rather than blocking other subscribers.
func (p *pluginHost) deliver(msg *mq.Message) {
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		p.log.Debug("dropping unencodable message", zap.String("topic", msg.Topic), zap.Error(err))
		return
	}
	ev := &pb.MessageEvent{Topic: msg.Topic, MsgType: msg.Type, Payload: payload}

	p.mu.Lock()
	if p.rpc == nil {
		p.mu.Unlock()
		return
	}
	if len(p.pending) >= maxPendingMessages {
		p.mu.Unlock()
		p.log.Warn("dropping a message for a plugin that is not answering", zap.String("topic", msg.Topic))
		return
	}
	p.pending = append(p.pending, ev)
	start := !p.draining
	p.draining = true
	p.mu.Unlock()
	if start {
		go p.drain()
	}
}

// drain hands the queued messages to the plugin one at a time, each after the
// plugin answered the one before. Plugins start and stop work on subscription
// counts, and a count handled ahead of an earlier one leaves the plugin in
// the state the topic was in before, not the one it ended in.
func (p *pluginHost) drain() {
	for {
		p.mu.Lock()
		if len(p.pending) == 0 || p.rpc == nil {
			p.pending = nil
			p.draining = false
			p.mu.Unlock()
			return
		}
		ev := p.pending[0]
		p.pending = p.pending[1:]
		rpc := p.rpc
		p.mu.Unlock()

		// A plugin that stops answering would otherwise hold the queue for
		// the life of the process.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := rpc.HandleMessage(ctx, ev)
		cancel()
		if err != nil {
			p.log.Debug("plugin rejected a message", zap.String("topic", ev.Topic), zap.Error(err))
		}
	}
}

func (p *pluginHost) RegisterPermission(_ context.Context, req *pb.PermissionRequest) (*pb.Empty, error) {
	permissions.R.RegisterWithSource(req.Permission, req.Actions, req.Description, p.name)
	return &pb.Empty{}, nil
}

func (p *pluginHost) UnregisterPermission(_ context.Context, req *pb.PermissionName) (*pb.Empty, error) {
	permissions.R.UnregisterFromSource(req.Permission, p.name)
	return &pb.Empty{}, nil
}

func (p *pluginHost) SeedRoleDefaults(_ context.Context, req *pb.SeedRoleDefaultsRequest) (*pb.ErrorResponse, error) {
	if db.DB == nil {
		return &pb.ErrorResponse{}, nil
	}
	for _, d := range req.Defaults {
		if err := seedRolePermission(d); err != nil {
			return &pb.ErrorResponse{Error: err.Error()}, nil
		}
	}
	return &pb.ErrorResponse{}, nil
}

// seedRolePermission grants one permission to the roles a plugin named,
// skipping roles that already hold it so an operator's later revoke sticks.
func seedRolePermission(d *pb.RoleDefault) error {
	var roles []models.Role
	q := db.DB.Model(&models.Role{})
	if !d.AllRoles {
		if len(d.Roles) == 0 {
			return nil
		}
		q = q.Where("name IN ?", d.Roles)
	}
	if err := q.Find(&roles).Error; err != nil {
		return fmt.Errorf("listing roles: %w", err)
	}
	for _, role := range roles {
		var count int64
		db.DB.Model(&models.RolePermission{}).
			Where("role_id = ? AND permission = ?", role.ID, d.Permission).Count(&count)
		if count > 0 {
			continue
		}
		if err := db.DB.Create(&models.RolePermission{
			RoleID:     role.ID,
			Permission: d.Permission,
		}).Error; err != nil {
			return fmt.Errorf("granting %s to %s: %w", d.Permission, role.Name, err)
		}
	}
	return nil
}

func (p *pluginHost) CheckPermission(_ context.Context, req *pb.PermissionCheck) (*pb.AllowedResponse, error) {
	if db.DB == nil || req.UserId == "" {
		return &pb.AllowedResponse{}, nil
	}
	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return &pb.AllowedResponse{}, nil
	}
	held, err := auth.GetEffectivePermissions(userID)
	if err != nil {
		return &pb.AllowedResponse{}, nil
	}
	for _, h := range held {
		if permissions.MatchPermission(h, req.Permission) {
			return &pb.AllowedResponse{Allowed: true}, nil
		}
	}
	return &pb.AllowedResponse{}, nil
}

// CheckDashboardAccess answers whether a user may act on one dashboard at the
// requested level, so a plugin storing per-widget configuration gates on the
// dashboard rather than on a blanket permission.
func (p *pluginHost) CheckDashboardAccess(_ context.Context, req *pb.DashboardAccessCheck) (*pb.AllowedResponse, error) {
	if db.DB == nil {
		return &pb.AllowedResponse{}, nil
	}
	dashboardID, err := uuid.Parse(req.DashboardId)
	if err != nil {
		return &pb.AllowedResponse{}, nil
	}

	// An unparsable or absent user is anonymous, which only public dashboards
	// admit and only for viewing.
	userID, _ := uuid.Parse(req.UserId)
	var groupIDs []uint
	if userID != uuid.Nil {
		groupIDs, _ = models.GetUserGroupIDs(userID)
	}

	allowed, err := models.CanAccessDashboard(dashboardID, userID, groupIDs, req.Level)
	if err != nil {
		return &pb.AllowedResponse{}, nil
	}
	return &pb.AllowedResponse{Allowed: allowed}, nil
}

// CheckConnectionAccess answers whether a user may use one connection. An
// unparsable or absent user may use none.
func (p *pluginHost) CheckConnectionAccess(_ context.Context, req *pb.ConnectionAccessCheck) (*pb.AllowedResponse, error) {
	if db.DB == nil {
		return &pb.AllowedResponse{}, nil
	}
	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return &pb.AllowedResponse{}, nil
	}
	return &pb.AllowedResponse{Allowed: access.CanUseConnection(context.Background(), userID, req.ConnectionId)}, nil
}

func (p *pluginHost) RegisterProvider(_ context.Context, req *pb.ProviderRequest) (*pb.Empty, error) {
	providers.R.Register(providers.Provider{
		Type:        req.Type,
		Name:        req.Name,
		Endpoint:    req.Endpoint,
		DisplayName: req.DisplayName,
		Source:      p.name,
	})
	return &pb.Empty{}, nil
}

func (p *pluginHost) UnregisterProvider(_ context.Context, req *pb.ProviderName) (*pb.Empty, error) {
	providers.R.Unregister(req.Type, req.Name)
	return &pb.Empty{}, nil
}

func (p *pluginHost) RegisterTopicPermission(_ context.Context, req *pb.TopicPermissionRequest) (*pb.Empty, error) {
	mq.WS.RegisterTopicPermissionFromSource(req.Pattern, req.Permission, p.name)
	return &pb.Empty{}, nil
}

func (p *pluginHost) UnregisterTopicPermission(_ context.Context, req *pb.TopicPermissionName) (*pb.Empty, error) {
	mq.WS.UnregisterTopicPermission(req.Pattern, p.name)
	return &pb.Empty{}, nil
}

// connLookup resolves a running host connection's driver, installed by serve
// once the connection manager exists. Nil until then, which the token
// callback reports as unavailable.
var connLookup func(id uuid.UUID) (any, bool)

// SetConnectionLookup installs the resolver plugin callbacks use to reach
// host connections.
func SetConnectionLookup(fn func(id uuid.UUID) (any, bool)) { connLookup = fn }

// accessTokenSource is the driver surface ConnectionAccessToken needs, met by
// the oauth2 connection kind.
type accessTokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

// ConnectionAccessToken returns an access token from a host connection that
// issues them. A plugin process cannot hold a host driver, so a plugin that
// signs requests with an oauth2 connection asks for the token here.
func (p *pluginHost) ConnectionAccessToken(ctx context.Context, req *pb.ConnectionTokenRequest) (*pb.ConnectionTokenResponse, error) {
	id, err := uuid.Parse(req.ConnId)
	if err != nil {
		return &pb.ConnectionTokenResponse{Error: fmt.Sprintf("invalid connection ID %q", req.ConnId)}, nil
	}
	if connLookup == nil {
		return &pb.ConnectionTokenResponse{Error: "connections are not available"}, nil
	}
	driver, ok := connLookup(id)
	if !ok {
		return &pb.ConnectionTokenResponse{Error: "connection is not running; is it enabled?"}, nil
	}
	src, ok := driver.(accessTokenSource)
	if !ok {
		return &pb.ConnectionTokenResponse{Error: "connection does not issue access tokens; use an oauth2 connection"}, nil
	}
	token, err := src.AccessToken(ctx)
	if err != nil {
		return &pb.ConnectionTokenResponse{Error: err.Error()}, nil
	}
	p.log.Debug("plugin read a connection access token",
		zap.String("plugin", p.name), zap.String("connection_id", id.String()))
	return &pb.ConnectionTokenResponse{Token: token}, nil
}

// TriggerSkipped records a canceled run for an event an armed plugin trigger
// skipped.
func (p *pluginHost) TriggerSkipped(_ context.Context, req *pb.TriggerSkippedRequest) (*pb.Empty, error) {
	ec, err := p.resolve(req.CtxId)
	if err != nil {
		return &pb.Empty{}, nil
	}
	rec, ok := ec.(engine.TriggerRecorder)
	if !ok {
		return &pb.Empty{}, nil
	}
	msg, err := plugin.MsgFromWire(req.Msg)
	if err != nil {
		msg = nil
	}
	rec.Skipped(msg, req.Reason)
	return &pb.Empty{}, nil
}
