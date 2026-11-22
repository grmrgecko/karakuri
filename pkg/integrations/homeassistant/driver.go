// Package homeassistant implements the Home Assistant connection driver as a
// hand-rolled WebSocket client speaking the HA WebSocket API: token auth
// handshake, id-correlated commands, shared event subscriptions with fan-out,
// and a reconnect supervisor with exponential backoff and ping keepalives.
package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"go.uber.org/zap"
)

// Kind is the driver kind registered with the integrations registry.
const Kind = "homeassistant"

// Protocol timing defaults. Ping, backoff, and the command timeouts are driver
// fields so tests can shorten them.
const (
	dialTimeout    = 10 * time.Second
	commandTimeout = 10 * time.Second
	// serviceCallTimeout bounds a call_service reply. Home Assistant answers a
	// command only once the work behind it finishes, and a service call reaches
	// real devices: a light group on a slow Zigbee mesh routinely takes longer
	// than the generic command timeout, so the lights turn on and the caller is
	// told the call timed out. It stays under the HTTP server's 30s write
	// timeout so the dashboard receives the error instead of a cut connection.
	serviceCallTimeout  = 25 * time.Second
	defaultPingInterval = 30 * time.Second
	defaultPingTimeout  = 10 * time.Second
	defaultBackoff      = time.Second
	maxBackoff          = 30 * time.Second
	// statesCacheTTL bounds how long the entity picker's state list is reused.
	statesCacheTTL = 30 * time.Second
	// readLimit allows large get_states responses from big installations.
	readLimit = 32 << 20
	// deliveryQueueSize bounds the per-connection event backlog. Handlers run
	// on the driver's own goroutine rather than the read loop, which also
	// resolves command replies: blocking it would stall every command and trip
	// the ping keepalive into a needless reconnect.
	deliveryQueueSize = 1024
)

// Config is the driver configuration stored (encrypted) on a connection.
type Config struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
}

// Validate checks that the config is complete and the base URL is usable.
func (c Config) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("homeassistant config has no base_url")
	}
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("homeassistant config has no token")
	}
	_, err := WebsocketURL(c.BaseURL)
	return err
}

// WebsocketURL normalizes a Home Assistant base URL ("http://ha.local:8123",
// with or without scheme or trailing slash) into the ws(s):// API endpoint.
func WebsocketURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", fmt.Errorf("empty base URL")
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", base, err)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported base URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("base URL %q has no host", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/websocket"
	return u.String(), nil
}

// Entity is one Home Assistant entity state as returned by get_states.
type Entity struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

// wsMsg is the generic Home Assistant WebSocket message envelope.
type wsMsg struct {
	ID      int64           `json:"id,omitempty"`
	Type    string          `json:"type"`
	Success bool            `json:"success,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wsError        `json:"error,omitempty"`
	Event   json.RawMessage `json:"event,omitempty"`
	Message string          `json:"message,omitempty"`
}

// wsError is the error object carried by failed command results.
type wsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// eventSub is one registered handler for an event type.
type eventSub struct {
	eventType string
	handler   func(event json.RawMessage)
}

// delivery is one received event queued for handler dispatch, with the
// handlers resolved at receive time so a later unsubscribe cannot redirect it.
type delivery struct {
	generation uint64
	event      json.RawMessage
	handlers   []func(event json.RawMessage)
	// run, when set, is work that must happen in order with events, such
	// as a reconnect's catch-up.
	run func()
}

// Driver is a running Home Assistant connection shared by all nodes
// referencing it. One server-side event subscription exists per distinct
// event type; received events fan out to every registered handler.
type Driver struct {
	id    uuid.UUID
	cfg   Config
	wsURL string
	log   *zap.Logger

	publishStatus func(integrations.Status)

	// Timing knobs, set before Start; tests shorten them.
	pingInterval   time.Duration
	pingTimeout    time.Duration
	initialBackoff time.Duration
	cmdTimeout     time.Duration
	svcTimeout     time.Duration

	mu         sync.Mutex
	generation uint64
	status     integrations.Status
	conn       *websocket.Conn
	nextCmdID  int64
	pending    map[int64]chan wsMsg
	subs       map[int]*eventSub
	nextSub    int
	subIDs     map[string]int64 // event type -> live server subscription id
	subTypes   map[int64]string // live server subscription id -> event type
	subBusy    map[string]bool  // event types with a subscribe command in flight
	dropped    int
	ctx        context.Context
	cancel     context.CancelFunc

	// deliveries hands events to the dispatch goroutine so handlers never run
	// on the read loop, which also resolves command replies and pongs.
	deliveries chan delivery

	statesCache   []Entity
	statesCacheAt time.Time

	// stateHandlers receive state changes, live and caught up; stateUnsub
	// drops the one state_changed subscription they share.
	stateHandlers map[int]func(StateChange)
	nextState     int
	stateUnsub    func()
	// stateSubMu serializes opening and dropping that subscription, so
	// handlers arriving or leaving together agree on whether it exists.
	stateSubMu sync.Mutex
	// syncWatchers run on the dispatch goroutine after every full state
	// read, seed or catch-up, once the known states hold it.
	syncWatchers map[int]func()
	nextSync     int
	// memory is the last state seen of every entity, shared with any later
	// driver for the same connection and server.
	memory *stateMemory

	// reg is the copy of Home Assistant's registries; regDebounce is how long
	// an update event waits for others before the registry is reread.
	reg         registryState
	regDebounce time.Duration
}

// New builds a Home Assistant driver from a connection model. Registered as
// the factory for the "homeassistant" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid homeassistant config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	wsURL, err := WebsocketURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:             model.ID,
		cfg:            cfg,
		wsURL:          wsURL,
		log:            log,
		publishStatus:  deps.PublishStatus,
		pingInterval:   defaultPingInterval,
		pingTimeout:    defaultPingTimeout,
		initialBackoff: defaultBackoff,
		cmdTimeout:     commandTimeout,
		svcTimeout:     serviceCallTimeout,
		status:         integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		pending:        make(map[int64]chan wsMsg),
		subs:           make(map[int]*eventSub),
		subIDs:         make(map[string]int64),
		subTypes:       make(map[int64]string),
		subBusy:        make(map[string]bool),
		deliveries:     make(chan delivery, deliveryQueueSize),
		stateHandlers:  make(map[int]func(StateChange)),
		syncWatchers:   make(map[int]func()),
		memory:         memoryFor(model.ID.String() + "|" + wsURL),
		regDebounce:    registryDebounce,
	}, nil
}

// ID returns the connection's database identifier.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the connection's current lifecycle state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// setStatus records a state transition and relays it to the manager. Repeated
// identical transitions (e.g. failing reconnect attempts) are collapsed.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	publish := d.publishStatus
	d.mu.Unlock()

	d.log.Info("homeassistant connection status", zap.String("state", state), zap.String("error", errMsg))
	if publish != nil {
		publish(status)
	}
}

// Start launches the reconnect supervisor. It returns immediately; connection
// establishment and reconnects run in the background until Stop.
func (d *Driver) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.ctx = runCtx
	d.cancel = cancel
	d.mu.Unlock()

	d.setStatus(integrations.StateConnecting, "")
	d.subscribeRegistryEvents()
	go d.dispatchLoop(runCtx)
	go d.supervise(runCtx)
	return nil
}

// Stop tears the connection down and marks it disabled.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	conn := d.conn
	d.cancel = nil
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "shutting down")
	}
	d.setStatus(integrations.StateDisabled, "")
}

// supervise runs connect sessions until the context is canceled, backing off
// exponentially (1s doubling, capped at 30s) between failed attempts. The
// backoff resets after any session that reached the connected state.
func (d *Driver) supervise(ctx context.Context) {
	backoff := d.initialBackoff
	for {
		connected, err := d.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = d.initialBackoff
			// The session had reached connected, so this is a link that
			// dropped and will be reestablished rather than a failed attempt.
			metrics.ConnectionReconnectsTotal.WithLabelValues(Kind).Inc()
		}
		errMsg := "connection lost"
		if err != nil {
			errMsg = err.Error()
		}
		d.setStatus(integrations.StateError, errMsg)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// session runs one connection lifetime: dial, authenticate, resubscribe
// registered event types, then read until the socket dies. It reports whether
// the session reached the connected state.
func (d *Driver) session(ctx context.Context) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, _, err := websocket.Dial(dialCtx, d.wsURL, nil)
	cancel()
	if err != nil {
		return false, fmt.Errorf("failed to connect to %s: %w", d.wsURL, err)
	}
	conn.SetReadLimit(readLimit)

	if err := authenticate(ctx, conn, d.cfg.Token); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "auth failed")
		return false, err
	}

	// Install the live socket; live subscriptions start empty per session.
	d.mu.Lock()
	d.conn = conn
	d.generation++
	d.subIDs = make(map[string]int64)
	d.subTypes = make(map[int64]string)
	d.mu.Unlock()
	d.setStatus(integrations.StateConnected, "")
	d.log.Debug("homeassistant session established")

	// Keepalive pings force a reconnect when the server stops answering.
	sessCtx, sessCancel := context.WithCancel(ctx)
	go d.pingLoop(sessCtx, conn)

	// Reestablish one server subscription per registered event type. Must run
	// concurrently with the read loop, which delivers the command replies.
	go d.resubscribe(sessCtx)

	err = d.readLoop(ctx, conn)
	sessCancel()
	d.teardownSession(conn)
	return true, err
}

// authenticate performs the auth_required/auth/auth_ok handshake.
func authenticate(ctx context.Context, conn *websocket.Conn, token string) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var hello wsMsg
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		return fmt.Errorf("failed to read auth handshake: %w", err)
	}
	if hello.Type != "auth_required" {
		return fmt.Errorf("unexpected handshake message %q", hello.Type)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"type": "auth", "access_token": token}); err != nil {
		return fmt.Errorf("failed to send auth: %w", err)
	}
	var reply wsMsg
	if err := wsjson.Read(ctx, conn, &reply); err != nil {
		return fmt.Errorf("failed to read auth reply: %w", err)
	}
	switch reply.Type {
	case "auth_ok":
		return nil
	case "auth_invalid":
		msg := reply.Message
		if msg == "" {
			msg = "invalid access token"
		}
		return fmt.Errorf("authentication failed: %s", msg)
	default:
		return fmt.Errorf("unexpected auth reply %q", reply.Type)
	}
}

// readLoop dispatches incoming messages until the socket errors: events fan
// out to subscription handlers, everything id-correlated resolves a pending
// command.
func (d *Driver) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		var msg wsMsg
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return fmt.Errorf("connection lost: %w", err)
		}
		switch msg.Type {
		case "event":
			d.dispatchEvent(msg)
		default:
			d.mu.Lock()
			ch := d.pending[msg.ID]
			delete(d.pending, msg.ID)
			d.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
}

// teardownSession clears the live socket and fails every pending command so
// callers do not hang across a reconnect.
func (d *Driver) teardownSession(conn *websocket.Conn) {
	d.mu.Lock()
	if d.conn == conn {
		d.conn = nil
		d.generation++
	}
	pending := d.pending
	d.pending = make(map[int64]chan wsMsg)
	d.subIDs = make(map[string]int64)
	d.subTypes = make(map[int64]string)
	// States fetched before the outage may describe a world that changed while
	// the socket was down; the next read refetches instead of serving them.
	d.statesCache = nil
	d.mu.Unlock()

	_ = conn.Close(websocket.StatusNormalClosure, "")
	for _, ch := range pending {
		close(ch)
	}
}

// pingLoop sends a protocol ping on an interval and forces the socket closed
// when the pong does not arrive in time, triggering a reconnect.
func (d *Driver) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(d.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, d.pingTimeout)
			_, _, err := d.command(pingCtx, map[string]any{"type": "ping"}, nil)
			cancel()
			if err != nil && ctx.Err() == nil {
				d.log.Warn("homeassistant ping failed; forcing reconnect", zap.Error(err))
				_ = conn.Close(websocket.StatusGoingAway, "ping timeout")
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// command sends one id-correlated command and waits the default reply timeout.
func (d *Driver) command(ctx context.Context, cmd map[string]any, onID func(id int64)) (wsMsg, int64, error) {
	return d.commandWithin(ctx, d.cmdTimeout, cmd, onID)
}

// commandWithin sends one id-correlated command and waits up to timeout for its
// reply. onID runs under the driver lock after the id is assigned and before
// the write, so callers can register id-keyed routing without racing the reply.
// Failed results are returned as errors carrying the server's message.
func (d *Driver) commandWithin(ctx context.Context, timeout time.Duration, cmd map[string]any, onID func(id int64)) (wsMsg, int64, error) {
	d.mu.Lock()
	conn := d.conn
	if conn == nil {
		d.mu.Unlock()
		return wsMsg{}, 0, fmt.Errorf("home assistant connection is not established")
	}
	d.nextCmdID++
	id := d.nextCmdID
	ch := make(chan wsMsg, 1)
	d.pending[id] = ch
	if onID != nil {
		onID(id)
	}
	d.mu.Unlock()

	cmd["id"] = id
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := wsjson.Write(ctx, conn, cmd); err != nil {
		d.dropPending(id)
		return wsMsg{}, id, fmt.Errorf("failed to send command: %w", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return wsMsg{}, id, fmt.Errorf("connection lost while waiting for reply")
		}
		if msg.Type == "result" && !msg.Success {
			if msg.Error != nil {
				return msg, id, fmt.Errorf("home assistant error: %s", msg.Error.Message)
			}
			return msg, id, fmt.Errorf("home assistant command failed")
		}
		return msg, id, nil
	case <-ctx.Done():
		d.dropPending(id)
		// The caller going away and the reply being late are different
		// problems; reporting both as a timeout hides a canceled request.
		if parent.Err() != nil {
			return wsMsg{}, id, fmt.Errorf("home assistant command canceled: %w", parent.Err())
		}
		return wsMsg{}, id, fmt.Errorf("home assistant command timed out after %s", timeout)
	}
}

// dropPending removes one pending command entry.
func (d *Driver) dropPending(id int64) {
	d.mu.Lock()
	delete(d.pending, id)
	d.mu.Unlock()
}

// CallService invokes a Home Assistant service and returns the result value.
// Failed calls (success:false) return errors with the server's message. The
// reply waits out serviceCallTimeout rather than the generic command timeout
// because Home Assistant holds it until the service handler has finished
// driving the devices behind it.
func (d *Driver) CallService(ctx context.Context, domain, service string, serviceData map[string]any, target map[string]any) (any, error) {
	cmd := map[string]any{"type": "call_service", "domain": domain, "service": service}
	if len(serviceData) > 0 {
		cmd["service_data"] = serviceData
	}
	if len(target) > 0 {
		cmd["target"] = target
	}
	msg, _, err := d.commandWithin(ctx, d.svcTimeout, cmd, nil)
	if err != nil {
		return nil, err
	}
	var result any
	if len(msg.Result) > 0 {
		_ = json.Unmarshal(msg.Result, &result)
	}
	return result, nil
}

// GetServices returns Home Assistant's service descriptions keyed by domain and name.
// Raw descriptions preserve selectors and fields added by installed integrations.
func (d *Driver) GetServices(ctx context.Context) (map[string]map[string]json.RawMessage, error) {
	msg, _, err := d.command(ctx, map[string]any{"type": "get_services"}, nil)
	if err != nil {
		return nil, err
	}
	var services map[string]map[string]json.RawMessage
	if err := json.Unmarshal(msg.Result, &services); err != nil {
		return nil, fmt.Errorf("invalid get_services result: %w", err)
	}
	return services, nil
}

// GetStates fetches the current state of every entity.
func (d *Driver) GetStates(ctx context.Context) ([]Entity, error) {
	msg, _, err := d.command(ctx, map[string]any{"type": "get_states"}, nil)
	if err != nil {
		return nil, err
	}
	var entities []Entity
	if err := json.Unmarshal(msg.Result, &entities); err != nil {
		return nil, fmt.Errorf("invalid get_states result: %w", err)
	}
	return entities, nil
}

// StatesCached returns the entity states, reusing the previous fetch for up
// to 30 seconds. Used by the entity autocomplete API to keep the picker snappy.
func (d *Driver) StatesCached(ctx context.Context) ([]Entity, error) {
	d.mu.Lock()
	if d.statesCache != nil && time.Since(d.statesCacheAt) < statesCacheTTL {
		cached := d.statesCache
		d.mu.Unlock()
		return cached, nil
	}
	d.mu.Unlock()

	entities, err := d.GetStates(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.statesCache = entities
	d.statesCacheAt = time.Now()
	d.mu.Unlock()
	return entities, nil
}

// SubscribeEvents registers a handler for a Home Assistant event type and
// returns an unsubscribe function. Handlers for the same event type share one
// server subscription; the server subscription is dropped once the last
// handler unsubscribes and reestablished on every reconnect.
func (d *Driver) SubscribeEvents(eventType string, handler func(event json.RawMessage)) (func(), error) {
	if eventType == "" {
		return nil, fmt.Errorf("empty event type")
	}
	if handler == nil {
		return nil, fmt.Errorf("nil event handler")
	}

	d.mu.Lock()
	handle := d.nextSub
	d.nextSub++
	d.subs[handle] = &eventSub{eventType: eventType, handler: handler}
	connected := d.conn != nil
	ctx := d.ctx
	d.mu.Unlock()

	// Establish the server subscription now when live; reconnects handle the
	// rest through resubscribe.
	if connected && ctx != nil {
		go func() {
			if err := d.subscribeEventType(ctx, eventType); err != nil {
				d.log.Warn("homeassistant subscribe failed",
					zap.String("event_type", eventType), zap.Error(err))
			}
		}()
	}

	unsubscribe := func() {
		d.mu.Lock()
		delete(d.subs, handle)
		last := true
		for _, s := range d.subs {
			if s.eventType == eventType {
				last = false
				break
			}
		}
		subID, live := d.subIDs[eventType]
		if last && live {
			delete(d.subIDs, eventType)
			delete(d.subTypes, subID)
		}
		ctx := d.ctx
		d.mu.Unlock()

		if last && live && ctx != nil {
			go func() {
				if _, _, err := d.command(ctx, map[string]any{
					"type": "unsubscribe_events", "subscription": subID,
				}, nil); err != nil {
					d.log.Debug("homeassistant unsubscribe failed",
						zap.String("event_type", eventType), zap.Error(err))
				}
			}()
		}
	}
	return unsubscribe, nil
}

// StateChange carries the decoded payload of one state_changed event.
type stateChangedEvent struct {
	Data struct {
		EntityID string          `json:"entity_id"`
		OldState json.RawMessage `json:"old_state"`
		NewState json.RawMessage `json:"new_state"`
	} `json:"data"`
}

// SubscribeStateChanges registers a handler for state changes with the entity
// and old/new state objects extracted. Changes caught up after a reconnect
// arrive like live ones; SubscribeStateChangesDetailed tells them apart.
func (d *Driver) SubscribeStateChanges(handler func(entityID string, oldState, newState json.RawMessage)) (func(), error) {
	if handler == nil {
		return nil, fmt.Errorf("nil state change handler")
	}
	return d.SubscribeStateChangesDetailed(func(c StateChange) { handler(c.EntityID, c.Old, c.New) })
}

// subscribeEventType issues one subscribe_events command for an event type,
// deduplicating against live and in-flight subscriptions. The command id
// doubles as the server subscription id and is registered for event routing
// before the command is sent so no event can slip past the dispatcher.
func (d *Driver) subscribeEventType(ctx context.Context, eventType string) error {
	for {
		d.mu.Lock()
		if _, live := d.subIDs[eventType]; live || d.subBusy[eventType] {
			d.mu.Unlock()
			return nil
		}
		d.subBusy[eventType] = true
		d.mu.Unlock()

		var subID int64
		_, _, err := d.command(ctx, map[string]any{
			"type": "subscribe_events", "event_type": eventType,
		}, func(id int64) {
			// Runs under the driver lock during command id assignment.
			subID = id
			d.subIDs[eventType] = id
			d.subTypes[id] = eventType
		})

		d.mu.Lock()
		delete(d.subBusy, eventType)
		if err != nil {
			if d.subIDs[eventType] == subID {
				delete(d.subIDs, eventType)
			}
			delete(d.subTypes, subID)
			d.mu.Unlock()
			return err
		}
		// The last handler leaving while the command was unanswered took
		// this subscription with it, and a handler arriving after that found
		// the command in flight and sent none, so it still needs one.
		_, live := d.subIDs[eventType]
		wanted := false
		for _, s := range d.subs {
			if s.eventType == eventType {
				wanted = true
				break
			}
		}
		d.mu.Unlock()
		if live || !wanted {
			d.log.Debug("homeassistant events subscribed", zap.String("event_type", eventType))
			return nil
		}
	}
}

// resubscribe reestablishes one server subscription per registered event type
// after a (re)connect.
func (d *Driver) resubscribe(ctx context.Context) {
	d.mu.Lock()
	types := make(map[string]bool)
	for _, s := range d.subs {
		types[s.eventType] = true
	}
	d.mu.Unlock()

	for eventType := range types {
		if err := d.subscribeEventType(ctx, eventType); err != nil {
			d.log.Warn("homeassistant resubscribe failed",
				zap.String("event_type", eventType), zap.Error(err))
		}
	}
	// Registry update events during an outage are lost, so every session
	// starts from a fresh read. It comes before the state read, so area,
	// floor, device, and label triggers match the caught-up changes against
	// this server's registries rather than an empty or outdated copy.
	d.loadRegistries(ctx)
	// With state_changed live again, nothing can fall between the read and
	// the stream.
	if types["state_changed"] {
		d.syncStates(ctx)
	}
}

// dispatchEvent resolves the handlers for one received event and queues it for
// the dispatch goroutine. Called on the read loop, so it must never block.
func (d *Driver) dispatchEvent(msg wsMsg) {
	d.mu.Lock()
	eventType, ok := d.subTypes[msg.ID]
	var handlers []func(json.RawMessage)
	if ok {
		for _, s := range d.subs {
			if s.eventType == eventType {
				handlers = append(handlers, s.handler)
			}
		}
	}
	generation := d.generation
	d.mu.Unlock()
	if len(handlers) == 0 {
		return
	}

	select {
	case d.deliveries <- delivery{event: msg.Event, handlers: handlers, generation: generation}:
	default:
		d.mu.Lock()
		d.dropped++
		dropped := d.dropped
		d.mu.Unlock()
		metrics.MessagesDroppedTotal.WithLabelValues(Kind).Inc()
		// Sustained overload would flood the log, so sample after the first.
		if dropped == 1 || dropped%100 == 0 {
			d.log.Warn("homeassistant event dropped, queue full",
				zap.String("event_type", eventType), zap.Int("dropped_total", dropped))
		}
	}
}

// dispatchLoop runs queued handlers off the read loop goroutine.
func (d *Driver) dispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-d.deliveries:
			d.deliver(msg)
		}
	}
}

// deliver runs one event's handlers, isolating panics so a faulty handler
// cannot take down the connection's only dispatch goroutine.
func (d *Driver) deliver(msg delivery) {
	d.mu.Lock()
	current := d.generation
	d.mu.Unlock()
	if current != msg.generation {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("homeassistant handler panicked", zap.Any("panic", r))
		}
	}()
	if msg.run != nil {
		msg.run()
		return
	}
	for _, handler := range msg.handlers {
		handler(msg.event)
	}
}

// TestConnection performs a short-lived connect and auth handshake against
// the Home Assistant instance described by the config JSON. Used by the
// connection test API.
func TestConnection(ctx context.Context, _ uuid.UUID, cfgJSON []byte) error {
	var cfg Config
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return fmt.Errorf("invalid homeassistant config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	wsURL, err := WebsocketURL(cfg.BaseURL)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", wsURL, err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	return authenticate(ctx, conn, cfg.Token)
}

// ConnectionGeneration identifies uninterrupted event delivery. Reconnects and
// dropped events invalidate held-state intervals that may have missed changes.
// Drops are folded in here rather than into the dispatch generation so one
// overflow does not discard the events already queued for delivery.
func (d *Driver) ConnectionGeneration() (uint64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generation + uint64(d.dropped), d.conn != nil
}
