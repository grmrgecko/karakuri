// Package mqtt implements the MQTT connection driver on top of
// paho.golang/autopaho, which supervises reconnects internally. A single
// broker session is shared by all nodes using the connection. The driver keeps
// a filter->handler registry and reconciles the broker's subscriptions against
// it after every connect, subscribe, and unsubscribe, retrying until the two
// agree.
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"go.uber.org/zap"
)

// Kind is the driver kind registered with the integrations registry.
const Kind = "mqtt"

// publishTimeout bounds how long a publish waits for the session, so node
// executions fail (and can retry) instead of hanging while the broker is down.
const publishTimeout = 15 * time.Second

// subscribeTimeout bounds subscribe/unsubscribe round trips.
const subscribeTimeout = 10 * time.Second

// deliveryQueueSize bounds the per-connection delivery backlog. Handlers run
// on the driver's own goroutine rather than paho's reader: that reader also
// processes PINGRESP, so a slow flow blocking it would take the session down.
const deliveryQueueSize = 1024

// Subscription reconciliation retry bounds, used when the broker rejects or
// times out a subscribe/unsubscribe.
const (
	reconcileMinBackoff = time.Second
	reconcileMaxBackoff = 30 * time.Second
)

// Config is the driver configuration stored (encrypted) on a connection.
type Config struct {
	BrokerURL  string `json:"broker_url"`
	ClientID   string `json:"client_id,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	KeepAliveS int    `json:"keep_alive_s,omitempty"`
	CleanStart bool   `json:"clean_start,omitempty"`
}

// applyDefaults fills unset fields with usable defaults.
func (c *Config) applyDefaults(id uuid.UUID) {
	if c.ClientID == "" {
		c.ClientID = "karakuri-" + id.String()[:8]
	}
	if c.KeepAliveS <= 0 {
		c.KeepAliveS = 30
	}
}

// Message is one received message as a handler sees it.
type Message struct {
	Topic   string
	Payload []byte
	// Retained is true when the broker delivered the message from its
	// retained store because a subscription was made, and false for a message
	// published while subscribed. The driver speaks MQTT 5 without
	// retain-as-published, so the broker clears the flag on live messages.
	Retained bool
	// QoS is the QoS the message was delivered at.
	QoS byte
}

// subscription is one registered handler for a topic filter.
type subscription struct {
	filter  string
	qos     byte
	handler func(Message)
	// fresh marks a handler no SUBSCRIBE has covered since it registered. A
	// broker replays retained messages only when a SUBSCRIBE arrives, so a
	// handler joining a filter the session already holds needs one sent.
	fresh bool
	// seen are the topics this handler was handed a message on during the
	// current session, retained or live. A replay asked for by another
	// handler, on this filter or one overlapping it, is not handed over again:
	// the retained store holds nothing newer than what the handler has read.
	seen map[string]struct{}
}

// delivery is one received message queued for handler dispatch, with the
// handlers resolved at receive time so a later unsubscribe cannot redirect it.
type delivery struct {
	msg      Message
	handlers []func(Message)
}

// maxSeenLive caps the topics one handler records from live messages. A
// wildcard filter over topics that never repeat, such as one topic per
// request, would otherwise grow the record for as long as the session lasts.
// Past the cap a live topic goes unrecorded, and the handler may be handed
// its retained copy once when another handler joins.
const maxSeenLive = 4096

// markSeen reports whether topic is new to the handler, recording it when it
// is. The caller holds the driver lock.
func (s *subscription) markSeen(topic string) bool {
	if _, seen := s.seen[topic]; seen {
		return false
	}
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	s.seen[topic] = struct{}{}
	return true
}

// Driver is a running MQTT connection shared by all nodes referencing it.
type Driver struct {
	id  uuid.UUID
	cfg Config
	log *zap.Logger

	publishStatus func(integrations.Status)

	mu        sync.Mutex
	status    integrations.Status
	subs      map[int]*subscription
	nextSubID int
	// brokerSubs are filters believed to exist on the broker. Entries outlive
	// reconnects so a subscription leaked by a failed unsubscribe is retried
	// instead of silently feeding the session forever.
	brokerSubs map[string]bool
	// sessionSubs are filters confirmed on the current session, keyed to the
	// QoS granted. Cleared on every connect so a fresh or resumed session is
	// always reestablished.
	sessionSubs map[string]byte
	// grantedSubs are the QoS the broker granted per filter on the current
	// session, which may be lower than the QoS asked for.
	grantedSubs map[string]byte
	// lastErr is the most recent client error, reported as the status message
	// when the connection drops.
	lastErr    string
	reconnects int
	dropped    int
	cm         *autopaho.ConnectionManager
	ctx        context.Context
	cancel     context.CancelFunc

	deliveries chan delivery
	reconcile  chan struct{}
}

// New builds an MQTT driver from a connection model. Registered as the
// factory for the "mqtt" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid mqtt config: %w", err)
	}
	if cfg.BrokerURL == "" {
		return nil, fmt.Errorf("mqtt config has no broker_url")
	}
	cfg.applyDefaults(model.ID)

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:            model.ID,
		cfg:           cfg,
		log:           log,
		publishStatus: deps.PublishStatus,
		status:        integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		subs:          make(map[int]*subscription),
		brokerSubs:    make(map[string]bool),
		sessionSubs:   make(map[string]byte),
		grantedSubs:   make(map[string]byte),
		deliveries:    make(chan delivery, deliveryQueueSize),
		reconcile:     make(chan struct{}, 1),
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

	d.log.Info("mqtt connection status", zap.String("state", state), zap.String("error", errMsg))
	if publish != nil {
		publish(status)
	}
}

// Start builds the autopaho session and begins connecting. Reconnect
// supervision is handled by autopaho until Stop.
func (d *Driver) Start(ctx context.Context) error {
	serverURL, err := url.Parse(d.cfg.BrokerURL)
	if err != nil {
		return fmt.Errorf("invalid broker URL %q: %w", d.cfg.BrokerURL, err)
	}

	runCtx, cancel := context.WithCancel(ctx)

	pahoCfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     uint16(d.cfg.KeepAliveS),
		CleanStartOnInitialConnection: d.cfg.CleanStart,
		ConnectUsername:               d.cfg.Username,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			d.onConnectionUp(cm)
		},
		OnConnectionDown: func() bool {
			d.onConnectionDown()
			// Keep reconnecting; Stop cancels the context to end the session.
			return true
		},
		OnConnectError: func(err error) {
			d.setStatus(integrations.StateError, err.Error())
		},
		ClientConfig: paho.ClientConfig{
			ClientID: d.cfg.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					d.dispatch(Message{Topic: pr.Packet.Topic, Payload: pr.Packet.Payload,
						Retained: pr.Packet.Retain, QoS: pr.Packet.QoS})
					return true, nil
				},
			},
			OnClientError: func(err error) {
				d.recordError(err.Error())
			},
			OnServerDisconnect: func(disc *paho.Disconnect) {
				reason := "server initiated disconnect"
				if disc != nil && disc.Properties != nil && disc.Properties.ReasonString != "" {
					reason = disc.Properties.ReasonString
				}
				d.recordError(reason)
			},
		},
	}
	if d.cfg.Password != "" {
		pahoCfg.ConnectPassword = []byte(d.cfg.Password)
	}
	if !d.cfg.CleanStart {
		// Keep the session alive across short disconnects so QoS>0 messages
		// published while offline are delivered on reconnect.
		pahoCfg.SessionExpiryInterval = 3600
	}

	cm, err := autopaho.NewConnection(runCtx, pahoCfg)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to start mqtt session: %w", err)
	}

	d.mu.Lock()
	if d.cm == nil {
		d.cm = cm
	}
	d.ctx = runCtx
	d.cancel = cancel
	d.mu.Unlock()

	go d.dispatchLoop(runCtx)
	go d.reconcileLoop(runCtx)

	d.setStatus(integrations.StateConnecting, "")
	return nil
}

// Stop tears the session down and marks the connection disabled.
func (d *Driver) Stop() {
	d.mu.Lock()
	cm := d.cm
	cancel := d.cancel
	d.cm = nil
	d.mu.Unlock()

	if cm != nil {
		ctx, cancelTimeout := context.WithTimeout(context.Background(), 2*time.Second)
		_ = cm.Disconnect(ctx)
		cancelTimeout()
	}
	if cancel != nil {
		cancel()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// onConnectionUp records a (re)established session and queues a subscription
// reconcile. Called by autopaho and must not block.
func (d *Driver) onConnectionUp(cm *autopaho.ConnectionManager) {
	d.mu.Lock()
	d.cm = cm
	// Nothing is confirmed on a brand new session, so every desired filter is
	// reestablished below even when the broker resumed the old one.
	d.sessionSubs = make(map[string]byte)
	d.grantedSubs = make(map[string]byte)
	// Every filter is subscribed again, and each handler reads the retained
	// messages of the new session.
	for _, s := range d.subs {
		s.seen = nil
	}
	d.lastErr = ""
	d.reconnects++
	reconnects := d.reconnects
	d.mu.Unlock()

	// The first connect is the normal lifecycle event; later ones mean the
	// link dropped and recovered, which is worth counting on its own.
	if reconnects > 1 {
		metrics.ConnectionReconnectsTotal.WithLabelValues(Kind).Inc()
		d.log.Warn("mqtt session reestablished", zap.Int("reconnects", reconnects-1))
	}
	d.setStatus(integrations.StateConnected, "")
	d.signalReconcile()
}

// onConnectionDown reports a dropped session so the connection stops claiming
// to be healthy while autopaho reconnects. Called by autopaho and must not
// block.
func (d *Driver) onConnectionDown() {
	d.mu.Lock()
	reason := d.lastErr
	d.sessionSubs = make(map[string]byte)
	d.grantedSubs = make(map[string]byte)
	d.mu.Unlock()

	if reason == "" {
		reason = "connection lost"
	}
	d.setStatus(integrations.StateError, reason)
}

// recordError remembers the most recent client-level failure so the next
// connection-down transition can report why.
func (d *Driver) recordError(msg string) {
	d.mu.Lock()
	d.lastErr = msg
	d.mu.Unlock()
	d.log.Warn("mqtt client error", zap.String("error", msg))
}

// Publish sends one message, waiting for the session to be available up to
// the publish timeout.
func (d *Driver) Publish(ctx context.Context, topic string, qos byte, retain bool, payload []byte) error {
	// A wildcard in a publish topic is a protocol violation, and an MQTT 5
	// broker answers it by disconnecting the session, taking every
	// subscription on this connection with it. Topics are often templated from
	// a message, so the check sits where every publish path meets.
	if err := validPublishTopic(topic); err != nil {
		return err
	}
	d.mu.Lock()
	cm := d.cm
	d.mu.Unlock()
	if cm == nil {
		return fmt.Errorf("mqtt connection is not running")
	}

	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := cm.AwaitConnection(ctx); err != nil {
		return fmt.Errorf("mqtt connection not available: %w", err)
	}
	_, err := cm.Publish(ctx, &paho.Publish{
		Topic:   topic,
		QoS:     qos,
		Retain:  retain,
		Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("mqtt publish to %q failed: %w", topic, err)
	}
	return nil
}

// GrantedQoS returns the QoS the broker granted a filter on the current
// session; ok is false while the filter is not subscribed.
func (d *Driver) GrantedQoS(filter string) (byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	q, ok := d.grantedSubs[filter]
	return q, ok
}

// Subscribed reports whether a filter is live on the broker's current
// session. It is false from a disconnect until the reconnect has subscribed
// again. Subscribing is asynchronous, applied by the reconcile loop after
// Subscribe returns, so a caller that must not miss the first message on a
// topic can wait for this rather than publishing into a subscription that
// does not exist yet.
func (d *Driver) Subscribed(filter string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, live := d.sessionSubs[filter]
	return live
}

// Subscribe registers a handler for a topic filter and returns an unsubscribe
// function. Multiple handlers share the session; the broker subscription for a
// filter is dropped once its last handler unsubscribes. Both directions are
// applied by the reconcile loop, which retries until the broker agrees.
func (d *Driver) Subscribe(filter string, qos byte, handler func(topic string, payload []byte)) (func(), error) {
	if handler == nil {
		return nil, fmt.Errorf("nil message handler")
	}
	return d.SubscribeMessages(filter, qos, func(m Message) { handler(m.Topic, m.Payload) })
}

// SubscribeMessages is Subscribe with the whole message, including whether
// the broker delivered it from its retained store.
func (d *Driver) SubscribeMessages(filter string, qos byte, handler func(Message)) (func(), error) {
	// A registered filter is sent on every reconcile and after every
	// reconnect, so a malformed one has to be stopped here: a broker that
	// answers it by closing the session would close it again each time.
	if err := validFilter(filter); err != nil {
		return nil, err
	}

	d.mu.Lock()
	id := d.nextSubID
	d.nextSubID++
	d.subs[id] = &subscription{filter: filter, qos: qos, handler: handler, fresh: true}
	d.mu.Unlock()
	d.signalReconcile()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.subs, id)
			d.mu.Unlock()
			d.signalReconcile()
		})
	}, nil
}

// signalReconcile wakes the reconcile loop without blocking; a pending signal
// already covers the new work.
func (d *Driver) signalReconcile() {
	select {
	case d.reconcile <- struct{}{}:
	default:
	}
}

// reconcileLoop applies the registered filters to the broker, retrying with
// capped exponential backoff until the two agree.
func (d *Driver) reconcileLoop(ctx context.Context) {
	backoff := reconcileMinBackoff
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.reconcile:
		case <-retry.C:
		}

		if d.reconcileOnce(ctx) {
			backoff = reconcileMinBackoff
			continue
		}
		retry.Reset(backoff)
		if backoff *= 2; backoff > reconcileMaxBackoff {
			backoff = reconcileMaxBackoff
		}
	}
}

// reconcileOnce issues the subscribes and unsubscribes needed to match the
// registered filters. It reports whether the broker is now in sync.
func (d *Driver) reconcileOnce(ctx context.Context) bool {
	// Determine the difference between what nodes want and what the broker has.
	d.mu.Lock()
	cm := d.cm
	connected := d.status.State == integrations.StateConnected
	if cm == nil || !connected {
		d.mu.Unlock()
		// Nothing can be applied while the session is down; the next connect
		// signals another pass.
		return true
	}
	desired := make(map[string]byte, len(d.subs))
	// joined are the handlers this pass's SUBSCRIBE covers for the first time,
	// by filter.
	joined := make(map[string][]*subscription)
	for _, s := range d.subs {
		if q, ok := desired[s.filter]; !ok || s.qos > q {
			desired[s.filter] = s.qos
		}
		if s.fresh {
			joined[s.filter] = append(joined[s.filter], s)
		}
	}
	var subscribe []paho.SubscribeOptions
	for filter, qos := range desired {
		// A held filter is sent again for a handler that joined it, which is
		// what makes the broker replay its retained messages.
		if granted, ok := d.sessionSubs[filter]; !ok || granted != qos || len(joined[filter]) > 0 {
			subscribe = append(subscribe, paho.SubscribeOptions{Topic: filter, QoS: qos})
		}
	}
	var unsubscribe []string
	for filter := range d.brokerSubs {
		if _, ok := desired[filter]; !ok {
			unsubscribe = append(unsubscribe, filter)
		}
	}
	d.mu.Unlock()

	if len(subscribe) == 0 && len(unsubscribe) == 0 {
		return true
	}

	// Apply both directions, recording only what the broker confirmed so a
	// failure is retried rather than assumed.
	synced := true
	if len(subscribe) > 0 {
		sctx, cancel := context.WithTimeout(ctx, subscribeTimeout)
		suback, err := cm.Subscribe(sctx, &paho.Subscribe{Subscriptions: subscribe})
		cancel()
		// paho reports the batch as failed when any one filter was refused, but
		// the broker holds the others. Record each accepted filter by its own
		// reason code, or one filter the broker's ACL refuses would keep the
		// whole batch being re-sent every interval and leave the accepted ones
		// unremovable.
		accepted := 0
		d.mu.Lock()
		for i, opt := range subscribe {
			refused := err != nil
			if suback != nil && len(suback.Reasons) == len(subscribe) {
				refused = suback.Reasons[i] >= 0x80
			}
			if refused {
				continue
			}
			d.sessionSubs[opt.Topic] = opt.QoS
			d.grantedSubs[opt.Topic] = opt.QoS
			if suback != nil && len(suback.Reasons) == len(subscribe) {
				// A SUBACK reason below 0x80 is the QoS granted.
				d.grantedSubs[opt.Topic] = suback.Reasons[i]
			}
			d.brokerSubs[opt.Topic] = true
			for _, s := range joined[opt.Topic] {
				s.fresh = false
			}
			accepted++
		}
		d.mu.Unlock()
		if err != nil {
			d.log.Warn("mqtt subscribe refused, retrying the rest",
				zap.Int("filters", len(subscribe)), zap.Int("accepted", accepted), zap.Error(err))
			synced = false
		} else {
			d.log.Debug("mqtt subscriptions applied", zap.Int("filters", accepted))
		}
	}
	if len(unsubscribe) > 0 {
		uctx, cancel := context.WithTimeout(ctx, subscribeTimeout)
		_, err := cm.Unsubscribe(uctx, &paho.Unsubscribe{Topics: unsubscribe})
		cancel()
		if err != nil {
			d.log.Warn("mqtt unsubscribe failed, retrying",
				zap.Strings("filters", unsubscribe), zap.Error(err))
			synced = false
		} else {
			d.mu.Lock()
			for _, filter := range unsubscribe {
				delete(d.brokerSubs, filter)
				delete(d.sessionSubs, filter)
				delete(d.grantedSubs, filter)
			}
			d.mu.Unlock()
			d.log.Debug("mqtt subscriptions removed", zap.Strings("filters", unsubscribe))
		}
	}
	return synced
}

// dispatch resolves the handlers for a received message and queues it for the
// dispatch goroutine. Called on paho's reader, so it must never block.
func (d *Driver) dispatch(msg Message) {
	topic := msg.Topic
	d.mu.Lock()
	var handlers []func(Message)
	var targets []*subscription
	for _, s := range d.subs {
		if !Match(s.filter, topic) {
			continue
		}
		// The broker marks a message retained only when it replays its store
		// for a SUBSCRIBE, and a SUBSCRIBE sent for one handler replays to
		// every handler whose filter matches. A handler that read the topic
		// live already holds what the store would replay.
		switch {
		case msg.Retained:
			if !s.markSeen(topic) {
				continue
			}
		case len(s.seen) < maxSeenLive:
			s.markSeen(topic)
		}
		handlers = append(handlers, s.handler)
		targets = append(targets, s)
	}
	d.mu.Unlock()
	if len(handlers) == 0 {
		return
	}

	// The payload is owned by the packet being read, so copy it for the queue.
	select {
	case d.deliveries <- delivery{msg: Message{Topic: topic, Payload: slices.Clone(msg.Payload),
		Retained: msg.Retained, QoS: msg.QoS}, handlers: handlers}:
	default:
		d.mu.Lock()
		d.dropped++
		dropped := d.dropped
		// A dropped message was not read, so a later replay is still new,
		// including to a handler that read an older value on the topic.
		for _, s := range targets {
			delete(s.seen, topic)
		}
		d.mu.Unlock()
		metrics.MessagesDroppedTotal.WithLabelValues(Kind).Inc()
		// Sustained overload would flood the log, so sample after the first.
		if dropped == 1 || dropped%100 == 0 {
			d.log.Warn("mqtt delivery dropped, queue full",
				zap.String("topic", topic), zap.Int("dropped_total", dropped))
		}
	}
}

// dispatchLoop runs queued handlers off paho's reader goroutine.
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

// deliver runs one message's handlers, isolating panics so a faulty handler
// cannot take down the connection's only dispatch goroutine.
func (d *Driver) deliver(msg delivery) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("mqtt handler panicked",
				zap.String("topic", msg.msg.Topic), zap.Any("panic", r))
		}
	}()
	for _, handler := range msg.handlers {
		handler(msg.msg)
	}
}

// TestConnection attempts a short-lived connect/disconnect against the broker
// described by the config JSON. Used by the connection test API.
func TestConnection(ctx context.Context, id uuid.UUID, cfgJSON []byte) error {
	var cfg Config
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return fmt.Errorf("invalid mqtt config: %w", err)
	}
	if cfg.BrokerURL == "" {
		return fmt.Errorf("mqtt config has no broker_url")
	}
	cfg.applyDefaults(id)
	cfg.ClientID += "-test"

	serverURL, err := url.Parse(cfg.BrokerURL)
	if err != nil {
		return fmt.Errorf("invalid broker URL %q: %w", cfg.BrokerURL, err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Capture the last connect error so timeouts report the real cause.
	var mu sync.Mutex
	var lastErr error
	pahoCfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     uint16(cfg.KeepAliveS),
		CleanStartOnInitialConnection: true,
		ConnectUsername:               cfg.Username,
		OnConnectError: func(err error) {
			mu.Lock()
			lastErr = err
			mu.Unlock()
		},
		ClientConfig: paho.ClientConfig{ClientID: cfg.ClientID},
	}
	if cfg.Password != "" {
		pahoCfg.ConnectPassword = []byte(cfg.Password)
	}

	cm, err := autopaho.NewConnection(ctx, pahoCfg)
	if err != nil {
		return err
	}
	defer func() {
		dctx, dcancel := context.WithTimeout(context.Background(), time.Second)
		_ = cm.Disconnect(dctx)
		dcancel()
	}()

	if err := cm.AwaitConnection(ctx); err != nil {
		mu.Lock()
		defer mu.Unlock()
		if lastErr != nil {
			return lastErr
		}
		return fmt.Errorf("connection timed out")
	}
	return nil
}

// validPublishTopic rejects a topic name the broker would refuse the session
// for: empty, not UTF-8, carrying a wildcard or NUL, or longer than the
// protocol allows.
func validPublishTopic(topic string) error {
	switch {
	case topic == "":
		return fmt.Errorf("mqtt publish topic is empty")
	case !utf8.ValidString(topic):
		return fmt.Errorf("mqtt publish topic %q is not valid UTF-8 text", topic)
	case strings.ContainsAny(topic, "+#\x00"):
		return fmt.Errorf("mqtt publish topic %q contains a wildcard or NUL", topic)
	case len(topic) > 65535:
		return fmt.Errorf("mqtt publish topic is longer than 65535 bytes")
	}
	return nil
}

// validFilter rejects a topic filter the broker would refuse or the driver
// could not deliver: empty, too long, not UTF-8, carrying NUL, with a wildcard
// that does not fill its whole level or a '#' before the last level, or a
// shared subscription, whose messages arrive on topics the filter itself does
// not match.
func validFilter(filter string) error {
	switch {
	case filter == "":
		return fmt.Errorf("empty topic filter")
	case len(filter) > 65535:
		return fmt.Errorf("mqtt topic filter is longer than 65535 bytes")
	case !utf8.ValidString(filter) || strings.ContainsRune(filter, 0):
		return fmt.Errorf("mqtt topic filter %q is not valid UTF-8 text", filter)
	case strings.HasPrefix(filter, "$share/"):
		return fmt.Errorf("mqtt topic filter %q is a shared subscription, which is not supported", filter)
	}
	levels := strings.Split(filter, "/")
	for i, level := range levels {
		if !strings.ContainsAny(level, "+#") {
			continue
		}
		if level != "+" && level != "#" {
			return fmt.Errorf("mqtt topic filter %q has a wildcard that is not a whole level", filter)
		}
		if level == "#" && i != len(levels)-1 {
			return fmt.Errorf("mqtt topic filter %q has # before its last level", filter)
		}
	}
	return nil
}
