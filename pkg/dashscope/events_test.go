package dashscope

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEventSubjectTopics covers FR-009's live half: a dashboard may follow the
// event log entries of the subjects its widgets are bound to and no others,
// and never the log's own topics.
func TestEventSubjectTopics(t *testing.T) {
	scope := newScope()
	scope.add(Capability{Kind: CapControl, Ref: "c1"})
	scope.add(Capability{Kind: CapDevice, Ref: "d1"})
	scope.add(Capability{Kind: CapHA, Owner: "ha", Ref: "binary_sensor.door"})

	tests := []struct {
		topic string
		want  bool
	}{
		{"events:subject:control:c1", true},
		{"events:subject:control:c2", false},
		{"events:subject:device:d1", true},
		{"events:subject:ha_entity:ha/binary_sensor.door", true},
		{"events:subject:ha_entity:ha/light.kitchen", false},
		{"events:subject:connection:x", false},
		{"events", false},
		{"events:security", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, scope.AllowsTopic(tc.topic), tc.topic)
	}
}
