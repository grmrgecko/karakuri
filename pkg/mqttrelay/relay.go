// Package mqttrelay bridges MQTT topics onto the in-process broker so
// dashboard widgets can watch a device's state topic live.
//
// Home Assistant streams every entity's state whether anyone is looking, so its
// relay is a single subscription made at startup. MQTT is not like that: a
// subscription is per topic and costs broker traffic, so this relay follows
// demand. The broker already reports how many clients are subscribed to a
// topic, and that count is the signal — the first widget to watch
// mqtt:{connection}:{topic} causes an upstream subscribe, and the last one to
// leave causes an unsubscribe.
package mqttrelay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"go.uber.org/zap"
)

// TopicPrefix is the broker namespace a relayed MQTT message is published in.
const TopicPrefix = "mqtt"

// countPattern is the meta topic the broker announces subscription counts on
// for every relayed MQTT topic.
const countPattern = "topic:" + TopicPrefix + ":*:count"

// Resolver returns the running driver for a connection ID, or false when the
// connection is not running. connmgr.Manager.Get satisfies it.
type Resolver func(id uuid.UUID) (any, bool)

// Relay keeps one upstream MQTT subscription per watched topic.
type Relay struct {
	resolve Resolver
	log     *zap.Logger

	mu      sync.Mutex
	watched map[string]*watch
	metaSub string
	// countMu serializes count handling. The broker announces a count after
	// releasing its own lock, so announcements can arrive out of order; each
	// one reads the live count under countMu, and the last to run sees the
	// final count.
	countMu sync.Mutex
}

// watch is one relayed topic: the connection it belongs to and the handle that
// withdraws the upstream subscription.
type watch struct {
	connID      uuid.UUID
	topic       string
	unsubscribe func()
}

// New builds a relay over a connection resolver.
func New(resolve Resolver, log *zap.Logger) *Relay {
	return &Relay{resolve: resolve, log: log, watched: make(map[string]*watch)}
}

// Start begins following subscription counts. Nothing is subscribed upstream
// until a client actually watches a topic.
func (r *Relay) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.metaSub != "" {
		return
	}
	r.metaSub = mq.B.Subscribe(countPattern, func(msg *mq.Message) {
		topic, ok := countTopic(msg)
		if !ok {
			return
		}
		r.countMu.Lock()
		defer r.countMu.Unlock()
		if mq.B.GetSubscriberCount(topic) > 0 {
			r.ensure(topic)
		} else {
			r.release(topic)
		}
	})
}

// Stop withdraws every upstream subscription and stops following counts.
func (r *Relay) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.metaSub != "" {
		mq.B.Unsubscribe(r.metaSub)
		r.metaSub = ""
	}
	for key, w := range r.watched {
		if w.unsubscribe != nil {
			w.unsubscribe()
		}
		delete(r.watched, key)
	}
}

// Rebind re-establishes the upstream subscriptions of one connection, for when
// its driver is replaced: the handles the relay holds belong to the old driver
// and would leave the dashboard watching a topic nothing feeds.
func (r *Relay) Rebind(connID uuid.UUID) {
	r.mu.Lock()
	stale := make([]*watch, 0)
	var drop []func()
	for _, w := range r.watched {
		if w.connID == connID {
			stale = append(stale, w)
			if w.unsubscribe != nil {
				drop = append(drop, w.unsubscribe)
				w.unsubscribe = nil
			}
		}
	}
	r.mu.Unlock()

	for _, unsubscribe := range drop {
		unsubscribe()
	}
	for _, w := range stale {
		r.subscribe(w)
	}
}

// ensure subscribes upstream for a relayed broker topic, if it is not already.
func (r *Relay) ensure(brokerTopic string) {
	connID, topic, ok := parseTopic(brokerTopic)
	if !ok {
		return
	}

	r.mu.Lock()
	if _, exists := r.watched[brokerTopic]; exists {
		r.mu.Unlock()
		return
	}
	w := &watch{connID: connID, topic: topic}
	r.watched[brokerTopic] = w
	r.mu.Unlock()

	r.subscribe(w)
}

// subscribe attaches the driver-side handler for one watch. A connection that
// is not running is left recorded but unsubscribed: the dashboard keeps its
// place, and Rebind picks it up when the driver starts.
func (r *Relay) subscribe(w *watch) {
	driver, ok := r.resolve(w.connID)
	if !ok {
		return
	}
	d, ok := driver.(*mqtt.Driver)
	if !ok {
		return
	}

	brokerTopic := key(w.connID, w.topic)
	unsubscribe, err := d.Subscribe(w.topic, 0, func(topic string, payload []byte) {
		mq.B.Publish(brokerTopic, "mqtt_message", message(w.connID, topic, payload))
	})
	if err != nil {
		r.log.Warn("failed to relay MQTT topic",
			zap.String("connection_id", w.connID.String()),
			zap.String("topic", w.topic), zap.Error(err))
		return
	}

	r.mu.Lock()
	// The watch may have been released while the subscribe was in flight; drop
	// the fresh handle rather than leaving a subscription nobody will withdraw.
	current, still := r.watched[brokerTopic]
	if !still || current != w {
		r.mu.Unlock()
		unsubscribe()
		return
	}
	w.unsubscribe = unsubscribe
	r.mu.Unlock()

	r.log.Debug("relaying MQTT topic",
		zap.String("connection_id", w.connID.String()), zap.String("topic", w.topic))
}

// release drops the upstream subscription once nothing is watching.
func (r *Relay) release(brokerTopic string) {
	r.mu.Lock()
	w, ok := r.watched[brokerTopic]
	if ok {
		delete(r.watched, brokerTopic)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	if w.unsubscribe != nil {
		w.unsubscribe()
	}
	r.log.Debug("stopped relaying MQTT topic",
		zap.String("connection_id", w.connID.String()), zap.String("topic", w.topic))
}

// message renders one received MQTT message for the broker. The payload is
// relayed as it arrived: a row names the format it expects, and a relay that
// decoded on its behalf would be guessing at what the bytes mean.
func message(connID uuid.UUID, topic string, payload []byte) map[string]any {
	out := map[string]any{
		"connection_id": connID.String(),
		"topic":         topic,
	}
	if utf8.Valid(payload) {
		out["payload"] = string(payload)
		out["binary"] = false
		return out
	}
	// Bytes that are not text cannot ride in JSON as text, so they are base64
	// and the flag says so rather than delivering mojibake.
	out["payload"] = base64.StdEncoding.EncodeToString(payload)
	out["binary"] = true
	return out
}

// key builds the broker topic one connection's MQTT topic is relayed on.
func key(connID uuid.UUID, topic string) string {
	return fmt.Sprintf("%s:%s:%s", TopicPrefix, connID, topic)
}

// parseTopic splits a relayed broker topic back into its connection and MQTT
// topic. Only the first two segments are ours: an MQTT topic may itself contain
// a colon, so the remainder is taken whole.
func parseTopic(brokerTopic string) (uuid.UUID, string, bool) {
	parts := strings.SplitN(brokerTopic, ":", 3)
	if len(parts) != 3 || parts[0] != TopicPrefix {
		return uuid.Nil, "", false
	}
	connID, err := uuid.Parse(parts[1])
	if err != nil || parts[2] == "" {
		return uuid.Nil, "", false
	}
	return connID, parts[2], true
}

// countTopic reads the topic a subscription-count announcement is for. The
// count it carries may already be stale, so the relay does not use it.
func countTopic(msg *mq.Message) (string, bool) {
	if msg == nil || msg.Type != "subscription_count" {
		return "", false
	}
	var body struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal(msg.Payload, &body); err != nil || body.Topic == "" {
		return "", false
	}
	return body.Topic, true
}
