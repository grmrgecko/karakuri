package mq

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Message is the unit of communication delivered to subscribers.
type Message struct {
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	// Dashboard is inbound only: a subscribing client naming the dashboard it
	// is displaying, so a topic it holds no permission for can still be
	// checked against what that dashboard exposes. It is never published.
	Dashboard string `json:"dashboard,omitempty"`
}

// Handler is a callback invoked when a message matches a subscription pattern.
type Handler func(msg *Message)

// Subscription binds a pattern to a handler for message delivery.
type Subscription struct {
	ID      string
	Pattern string
	Handler Handler
}

// Broker is an in-process pub/sub message broker with topic routing,
// wildcard subscriptions, and automatic subscription count tracking.
type Broker struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription // id -> subscription
	topicCounts   map[string]int           // exact topic -> tracked subscription count
}

// B is the package-level broker for in-process pub/sub messaging.
var B = &Broker{
	subscriptions: make(map[string]*Subscription),
	topicCounts:   make(map[string]int),
}

// Publish serializes the payload and delivers the message to all subscriptions
// whose pattern matches the given topic.
func (b *Broker) Publish(topic, msgType string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	msg := &Message{
		Topic:   topic,
		Type:    msgType,
		Payload: json.RawMessage(data),
	}

	b.mu.RLock()
	subs := make([]*Subscription, 0, len(b.subscriptions))
	for _, sub := range b.subscriptions {
		if Match(sub.Pattern, topic) {
			subs = append(subs, sub)
		}
	}
	b.mu.RUnlock()

	for _, sub := range subs {
		sub.Handler(msg)
	}
}

// Subscribe registers a handler for the given pattern and returns the
// subscription ID. For exact topic subscriptions (no wildcard, no "topic:"
// prefix), the broker tracks the subscription count and auto-publishes count
// changes. Use Unsubscribe with the returned ID to remove the subscription.
func (b *Broker) Subscribe(pattern string, handler Handler) string {
	id := uuid.New().String()
	sub := &Subscription{
		ID:      id,
		Pattern: pattern,
		Handler: handler,
	}

	b.mu.Lock()
	b.subscriptions[id] = sub
	if isTrackedPattern(pattern) {
		b.topicCounts[pattern]++
		count := b.topicCounts[pattern]
		b.mu.Unlock()
		b.publishCount(pattern, count)
	} else {
		b.mu.Unlock()
	}

	return id
}

// Unsubscribe removes the subscription with the given ID. If the subscription
// was for a tracked pattern, the broker decrements and publishes the updated
// subscription count.
func (b *Broker) Unsubscribe(id string) {
	b.mu.Lock()
	sub, exists := b.subscriptions[id]
	if !exists {
		b.mu.Unlock()
		return
	}
	pattern := sub.Pattern
	delete(b.subscriptions, id)
	if isTrackedPattern(pattern) {
		b.topicCounts[pattern]--
		count := b.topicCounts[pattern]
		if count <= 0 {
			delete(b.topicCounts, pattern)
			count = 0
		}
		b.mu.Unlock()
		b.publishCount(pattern, count)
	} else {
		b.mu.Unlock()
	}
}

// GetSubscriberCount returns the number of tracked subscriptions for an exact topic.
func (b *Broker) GetSubscriberCount(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.topicCounts[topic]
}

// publishCount sends a subscription_count message to the meta topic for the given pattern.
func (b *Broker) publishCount(pattern string, count int) {
	metaTopic := "topic:" + pattern + ":count"
	payload := map[string]interface{}{
		"topic": pattern,
		"count": count,
	}
	b.Publish(metaTopic, "subscription_count", payload)
}

// isTrackedPattern returns true if the pattern should trigger automatic count tracking.
// Patterns containing wildcards or starting with "topic:" are excluded.
func isTrackedPattern(pattern string) bool {
	if strings.Contains(pattern, "*") {
		return false
	}
	if strings.HasPrefix(pattern, "topic:") {
		return false
	}
	return true
}
