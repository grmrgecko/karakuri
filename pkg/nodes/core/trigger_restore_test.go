package core

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// triggerRestoreGraph wires start → trigger → expired sink, with a second
// manual node feeding the trigger's reset input.
func triggerRestoreGraph(t *testing.T, cfg map[string]any) *flow.Graph {
	return &flow.Graph{Nodes: []flow.Node{
		{ID: "start", Type: "manual"},
		{ID: "reset", Type: "manual"},
		{ID: "trig", Type: "trigger", Config: cfgJSON(t, cfg)},
		{ID: "expired", Type: "test-sink"},
	}, Edges: []flow.Edge{
		{ID: "e1", Source: "start", SourceHandle: "out", Target: "trig", TargetHandle: "in"},
		{ID: "e2", Source: "reset", SourceHandle: "out", Target: "trig", TargetHandle: "reset"},
		{ID: "e3", Source: "trig", SourceHandle: "expired", Target: "expired", TargetHandle: "in"},
	}}
}

// TestTriggerRestoresAcrossRestart covers US1 for the Trigger node: an armed
// wait interrupted by a restart fires at its original deadline, a reset or an
// extension after the restart still works, and keys stay independent.
func TestTriggerRestoresAcrossRestart(t *testing.T) {
	base := map[string]any{"first_source": "nothing", "duration": "2m", "second_source": "original"}

	t.Run("fires at the original deadline", func(t *testing.T) {
		h := newDurableHarness(t, triggerRestoreGraph(t, base))
		h.start()
		h.send("start", &engine.Msg{Payload: "guest", Topic: "house/mode"})
		h.parked(1)
		h.restart(30 * time.Second)
		h.parked(2)
		h.clock.Advance(89 * time.Second)
		assert.Empty(t, h.sinks["expired"].received(), "nothing fires before the original deadline")
		h.clock.Advance(time.Second)
		require.Eventually(t, func() bool { return len(h.sinks["expired"].received()) == 1 }, 3*time.Second, time.Millisecond,
			"the delayed message fires once at the original deadline")
		got := h.sinks["expired"].received()[0]
		assert.Equal(t, "guest", got.Payload, "the message armed before the restart is the one sent")
		assert.Equal(t, "house/mode", got.Topic)
		h.eventuallyStored(0)
	})

	t.Run("a reset after the restart cancels it", func(t *testing.T) {
		h := newDurableHarness(t, triggerRestoreGraph(t, base))
		h.start()
		h.send("start", &engine.Msg{Payload: "guest"})
		h.parked(1)
		h.restart(30 * time.Second)
		h.parked(2)
		h.send("reset", &engine.Msg{})
		h.eventuallyStored(0)
		h.clock.Advance(5 * time.Minute)
		h.settled()
		assert.Empty(t, h.sinks["expired"].received(), "a reset wait never fires")
	})

	t.Run("an extension after the restart moves the deadline", func(t *testing.T) {
		h := newDurableHarness(t, triggerRestoreGraph(t, base))
		h.start()
		h.send("start", &engine.Msg{Payload: "first"})
		h.parked(1)
		h.restart(30 * time.Second)
		h.parked(2)
		h.send("start", &engine.Msg{Payload: "second"})
		require.Eventually(t, func() bool {
			waits := h.eng.PendingWaits(h.flow.ID)
			return len(waits) == 1 && waits[0].Deadline.Equal(h.clock.Now().Add(2*time.Minute))
		}, 3*time.Second, time.Millisecond, "the new message pushes the restored deadline out")
		h.clock.Advance(90 * time.Second)
		h.parked(3)
		assert.Empty(t, h.sinks["expired"].received(), "the original deadline no longer fires")
		h.clock.Advance(30 * time.Second)
		require.Eventually(t, func() bool { return len(h.sinks["expired"].received()) == 1 }, 3*time.Second, time.Millisecond)
		assert.Equal(t, "first", h.sinks["expired"].received()[0].Payload,
			"an extension keeps the armed message, as it does without a restart")
	})

	t.Run("keys stay independent", func(t *testing.T) {
		cfg := map[string]any{"first_source": "nothing", "duration": "2m", "second_source": "original", "key": "topic"}
		h := newDurableHarness(t, triggerRestoreGraph(t, cfg))
		h.start()
		for i, topic := range []string{"kitchen", "hall", "porch"} {
			h.send("start", &engine.Msg{Payload: topic, Topic: topic})
			h.parked(i + 1)
		}
		h.restart(30 * time.Second)
		h.parked(6)
		h.send("reset", &engine.Msg{Topic: "hall"})
		h.eventuallyStored(2)
		h.clock.Advance(2 * time.Minute)
		require.Eventually(t, func() bool { return len(h.sinks["expired"].received()) == 2 }, 3*time.Second, time.Millisecond)
		h.settled()
		var topics []string
		for _, m := range h.sinks["expired"].received() {
			topics = append(topics, m.Topic)
		}
		assert.ElementsMatch(t, []string{"kitchen", "porch"}, topics, "only the reset key is cancelled")
	})
}
