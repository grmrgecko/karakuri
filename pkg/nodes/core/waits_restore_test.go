package core

import (
	"slices"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chainGraph wires start → node → out sink, plus any extra edges from the
// node to other sinks named by port.
func chainGraph(t *testing.T, typ string, cfg map[string]any, outPorts ...string) *flow.Graph {
	if len(outPorts) == 0 {
		outPorts = []string{"out"}
	}
	g := &flow.Graph{Nodes: []flow.Node{
		{ID: "start", Type: "manual"},
		{ID: "node", Type: typ, Config: cfgJSON(t, cfg)},
	}, Edges: []flow.Edge{{ID: "in", Source: "start", SourceHandle: "out", Target: "node", TargetHandle: "in"}}}
	for _, port := range outPorts {
		g.Nodes = append(g.Nodes, flow.Node{ID: port, Type: "test-sink"})
		g.Edges = append(g.Edges, flow.Edge{ID: port, Source: "node", SourceHandle: port, Target: port, TargetHandle: "in"})
	}
	return g
}

// eventuallyReceived waits until the named sink has n messages.
func (h *durableHarness) eventuallyReceived(sink string, n int) []*engine.Msg {
	h.t.Helper()
	require.Eventually(h.t, func() bool { return len(h.sinks[sink].received()) == n }, 3*time.Second, time.Millisecond,
		"sink %s must receive %d messages", sink, n)
	return h.sinks[sink].received()
}

// TestDelayHoldMissedDeadlines covers US2 for a fixed delay: a deadline that
// passed while the server was down is sent late by default, dropped when the
// node says so, and sent as on time within the grace period. A binary
// payload comes back as bytes.
func TestDelayHoldMissedDeadlines(t *testing.T) {
	cases := []struct {
		name     string
		cfg      map[string]any
		downtime time.Duration
		sent     bool
	}{
		{"late by default", map[string]any{"mode": "fixed", "duration": "30s"}, 10 * time.Minute, true},
		{"dropped when configured", map[string]any{"mode": "fixed", "duration": "30s", "missed_deadline": "drop", "missed_grace": "5s"}, 10 * time.Minute, false},
		{"within grace", map[string]any{"mode": "fixed", "duration": "30s", "missed_deadline": "drop", "missed_grace": "5m"}, time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDurableHarness(t, chainGraph(t, "delay", tc.cfg))
			h.start()
			h.send("start", &engine.Msg{Payload: []byte{0x01, 0xff}})
			h.parked(1)
			h.restart(tc.downtime)
			if tc.sent {
				got := h.eventuallyReceived("out", 1)[0]
				assert.Equal(t, []byte{0x01, 0xff}, got.Payload, "a binary payload is restored as bytes")
				resumed, _ := got.Meta["resumed"].(map[string]any)
				assert.NotNil(t, resumed, "a restored delivery says so")
			} else {
				h.settled()
				assert.Empty(t, h.sinks["out"].received(), "a dropped deadline sends nothing")
			}
			h.eventuallyStored(0)
		})
	}
}

// TestDebounceRestoresWindow covers a debounce window across a restart: the
// latest message is the one sent, and the maximum wait keeps its original
// start.
func TestDebounceRestoresWindow(t *testing.T) {
	h := newDurableHarness(t, chainGraph(t, "delay",
		map[string]any{"mode": "debounce", "quiet": "1m", "max_wait": "90s"}, "out", "superseded"))
	h.start()
	h.send("start", &engine.Msg{Payload: "first"})
	h.parked(1)
	h.clock.Advance(50 * time.Second)
	h.send("start", &engine.Msg{Payload: "second"})
	h.eventuallyReceived("superseded", 1)
	h.restart(10 * time.Second)
	h.parked(3)
	waits := h.eng.PendingWaits(h.flow.ID)
	require.Len(t, waits, 1)
	assert.Equal(t, time.Date(2026, 10, 30, 12, 1, 30, 0, time.UTC), waits[0].Deadline,
		"the window still closes at its maximum, measured from the first arrival")
	h.clock.Advance(30 * time.Second)
	got := h.eventuallyReceived("out", 1)
	assert.Equal(t, "second", got[0].Payload, "the latest message before the restart is the one sent")
}

// TestRateQueueRestoresInOrder covers queued rate-limited messages whose
// slots passed while the server was down: they leave one interval apart from
// the restart, in order, rather than all at once.
func TestRateQueueRestoresInOrder(t *testing.T) {
	h := newDurableHarness(t, chainGraph(t, "delay",
		map[string]any{"mode": "rate", "interval": "1m", "on_limit": "queue"}))
	h.start()
	h.send("start", &engine.Msg{Payload: "a"})
	h.eventuallyReceived("out", 1)
	h.send("start", &engine.Msg{Payload: "b"})
	h.parked(1)
	h.send("start", &engine.Msg{Payload: "c"})
	h.parked(2)
	h.restart(10 * time.Minute)
	got := h.eventuallyReceived("out", 2)
	assert.Equal(t, "b", got[1].Payload, "the first restored message goes at once")
	h.parked(3)
	h.clock.Advance(time.Minute)
	got = h.eventuallyReceived("out", 3)
	assert.Equal(t, "c", got[2].Payload, "the next one waits an interval from the restart")
}

// TestRateQueueKeepsOrderWhenSomeSlotsPassed covers a restart that passes only
// part of the schedule. At a 1m interval a goes at 0, b is queued for 1m and c
// for 2m. Back up at 1m30, b is late and c is not, so b goes at once and c
// moves to 2m30, one interval behind it. Reclaiming b's slot from the end of
// the stored schedule (3m) put it behind c.
func TestRateQueueKeepsOrderWhenSomeSlotsPassed(t *testing.T) {
	h := newDurableHarness(t, chainGraph(t, "delay",
		map[string]any{"mode": "rate", "interval": "1m", "on_limit": "queue"}))
	h.start()
	h.send("start", &engine.Msg{Payload: "a"})
	h.eventuallyReceived("out", 1)
	h.send("start", &engine.Msg{Payload: "b"})
	h.parked(1)
	h.send("start", &engine.Msg{Payload: "c"})
	h.parked(2)
	h.restart(90 * time.Second)
	got := h.eventuallyReceived("out", 2)
	assert.Equal(t, "b", got[1].Payload, "the late message keeps its place ahead of the one still queued")
	h.parked(3)
	h.clock.Advance(59 * time.Second)
	assert.Never(t, func() bool { return len(h.sinks["out"].received()) > 2 }, 100*time.Millisecond, time.Millisecond,
		"c must not leave within an interval of b")
	h.clock.Advance(time.Second)
	got = h.eventuallyReceived("out", 3)
	assert.Equal(t, "c", got[2].Payload, "c leaves one interval after b")
}

// TestWaitUntilRestores covers a wait-until across a restart: it fires at
// the instant the message named.
func TestWaitUntilRestores(t *testing.T) {
	h := newDurableHarness(t, chainGraph(t, "wait-until", map[string]any{"time": "payload.at"}))
	h.start()
	h.send("start", &engine.Msg{Payload: map[string]any{"at": "2026-10-30T12:05:00Z"}})
	h.parked(1)
	h.restart(time.Minute)
	h.parked(2)
	h.clock.Advance(4 * time.Minute)
	got := h.eventuallyReceived("out", 1)
	assert.Equal(t, "2026-10-30T12:05:00Z", got[0].Meta["waited_until"])
}

// TestConditionWaitRestoresDeadline covers a condition wait whose timeout
// passed while the server was down: it reports timeout, with the message it
// held.
func TestConditionWaitRestoresDeadline(t *testing.T) {
	h := newDurableHarness(t, chainGraph(t, "condition-wait",
		map[string]any{"condition": "payload == true", "timeout": "1m", "poll_interval": "10s"}, "matched", "timeout"))
	h.start()
	h.send("start", &engine.Msg{Payload: false, Topic: "door"})
	h.parked(1)
	h.restart(5 * time.Minute)
	got := h.eventuallyReceived("timeout", 1)
	assert.Equal(t, "door", got[0].Topic, "the held message is the one that timed out")
	assert.Empty(t, h.sinks["matched"].received())
	h.eventuallyStored(0)
}

// TestConditionWaitResumesWithShortRemainder covers a hold longer than the
// time left after a restart. Timeout 10m, held for 5m, restart at 6m leaves
// 4m, so the restarted hold cannot finish and the wait times out at its
// original deadline instead of failing the run as a configuration error.
func TestConditionWaitResumesWithShortRemainder(t *testing.T) {
	h := newDurableHarness(t, chainGraph(t, "condition-wait",
		map[string]any{"condition": "payload == true", "stable_for": "5m", "timeout": "10m", "poll_interval": "1m"},
		"matched", "timeout"))
	h.start()
	h.send("start", &engine.Msg{Payload: true, Topic: "door"})
	h.parked(1)
	h.restart(6 * time.Minute)
	// The restored wait parks for exactly the 4m left. Other engine waits
	// also use the clock, so counting calls cannot single it out.
	require.Eventually(t, func() bool { return slices.Contains(h.clock.AfterCalls(), 4*time.Minute) },
		3*time.Second, time.Millisecond, "the resumed wait must park until its original deadline")
	h.clock.Advance(4 * time.Minute)
	got := h.eventuallyReceived("timeout", 1)
	assert.Equal(t, "door", got[0].Topic, "the held message is the one that timed out")
	assert.Empty(t, h.sinks["matched"].received())
	h.eventuallyStored(0)
}

// TestFanInGroupsSurviveRestart covers Join groups: a count-completed group
// includes parts collected before the restart, and a timeout group restores
// and releases what arrived.
func TestFanInGroupsSurviveRestart(t *testing.T) {
	t.Run("count completes with earlier parts", func(t *testing.T) {
		h := newDurableHarness(t, chainGraph(t, "join", map[string]any{"mode": "array", "count": "3"}))
		h.start()
		<-h.send("start", &engine.Msg{Payload: "a"}).Done()
		<-h.send("start", &engine.Msg{Payload: "b"}).Done()
		h.eventuallyStored(1)
		h.restart(time.Minute)
		h.send("start", &engine.Msg{Payload: "c"})
		got := h.eventuallyReceived("out", 1)
		assert.Equal(t, []any{"a", "b", "c"}, got[0].Payload, "the parts from before the restart are in the group")
		h.eventuallyStored(0)
	})

	t.Run("timeout releases what arrived", func(t *testing.T) {
		h := newDurableHarness(t, chainGraph(t, "join", map[string]any{"mode": "array", "count": "5", "timeout": "2m"}))
		h.start()
		h.send("start", &engine.Msg{Payload: "a", Topic: "t"})
		h.parked(1)
		second := h.send("start", &engine.Msg{Payload: "b", Topic: "t"})
		<-second.Done()
		h.restart(30 * time.Second)
		h.parked(2)
		h.clock.Advance(90 * time.Second)
		got := h.eventuallyReceived("out", 1)
		assert.Equal(t, []any{"a", "b"}, got[0].Payload)
		assert.Equal(t, true, got[0].Meta["incomplete"], "a timed-out group says it is incomplete")
	})
}
