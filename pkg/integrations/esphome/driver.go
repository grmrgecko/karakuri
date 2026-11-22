// Package esphome connects to ESPHome nodes over their native API: one
// connection per node, kept open with keepalives, holding the node's entities
// and their live states for the devices that use them.
package esphome

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/mycontroller-org/esphome_api/pkg/client"
	esphometypes "github.com/mycontroller-org/esphome_api/pkg/types"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// Kind is the connection kind identifier.
const Kind = "esphome"

// DefaultPort is the native API's port.
const DefaultPort = 6053

// Session timing. A node drops a client it has not heard from in about 150
// seconds; pinging every 20 and waiting 10 for the answer notices a dead node
// within 30.
const (
	backoffMin  = time.Second
	backoffMax  = 60 * time.Second
	maxEntities = 500
	clientName  = "karakuri"
)

// requestTimeout and pingInterval are variables so tests can shorten them.
var (
	requestTimeout = 10 * time.Second
	pingInterval   = 20 * time.Second
)

// Config is an ESPHome connection's settings.
type Config struct {
	Host          string `json:"host"`
	Port          int    `json:"port,omitempty"`
	EncryptionKey string `json:"encryption_key,omitempty"`
	Password      string `json:"password,omitempty"`
}

// address joins host and port.
func (c Config) address() string {
	port := c.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(strings.TrimSpace(c.Host), strconv.Itoa(port))
}

// validate checks a config before anything dials.
func (c Config) validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("esphome config requires host")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port %d is not a TCP port", c.Port)
	}
	if c.EncryptionKey != "" {
		key, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
		if err != nil || len(key) != 32 {
			return errors.New("the encryption key must be the 32-byte base64 key from the node's api: encryption: key: setting")
		}
	}
	return nil
}

// NodeInfo is what a node says about itself.
type NodeInfo struct {
	Name     string `json:"name"`
	Friendly string `json:"friendly_name,omitempty"`
	Board    string `json:"board,omitempty"`
	MAC      string `json:"mac,omitempty"`
	Version  string `json:"version,omitempty"`
}

// NodeAction is a Home Assistant style action call a node makes, from
// homeassistant.action or homeassistant.event in its YAML.
type NodeAction struct {
	Action  string         `json:"action"`
	Data    map[string]any `json:"data"`
	IsEvent bool           `json:"is_event"`
}

// Action is one of the node's user-defined actions.
type Action struct {
	Name string      `json:"name"`
	Args []ActionArg `json:"args"`
	key  uint32
}

// ActionArg is one declared argument of an action.
type ActionArg struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Driver is one ESPHome node connection.
type Driver struct {
	id      uuid.UUID
	name    string
	cfg     Config
	log     *zap.Logger
	publish func(integrations.Status)

	mu       sync.Mutex
	status   integrations.Status
	client   *client.Client
	info     NodeInfo
	entities map[string]Entity // by object ID
	byKey    map[uint32]string
	actions  map[string]Action
	values   map[string]map[string]any
	host     string // the address in use, which mDNS may have moved

	watchMu     sync.Mutex
	nextWatch   int
	watchers    map[string]map[int]func(map[string]any)
	actionWatch map[int]func(NodeAction)
	listWatch   map[int]func([]Entity)

	cancel context.CancelFunc
	done   chan struct{}

	// listing collects an entity listing in progress.
	listing *listing
}

// listing is an entity listing in progress.
type listing struct {
	entities []Entity
	actions  []Action
	done     chan struct{}
}

// New builds a driver for one connection.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := decodeConfig(model.Config, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:          model.ID,
		name:        model.Name,
		cfg:         cfg,
		log:         log.With(zap.String("connection", model.Name), zap.String("kind", Kind)),
		publish:     deps.PublishStatus,
		status:      integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		entities:    map[string]Entity{},
		byKey:       map[uint32]string{},
		actions:     map[string]Action{},
		values:      map[string]map[string]any{},
		watchers:    map[string]map[int]func(map[string]any){},
		actionWatch: map[int]func(NodeAction){},
		listWatch:   map[int]func([]Entity){},
		host:        cfg.address(),
	}, nil
}

// ID returns the connection ID.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the connection's state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Info returns what the node said about itself in the current session.
func (d *Driver) Info() NodeInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

// Entities returns the node's entities in a stable order.
func (d *Driver) Entities() []Entity {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Entity, 0, len(d.entities))
	for _, e := range d.entities {
		out = append(out, e)
	}
	sortEntities(out)
	return out
}

// Actions returns the node's user-defined actions.
func (d *Driver) Actions() []Action {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Action, 0, len(d.actions))
	for _, a := range d.actions {
		out = append(out, a)
	}
	return out
}

// setStatus records and relays a state change, collapsing repeats.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	d.mu.Unlock()
	d.log.Info("esphome connection status", zap.String("state", state), zap.String("error", errMsg))
	if d.publish != nil {
		d.publish(status)
	}
}

// Start begins connecting and supervises reconnects until Stop.
func (d *Driver) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	go d.run(ctx)
	return nil
}

// Stop ends the session.
func (d *Driver) Stop() {
	if d.cancel != nil {
		d.cancel()
		<-d.done
	}
	d.setStatus(integrations.StateDisabled, "")
}

// run keeps a session up, backing off between attempts.
func (d *Driver) run(ctx context.Context) {
	defer close(d.done)
	backoff := backoffMin
	for {
		started := time.Now()
		err := d.session(ctx)
		if ctx.Err() != nil {
			return
		}
		d.setStatus(integrations.StateError, err.Error())
		if time.Since(started) > 2*backoffMax {
			backoff = backoffMin
		}
		d.relocate(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// relocate looks the node up by name on the network when its address stops
// answering, so a node that got a new address from DHCP is followed.
func (d *Driver) relocate(ctx context.Context) {
	d.mu.Lock()
	name := d.info.Name
	d.mu.Unlock()
	if name == "" {
		return
	}
	lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	node, err := Resolve(lookup, name)
	if err != nil {
		return
	}
	addr := net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	d.mu.Lock()
	moved := addr != d.host
	d.host = addr
	d.mu.Unlock()
	if moved {
		d.log.Info("esphome node found at a new address", zap.String("node", name), zap.String("address", addr))
	}
}

// session runs one connection to the node until it fails.
func (d *Driver) session(ctx context.Context) error {
	d.setStatus(integrations.StateConnecting, "")
	d.mu.Lock()
	addr := d.host
	d.mu.Unlock()

	c, info, err := dial(addr, d.cfg, d.onMessage)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	entities, actions, err := d.list(c)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.client = c
	d.info = info
	d.entities, d.byKey, d.actions = map[string]Entity{}, map[uint32]string{}, map[string]Action{}
	for _, e := range entities {
		d.entities[e.ObjectID] = e
		d.byKey[e.Key] = e.ObjectID
	}
	for _, a := range actions {
		d.actions[a.Name] = a
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.client = nil
		d.mu.Unlock()
	}()
	d.notifyList(entities)

	if err := c.SubscribeStates(); err != nil {
		return fmt.Errorf("subscribing to states: %w", err)
	}
	if err := c.Send(&api.SubscribeHomeassistantServicesRequest{}); err != nil {
		return fmt.Errorf("subscribing to node actions: %w", err)
	}
	d.setStatus(integrations.StateConnected, "")

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.Ping(); err != nil {
				return fmt.Errorf("node %s stopped answering: %w", info.Name, err)
			}
		}
	}
}

// dial opens and authenticates a session and reads the node's information.
func dial(addr string, cfg Config, onMessage func(proto.Message)) (*client.Client, NodeInfo, error) {
	c, err := client.GetClient(clientName, addr, cfg.EncryptionKey, requestTimeout, onMessage)
	if err != nil {
		return nil, NodeInfo{}, describeDialError(addr, cfg, err)
	}
	if cfg.Password != "" {
		err = c.Login(cfg.Password)
	} else {
		_, err = c.Hello()
	}
	if err != nil {
		_ = c.Close()
		return nil, NodeInfo{}, describeDialError(addr, cfg, err)
	}
	di, err := c.DeviceInfo()
	if err != nil {
		_ = c.Close()
		return nil, NodeInfo{}, fmt.Errorf("reading node information: %w", err)
	}
	return c, NodeInfo{Name: di.Name, Friendly: di.FriendlyName, Board: di.Model, MAC: di.MacAddress, Version: di.EsphomeVersion}, nil
}

// describeDialError turns protocol errors into what an operator can act on.
func describeDialError(addr string, cfg Config, err error) error {
	switch {
	case errors.Is(err, esphometypes.ErrConnRequireEncryption):
		return errors.New("the node requires an encryption key; copy it from the node's api: encryption: key: setting")
	case errors.Is(err, esphometypes.ErrPassword):
		return errors.New("invalid password")
	case cfg.EncryptionKey != "" && (strings.HasPrefix(err.Error(), "noise:") ||
		strings.Contains(err.Error(), "MAC failure") || strings.Contains(err.Error(), "handshake")):
		// A node answers a wrong key with an error frame the Noise state
		// cannot read, so every handshake failure reads as one.
		return errors.New("encryption handshake failed: wrong key")
	case errors.Is(err, esphometypes.ErrCommunicationTimeout) && cfg.EncryptionKey == "":
		return fmt.Errorf("no answer from %s; if the node uses an encryption key, set it", addr)
	}
	return fmt.Errorf("connecting to %s: %w", addr, err)
}

// list reads the node's entities and actions.
func (d *Driver) list(c *client.Client) ([]Entity, []Action, error) {
	l := &listing{done: make(chan struct{})}
	d.mu.Lock()
	d.listing = l
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.listing = nil
		d.mu.Unlock()
	}()
	if err := c.ListEntities(); err != nil {
		return nil, nil, fmt.Errorf("listing entities: %w", err)
	}
	select {
	case <-l.done:
	case <-time.After(requestTimeout):
		return nil, nil, errors.New("listing entities: the node did not finish its list")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return l.entities, l.actions, nil
}

// onMessage handles what the node sends outside a request.
func (d *Driver) onMessage(msg proto.Message) {
	switch m := msg.(type) {
	case *api.ListEntitiesDoneResponse:
		d.mu.Lock()
		if d.listing != nil {
			close(d.listing.done)
			d.listing = nil
		}
		d.mu.Unlock()
		return
	case *api.ListEntitiesServicesResponse:
		d.mu.Lock()
		if d.listing != nil {
			a := Action{Name: m.Name, key: m.Key}
			for _, arg := range m.Args {
				a.Args = append(a.Args, ActionArg{Name: arg.Name, Type: argTypeName(arg.Type)})
			}
			d.listing.actions = append(d.listing.actions, a)
		}
		d.mu.Unlock()
		return
	case *api.HomeassistantActionRequest:
		d.notifyAction(nodeAction(m))
		return
	}
	if e, ok := entityFrom(msg); ok {
		d.mu.Lock()
		if d.listing != nil && len(d.listing.entities) < maxEntities {
			d.listing.entities = append(d.listing.entities, e)
		}
		d.mu.Unlock()
		return
	}
	keyed, ok := msg.(interface{ GetKey() uint32 })
	if !ok {
		return
	}
	d.mu.Lock()
	objectID, known := d.byKey[keyed.GetKey()]
	entity := d.entities[objectID]
	d.mu.Unlock()
	if !known {
		return
	}
	values, ok := entity.Values(msg)
	if !ok {
		return
	}
	d.mu.Lock()
	d.values[objectID] = values
	d.mu.Unlock()
	d.notifyEntity(objectID, values)
}

// WatchEntity calls fn with an entity's values, now if known and on every
// state the node reports. It returns the function that stops watching.
func (d *Driver) WatchEntity(objectID string, fn func(map[string]any)) (func(), error) {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	if d.watchers[objectID] == nil {
		d.watchers[objectID] = map[int]func(map[string]any){}
	}
	d.watchers[objectID][id] = fn
	d.watchMu.Unlock()

	d.mu.Lock()
	current, ok := d.values[objectID]
	d.mu.Unlock()
	if ok {
		fn(copyValues(current))
	}
	return func() {
		d.watchMu.Lock()
		delete(d.watchers[objectID], id)
		d.watchMu.Unlock()
	}, nil
}

// WatchActions calls fn for every action call the node makes.
func (d *Driver) WatchActions(fn func(NodeAction)) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.actionWatch[id] = fn
	d.watchMu.Unlock()
	return func() {
		d.watchMu.Lock()
		delete(d.actionWatch, id)
		d.watchMu.Unlock()
	}
}

// WatchEntityList calls fn with the node's entities after every connect.
func (d *Driver) WatchEntityList(fn func([]Entity)) func() {
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	d.listWatch[id] = fn
	d.watchMu.Unlock()
	if d.Status().State == integrations.StateConnected {
		fn(d.Entities())
	}
	return func() {
		d.watchMu.Lock()
		delete(d.listWatch, id)
		d.watchMu.Unlock()
	}
}

func (d *Driver) notifyEntity(objectID string, values map[string]any) {
	d.watchMu.Lock()
	fns := make([]func(map[string]any), 0, len(d.watchers[objectID]))
	for _, fn := range d.watchers[objectID] {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn(copyValues(values))
	}
}

func (d *Driver) notifyAction(a NodeAction) {
	d.watchMu.Lock()
	fns := make([]func(NodeAction), 0, len(d.actionWatch))
	for _, fn := range d.actionWatch {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range fns {
		fn(a)
	}
}

func (d *Driver) notifyList(entities []Entity) {
	d.watchMu.Lock()
	fns := make([]func([]Entity), 0, len(d.listWatch))
	for _, fn := range d.listWatch {
		fns = append(fns, fn)
	}
	d.watchMu.Unlock()
	sortEntities(entities)
	for _, fn := range fns {
		fn(entities)
	}
}

// EntityWritable reports whether an entity takes commands from a device,
// which its type alone does not say: a lock that requires a code takes none,
// since a device's command carries no code. known is false while the node's
// entity list does not hold the entity.
func (d *Driver) EntityWritable(objectID string) (writable, known bool) {
	d.mu.Lock()
	entity, ok := d.entities[objectID]
	d.mu.Unlock()
	if !ok {
		return false, false
	}
	return entity.Propose().Writable, true
}

// CommandEntity sends one attribute of an entity's kind to the node. The
// value is already checked against the kind; nothing is assumed about the
// result, which arrives as a state report.
func (d *Driver) CommandEntity(ctx context.Context, objectID, field string, value any) error {
	d.mu.Lock()
	c := d.client
	entity, ok := d.entities[objectID]
	name := d.info.Name
	d.mu.Unlock()
	if c == nil {
		return fmt.Errorf("ESPHome node %s is not connected", d.nodeLabel(name))
	}
	if !ok {
		return fmt.Errorf("ESPHome node %s has no entity %q", d.nodeLabel(name), objectID)
	}
	req, err := entity.Command(field, value)
	if err != nil {
		return err
	}
	return c.Send(req)
}

// nodeLabel names the node for errors, falling back to the connection name.
func (d *Driver) nodeLabel(name string) string {
	if name != "" {
		return name
	}
	return d.name
}

// nodeAction reads a node's action call.
func nodeAction(m *api.HomeassistantActionRequest) NodeAction {
	data := map[string]any{}
	for _, kv := range m.Data {
		data[kv.Key] = kv.Value
	}
	for _, kv := range m.DataTemplate {
		if _, set := data[kv.Key]; !set {
			data[kv.Key] = kv.Value
		}
	}
	return NodeAction{Action: m.Service, Data: data, IsEvent: m.IsEvent}
}

// copyValues copies a value map so watchers cannot share it.
func copyValues(v map[string]any) map[string]any {
	out := make(map[string]any, len(v))
	for k, x := range v {
		out[k] = x
	}
	return out
}

// sortEntities orders entities by name, then object ID.
func sortEntities(list []Entity) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ObjectID < list[j].ObjectID
	})
}

// SetTimingForTest shortens the ping interval and request timeout so a
// silent node is noticed in milliseconds, and returns the function that
// restores them. Only tests call it.
func SetTimingForTest(ping, request time.Duration) (restore func()) {
	oldPing, oldRequest := pingInterval, requestTimeout
	pingInterval, requestTimeout = ping, request
	return func() { pingInterval, requestTimeout = oldPing, oldRequest }
}
