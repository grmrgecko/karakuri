package mq

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTopicGateDeniesUngatedTopics locks the direction the WebSocket topic
// gate fails in. The broker carries run events, connection status, entity
// state, and whatever any plugin publishes, and this check is the only thing
// standing between an anonymous socket and all of it.
//
// The gate used to allow a topic no registered pattern matched, which made
// every namespace anyone added later world-readable until someone remembered
// to register it. A subscriber has to be admitted by a gate, not by the
// absence of one.
func TestTopicGateDeniesUngatedTopics(t *testing.T) {
	hub := &Hub{clients: make(map[string]*Client)}
	hub.RegisterTopicPermission("run:*", "api:runs:read")
	hub.RegisterTopicPermissionCallback("control:*", func(_, topic string) bool {
		return topic == "control:public-one"
	})

	anonymous := identity{}

	tests := []struct {
		topic string
		want  bool
		why   string
	}{
		{"control:public-one", true, "a callback gate that admits this topic"},
		{"control:private-one", false, "the same gate refusing this one"},
		{"run:abc", false, "a role gate an anonymous client cannot satisfy"},
		{"weather:forecast:openmeteo:1:2", false, "a namespace no gate covers"},
		{"topic:control:public-one:count", false, "the broker's own subscription-count meta topics"},
		{"anything", false, "a bare topic nobody registered"},
	}

	for _, tc := range tests {
		assert.Equalf(t, tc.want, hub.permittedTopic(anonymous, tc.topic), "%s: %s", tc.topic, tc.why)
	}
}
