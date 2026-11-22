package mq

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"go.uber.org/zap"
)

// Client tracks a single WebSocket connection and its topic subscriptions.
// Its identity fields are guarded by mu, because revalidation changes them
// while the read loop is checking subscriptions against them.
type Client struct {
	ID     string
	UserID string
	// Render names the dashboard this socket is photographing, when the token
	// it connected with was a render job's rather than a session's. It holds
	// no account, so this is the whole of what it may read.
	Render      string
	RenderDraft bool
	// token is the credential the identity came from, kept so revalidation
	// can ask again whether it still holds: a session can be deleted or
	// expire, and a render job settles.
	token string
	conn  *websocket.Conn
	mu    sync.RWMutex
	subs  map[string]string // topic -> broker subscription ID
	// dashboards remembers which dashboard each subscription named, so a
	// recheck asks the same question the subscribe did.
	dashboards map[string]string
	// send queues broker messages for the socket's own writer, so a stalled
	// client backs up its queue rather than the goroutine publishing to it.
	send chan *Message
}

// identity is a snapshot of who a client is, read under its lock.
type identity struct {
	userID, render string
	draft          bool
}

func (c *Client) identity() identity {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return identity{userID: c.UserID, render: c.Render, draft: c.RenderDraft}
}

// revalidateInterval is how often every socket's credential and subscriptions
// are checked again. Changes that revoke access also trigger a recheck at
// once, so this bounds what a timer alone has to catch, such as an access
// token reaching its expiry.
const revalidateInterval = 30 * time.Second

// recheckSettle is how long a requested recheck waits for further requests,
// so a burst of writes costs one pass.
const recheckSettle = 250 * time.Millisecond

// clientQueueDepth is how many broker messages may wait for one socket. A
// full queue drops the newest message: the socket is not keeping up, and a
// publisher blocked on it would stall every other subscriber, including the
// MQTT dispatch loop that feeds device state.
const clientQueueDepth = 256

// TopicPermission gates subscription to topics matching a pattern.
type TopicPermission struct {
	Pattern    string
	Permission string                          // role-based permission string; empty means use Checker
	Checker    func(userID, topic string) bool // callback-based check
	// Source names the plugin that registered the gate, empty for the
	// built-in set. Unloading a plugin withdraws its gates by source.
	Source string
}

// WS is the package-level WebSocket hub initialized by InitHub.
var WS = &Hub{
	clients: make(map[string]*Client),
	recheck: make(chan struct{}, 1),
}

// Hub manages WebSocket clients, delegating pub/sub to the package-level broker.
type Hub struct {
	clients    map[string]*Client
	mu         sync.RWMutex
	log        *zap.Logger
	topicPerms []TopicPermission
	permMu     sync.RWMutex
	// dashboardScope is the fallback for a client that holds no permission for
	// a topic but is displaying a dashboard that exposes it. It is a callback
	// so the hub stays free of the dashboard packages; serve.go wires it.
	dashboardScope func(userID, dashboard, topic string) bool
	// renderScope is the same fallback for a headless render, which passes the
	// view check with its token instead of an account.
	renderScope func(dashboard, topic string, draft bool) bool
	// renderAuth resolves a render job's token into the dashboard it names.
	renderAuth func(token string) (string, bool)
	// messageFilters decide per message, after a subscription was admitted,
	// whether one client may receive it. A topic such as the event log carries
	// entries about many subjects, and some of them follow per-object access.
	messageFilters []messageFilter
	// recheck asks the watcher for a revalidation pass now. It holds one
	// pending request, so a burst of changes runs one pass.
	recheck   chan struct{}
	watchOnce sync.Once
}

// SetDashboardScope installs the dashboard-grant fallback for topic checks.
func (h *Hub) SetDashboardScope(fn func(userID, dashboard, topic string) bool) {
	h.permMu.Lock()
	h.dashboardScope = fn
	h.permMu.Unlock()
}

// SetRenderScope installs the same fallback for a headless render's socket.
func (h *Hub) SetRenderScope(fn func(dashboard, topic string, draft bool) bool) {
	h.permMu.Lock()
	h.renderScope = fn
	h.permMu.Unlock()
}

// SetRenderAuth installs the resolver that turns a render job's token into the
// dashboard it grants a view of. It is a callback for the same reason the
// scope checks are: the hub knows nothing about dashboards.
func (h *Hub) SetRenderAuth(fn func(token string) (string, bool)) {
	h.permMu.Lock()
	h.renderAuth = fn
	h.permMu.Unlock()
}

// InitHub wires the package-level WS hub with the given logger and starts
// the watcher that revalidates connected sockets.
func InitHub(log *zap.Logger) {
	WS.log = log
	WS.watchOnce.Do(func() { go WS.watch() })
}

// Recheck asks for every socket's credential and subscriptions to be checked
// again now, without waiting for the next interval. Callers that revoke
// access, such as a deleted session, a changed share, or an unloaded plugin,
// call it so a socket that was already open loses what it no longer may read.
func (h *Hub) Recheck() {
	select {
	case h.recheck <- struct{}{}:
	default:
	}
}

// watch revalidates every socket on an interval and whenever Recheck asks.
func (h *Hub) watch() {
	ticker := time.NewTicker(revalidateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-h.recheck:
			// Writes come in bursts, such as an editor dragging a widget;
			// a short wait lets one pass cover them all.
			time.Sleep(recheckSettle)
			select {
			case <-h.recheck:
			default:
			}
		}
		h.revalidateAll()
	}
}

// revalidateAll checks every connected socket.
func (h *Hub) revalidateAll() {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		h.revalidate(c)
	}
}

// revalidate asks again whether a socket's credential holds and whether each
// subscription is still allowed. A session that no longer validates leaves the
// socket anonymous and tells the client, which can send a refreshed token; a
// render whose job has settled is closed, since its token is all it had.
// Subscriptions that fail are withdrawn and the client is told which.
func (h *Hub) revalidate(client *Client) {
	client.mu.Lock()
	token, render := client.token, client.Render
	client.mu.Unlock()

	switch {
	case render != "":
		resolver := h.renderResolver()
		if resolver == nil {
			break
		}
		dashboard, draft := resolver(token)
		if dashboard != render || draft != client.identity().draft {
			h.log.Debug("closing a render socket whose job has settled", zap.String("client", client.ID))
			_ = client.conn.Close(websocket.StatusPolicyViolation, "render job settled")
			return
		}
	case token != "":
		if _, _, err := auth.ValidateToken(token); err != nil {
			client.mu.Lock()
			client.UserID = ""
			client.token = ""
			client.mu.Unlock()
			h.log.Debug("websocket session no longer valid", zap.String("client", client.ID), zap.Error(err))
			h.queue(client, &Message{Type: "auth_expired"})
		}
	}

	client.mu.RLock()
	topics := make(map[string]string, len(client.subs))
	for topic := range client.subs {
		topics[topic] = client.dashboards[topic]
	}
	client.mu.RUnlock()
	for topic, dashboard := range topics {
		if h.checkTopicPermission(client, topic, dashboard) {
			continue
		}
		h.unsubscribe(client, topic)
		h.log.Debug("withdrew a websocket subscription", zap.String("client", client.ID), zap.String("topic", topic))
		h.queue(client, &Message{Type: "revoked", Topic: topic})
	}
}

// authenticate replaces a socket's identity with the session a fresh token
// names, then rechecks its subscriptions under it. A render socket keeps its
// job identity; a token that does not validate leaves the socket anonymous.
func (h *Hub) authenticate(client *Client, raw json.RawMessage) {
	var token string
	_ = json.Unmarshal(raw, &token)
	if client.identity().render != "" {
		return
	}
	claims, _, err := auth.ValidateToken(token)
	client.mu.Lock()
	if err != nil {
		client.UserID = ""
		client.token = ""
	} else {
		client.UserID = claims.UserID.String()
		client.token = token
	}
	client.mu.Unlock()
	status := `"ok"`
	if err != nil {
		status = `"denied"`
	}
	h.queue(client, &Message{Type: "auth", Payload: json.RawMessage(status)})
	h.revalidate(client)
}

// queue hands a control message to the client's writer, dropping it if the
// queue is full like any other message.
func (h *Hub) queue(client *Client, msg *Message) {
	select {
	case client.send <- msg:
	default:
	}
}

// Publish sends a message to all broker subscribers of the given topic.
func (h *Hub) Publish(topic string, msgType string, payload interface{}) {
	B.Publish(topic, msgType, payload)
}

// messageFilter screens messages on topics matching Pattern.
type messageFilter struct {
	Pattern string
	Allow   func(userID string, msg *Message) bool
}

// RegisterMessageFilter screens every message on topics matching pattern
// before it reaches a subscribed client. Allow receives the client's user ID,
// empty for an anonymous or render socket, and the message; returning false
// drops the message for that client only.
func (h *Hub) RegisterMessageFilter(pattern string, allow func(userID string, msg *Message) bool) {
	h.permMu.Lock()
	defer h.permMu.Unlock()
	h.messageFilters = append(h.messageFilters, messageFilter{Pattern: pattern, Allow: allow})
}

// messageAllowed applies the registered message filters for one client.
func (h *Hub) messageAllowed(client *Client, msg *Message) bool {
	h.permMu.RLock()
	filters := h.messageFilters
	h.permMu.RUnlock()
	var userID string
	for _, f := range filters {
		if !Match(f.Pattern, msg.Topic) {
			continue
		}
		if userID == "" {
			userID = client.identity().userID
		}
		if !f.Allow(userID, msg) {
			return false
		}
	}
	return true
}

// RegisterTopicPermission adds a role-based permission gate for topics matching the pattern.
func (h *Hub) RegisterTopicPermission(pattern, permission string) {
	h.registerPerm(TopicPermission{Pattern: pattern, Permission: permission})
}

// RegisterTopicPermissionFromSource adds a role-based gate tagged with what
// registered it, so a plugin's gates can be withdrawn when it unloads.
func (h *Hub) RegisterTopicPermissionFromSource(pattern, permission, source string) {
	h.registerPerm(TopicPermission{Pattern: pattern, Permission: permission, Source: source})
}

// RegisterTopicPermissionCallback adds a callback-based permission gate for topics matching the pattern.
func (h *Hub) RegisterTopicPermissionCallback(pattern string, checker func(userID, topic string) bool) {
	h.registerPerm(TopicPermission{Pattern: pattern, Checker: checker})
}

// registerPerm stores a gate, replacing an identical pattern from the same
// source so reloading a plugin does not stack duplicates.
func (h *Hub) registerPerm(p TopicPermission) {
	h.permMu.Lock()
	defer h.permMu.Unlock()
	for i, existing := range h.topicPerms {
		if existing.Pattern == p.Pattern && existing.Source == p.Source {
			h.topicPerms[i] = p
			return
		}
	}
	h.topicPerms = append(h.topicPerms, p)
}

// UnregisterTopicPermissionsBySource removes every gate a source registered.
func (h *Hub) UnregisterTopicPermissionsBySource(source string) {
	if source == "" {
		return
	}
	h.permMu.Lock()
	defer h.permMu.Unlock()
	kept := h.topicPerms[:0]
	for _, p := range h.topicPerms {
		if p.Source != source {
			kept = append(kept, p)
		}
	}
	h.topicPerms = kept
	// Sockets subscribed through a withdrawn gate lose it now, not at the
	// next interval.
	h.Recheck()
}

// UnregisterTopicPermission removes one gate a source registered.
func (h *Hub) UnregisterTopicPermission(pattern, source string) {
	h.permMu.Lock()
	defer h.permMu.Unlock()
	kept := h.topicPerms[:0]
	for _, p := range h.topicPerms {
		if p.Pattern != pattern || p.Source != source {
			kept = append(kept, p)
		}
	}
	h.topicPerms = kept
}

// HandleWebSocket upgrades the HTTP connection to WebSocket and enters the read loop.
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		h.log.Error("failed to accept websocket", zap.Error(err))
		return
	}

	// Authenticate from query parameter if present. A session token names a
	// user; a render job's token names a dashboard instead, which is what lets
	// a headless render of a private dashboard watch the values it draws.
	var userID, renderFor, credential string
	var renderDraft bool
	if token := r.URL.Query().Get("token"); token != "" {
		if claims, _, err := auth.ValidateToken(token); err == nil {
			userID = claims.UserID.String()
			credential = token
		} else if resolver := h.renderResolver(); resolver != nil {
			renderFor, renderDraft = resolver(token)
			if renderFor != "" {
				credential = token
			}
		}
	}

	clientID := uuid.New().String()
	client := &Client{
		ID:          clientID,
		UserID:      userID,
		Render:      renderFor,
		RenderDraft: renderDraft,
		token:       credential,
		conn:        conn,
		subs:        make(map[string]string),
		dashboards:  make(map[string]string),
		send:        make(chan *Message, clientQueueDepth),
	}

	h.mu.Lock()
	h.clients[clientID] = client
	h.mu.Unlock()

	h.log.Debug("websocket connected", zap.String("client", clientID))

	defer func() {
		h.removeClient(client)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		h.log.Debug("websocket disconnected", zap.String("client", clientID))
	}()

	ctx := r.Context()
	go h.writeLoop(ctx, client)
	for {
		var msg Message
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			if websocket.CloseStatus(err) != -1 {
				break
			}
			h.log.Debug("websocket read error", zap.Error(err))
			break
		}

		h.handleMessage(client, &msg)
	}
}

// writeLoop drains one client's queue onto its socket until the connection
// ends.
func (h *Hub) writeLoop(ctx context.Context, client *Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-client.send:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := wsjson.Write(wctx, client.conn, msg)
			cancel()
			if err != nil {
				h.log.Debug("failed to write to client", zap.String("client", client.ID), zap.Error(err))
			}
		}
	}
}

func (h *Hub) handleMessage(client *Client, msg *Message) {
	switch msg.Type {
	case "subscribe":
		h.subscribe(client, msg.Topic, msg.Dashboard)
	case "unsubscribe":
		h.unsubscribe(client, msg.Topic)
	case "auth":
		h.authenticate(client, msg.Payload)
	case "ping":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = wsjson.Write(ctx, client.conn, Message{Type: "pong"})
		cancel()
	}
}

// checkTopicPermission evaluates registered topic permissions for the given
// client and topic. Returns true if the subscription should be allowed.
//
// A client that fails the permission check gets a second answer when it named
// the dashboard it is displaying: a viewer holds no subsystem permission, and
// the live half of a value the dashboard already shows over HTTP is granted by
// the same binding rather than by a permission they were never going to have.
func (h *Hub) checkTopicPermission(client *Client, topic, dashboard string) bool {
	h.permMu.RLock()
	defer h.permMu.RUnlock()

	id := client.identity()
	if !h.permittedTopic(id, topic) {
		if dashboard == "" {
			return false
		}
		if id.render != "" {
			return id.render == dashboard && h.renderScope != nil &&
				h.renderScope(dashboard, topic, id.draft)
		}
		if h.dashboardScope == nil {
			return false
		}
		return h.dashboardScope(id.userID, dashboard, topic)
	}
	return true
}

// renderResolver returns the render token resolver under the permission lock.
func (h *Hub) renderResolver() func(token string) (string, bool) {
	h.permMu.RLock()
	defer h.permMu.RUnlock()
	return h.renderAuth
}

// permittedTopic evaluates the registered gates alone. The caller holds the
// permission lock.
//
// A topic no gate matches is denied. The broker carries run events, connection
// status, entity state, and whatever a plugin publishes, and this is the only
// place a WebSocket client is stopped from reading any of it — so an
// unrecognised topic has to fail closed. The cost is that a plugin publishing
// on a namespace it never registered finds its own widget cut off, which is
// the failure worth having: the other direction hands every topic anyone ever
// adds to any anonymous socket.
func (h *Hub) permittedTopic(client identity, topic string) bool {
	matched := false
	for _, tp := range h.topicPerms {
		if !Match(tp.Pattern, topic) {
			continue
		}
		matched = true
		// A matching permission was found; check it.
		if tp.Permission != "" {
			if client.userID == "" {
				return false
			}
			uid, err := uuid.Parse(client.userID)
			if err != nil {
				return false
			}
			if !auth.HasPermission(uid, tp.Permission) {
				return false
			}
		} else if tp.Checker != nil {
			if !tp.Checker(client.userID, topic) {
				return false
			}
		} else {
			// A gate naming neither a permission nor a checker was registered
			// wrong. The rule is fail closed, so it admits nobody rather than
			// everybody.
			return false
		}
	}
	return matched
}

func (h *Hub) subscribe(client *Client, topic, dashboard string) {
	// Check topic permissions before subscribing.
	if !h.checkTopicPermission(client, topic, dashboard) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = wsjson.Write(ctx, client.conn, Message{Type: "error", Topic: topic, Payload: json.RawMessage(`"permission denied"`)})
		cancel()
		return
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	// Already subscribed to this topic.
	if _, exists := client.subs[topic]; exists {
		return
	}

	// Subscribe via the broker; the handler forwards messages to the WebSocket client.
	id := B.Subscribe(topic, func(msg *Message) {
		if !h.messageAllowed(client, msg) {
			return
		}
		select {
		case client.send <- msg:
		default:
			h.log.Debug("dropping a message for a client that is not keeping up",
				zap.String("client", client.ID), zap.String("topic", msg.Topic))
		}
	})

	client.subs[topic] = id
	client.dashboards[topic] = dashboard
}

func (h *Hub) unsubscribe(client *Client, topic string) {
	client.mu.Lock()
	defer client.mu.Unlock()

	if id, ok := client.subs[topic]; ok {
		B.Unsubscribe(id)
		delete(client.subs, topic)
		delete(client.dashboards, topic)
	}
}

func (h *Hub) removeClient(client *Client) {
	client.mu.Lock()
	for topic, id := range client.subs {
		B.Unsubscribe(id)
		delete(client.subs, topic)
	}
	client.mu.Unlock()

	h.mu.Lock()
	delete(h.clients, client.ID)
	h.mu.Unlock()
}
