package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flowCtx is a trigger context in a named flow that records skipped starts.
type flowCtx struct {
	*testCtx
	flowID  string
	mu      sync.Mutex
	skipped []string
}

func (c *flowCtx) FlowID() string { return c.flowID }
func (c *flowCtx) Skipped(msg *engine.Msg, reason string) {
	c.mu.Lock()
	c.skipped = append(c.skipped, reason)
	c.mu.Unlock()
}

// armTrigger starts Control Changed in a flow and returns its messages.
func armTrigger(t *testing.T, cfg, flowID string) (*flowCtx, chan *engine.Msg) {
	t.Helper()
	ec := &flowCtx{testCtx: newTestCtx(cfg, nil), flowID: flowID}
	msgs := make(chan *engine.Msg, 64)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ControlChangedNode{}.Start(ctx, ec, func(m *engine.Msg) { msgs <- m }) }()
	time.Sleep(50 * time.Millisecond)
	return ec, msgs
}

func received(msgs chan *engine.Msg) int {
	n := 0
	for {
		select {
		case <-msgs:
			n++
		case <-time.After(100 * time.Millisecond):
			return n
		}
	}
}

// TestControlChangedIgnoreWrites covers FR-026: by default a trigger skips
// only its own flow's writes, so another flow's write hands the control over;
// any_flow skips every flow, none skips nothing, and the older boolean still
// reads as it meant.
func TestControlChangedIgnoreWrites(t *testing.T) {
	setupControls(t)
	snap, err := controls.S.Create("House mode", controls.KindText, "", nil, false)
	require.NoError(t, err)
	own := controls.Writer{Source: controls.SourceFlow, FlowID: "flow-b", RunID: "run-1"}
	other := controls.Writer{Source: controls.SourceFlow, FlowID: "flow-a", RunID: "run-2"}

	for _, tc := range []struct {
		name        string
		option      string
		own, others int
	}{
		{"default own flow", ``, 0, 1},
		{"own flow", `,"ignore_writes":"own_flow"`, 0, 1},
		{"any flow", `,"ignore_writes":"any_flow"`, 0, 0},
		{"nothing", `,"ignore_writes":"none"`, 1, 1},
		{"old true", `,"ignore_flow_writes":true`, 0, 1},
		{"old false", `,"ignore_flow_writes":false`, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, msgs := armTrigger(t, `{"control":"`+snap.ID.String()+`"`+tc.option+`}`, "flow-b")
			_, err := controls.S.Set(snap.ID, tc.name+" own", own)
			require.NoError(t, err)
			assert.Equal(t, tc.own, received(msgs), "writes from the trigger's own flow")
			_, err = controls.S.Set(snap.ID, tc.name+" other", other)
			require.NoError(t, err)
			got := received(msgs)
			assert.Equal(t, tc.others, got, "writes from another flow")
		})
	}

	_, msgs := armTrigger(t, `{"control":"`+snap.ID.String()+`"}`, "flow-b")
	_, err = controls.S.Set(snap.ID, "night", other)
	require.NoError(t, err)
	select {
	case m := <-msgs:
		p := m.Payload.(map[string]any)
		assert.Equal(t, "flow-a", p["flow_id"])
		assert.Equal(t, "run-2", p["run_id"])
		assert.Equal(t, 1, m.Meta["control_depth"])
	case <-time.After(time.Second):
		require.FailNow(t, "no message")
	}
}

// TestControlLoopStops covers FR-027: flow C on X sets Y and flow D on Y sets
// X. Each run writes with its trigger's depth, so the chain grows until the
// limit, where the trigger records one skipped start naming the chain.
func TestControlLoopStops(t *testing.T) {
	setupControls(t)
	x, err := controls.S.Create("X", controls.KindNumber, "", nil, false)
	require.NoError(t, err)
	y, err := controls.S.Create("Y", controls.KindNumber, "", nil, false)
	require.NoError(t, err)
	cCtx, onX := armTrigger(t, `{"control":"`+x.ID.String()+`","loop_limit":4}`, "flow-c")
	dCtx, onY := armTrigger(t, `{"control":"`+y.ID.String()+`","loop_limit":4}`, "flow-d")

	// run stands in for the engine: a run started by msg writes the other
	// control with its depth and chain, as Set Control does.
	run := func(msg *engine.Msg, flow, name string, target controls.Snapshot, value float64) {
		depth, _ := msg.Meta["control_depth"].(int)
		chain, _ := msg.Meta["control_chain"].([]string)
		w := controls.Writer{Source: controls.SourceFlow, FlowID: flow, Depth: depth,
			Chain: append(append([]string{}, chain...), name+" set "+target.Name)}
		_, err := controls.S.Set(target.ID, value, w)
		require.NoError(t, err)
	}
	_, err = controls.S.Set(x.ID, 1, controls.By(controls.SourceDashboard))
	require.NoError(t, err)

	runs := 0
	deadline := time.After(3 * time.Second)
	for value := 2.0; ; value++ {
		select {
		case m := <-onX:
			runs++
			run(m, "flow-c", "C", y, value)
		case m := <-onY:
			runs++
			run(m, "flow-d", "D", x, value)
		case <-time.After(300 * time.Millisecond):
			cCtx.mu.Lock()
			dCtx.mu.Lock()
			skipped := append(append([]string{}, cCtx.skipped...), dCtx.skipped...)
			dCtx.mu.Unlock()
			cCtx.mu.Unlock()
			assert.Equal(t, 5, runs, "the dashboard write's run and four handoffs")
			require.Len(t, skipped, 1)
			assert.Contains(t, skipped[0], "control loop: C set Y → D set X → C set Y → D set X")
			return
		case <-deadline:
			require.FailNow(t, "the loop never stopped")
		}
	}
}
