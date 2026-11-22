package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// conditionRunOutputs reads persisted output envelopes after the run settles.
func conditionRunOutputs(t *testing.T, handle *engine.RunHandle) map[string][]*engine.Msg {
	t.Helper()
	select {
	case <-handle.Done():
	case <-time.After(3 * time.Second):
		require.FailNow(t, "the condition run did not settle")
	}
	var events []*models.RunEvent
	require.Eventually(t, func() bool {
		stored, err := models.FindRunByID(handle.ID)
		return err == nil && stored.Status != engine.RunStatusRunning
	}, time.Second, time.Millisecond, "the real history writer must finish before reading outputs")
	var err error
	events, err = models.ListRunEvents(handle.ID)
	require.NoError(t, err)
	outputs := map[string][]*engine.Msg{}
	for _, event := range events {
		if event.NodeID != "wait" || event.Payload == "" || event.State != engine.NodeStateSucceeded {
			continue
		}
		var body struct {
			Outputs map[string]*engine.Msg `json:"outputs"`
		}
		require.NoError(t, json.Unmarshal([]byte(event.Payload), &body))
		for port, message := range body.Outputs {
			outputs[port] = append(outputs[port], message)
		}
	}
	return outputs
}

// conditionCheckEngine uses production control storage, scheduling and history.
func conditionCheckEngine(t *testing.T, clock *engine.FakeClock) *engine.Engine {
	t.Helper()
	reg := engine.NewRegistry()
	RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg, Clock: clock, MaxParallelNodes: 1, History: engine.NewDBHistory(zap.NewNop())})
	eng.Start()
	t.Cleanup(eng.Stop)
	return eng
}

// TestConditionWaitChangeChecksAcrossRuns pins cross-run broadcast, cancellation
// ownership, sampled stability and fixed deadlines against real SQLite controls.
func TestConditionWaitChangeChecksAcrossRuns(t *testing.T) {
	for _, tc := range []struct{ name, stable, port string }{
		{"false change restarts the hold", "2s", "matched"},
		{"changes cannot extend the deadline", "8s", "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupControls(t)
			control, err := controls.S.Create("Observed condition", controls.KindBoolean, "", nil, false)
			require.NoError(t, err)
			clock := engine.NewFakeClock(time.Now())
			eng := conditionCheckEngine(t, clock)
			g := &flow.Graph{Nodes: []flow.Node{
				{ID: "start", Type: "manual"}, {ID: "changed", Type: "manual"},
				{ID: "value", Type: "control-get", Config: cfgJSON(t, map[string]any{"kind": "boolean", "control": control.ID.String()})},
				{ID: "wait", Type: "condition-wait", ExecuteOnce: true, Config: cfgJSON(t, map[string]any{"condition": "payload == true", "timeout": "10s", "stable_for": tc.stable, "poll_interval": "1m"})},
			}, Edges: []flow.Edge{
				{ID: "start", Source: "start", SourceHandle: "out", Target: "wait", TargetHandle: "in"},
				{ID: "changed", Source: "changed", SourceHandle: "out", Target: "wait", TargetHandle: "check"},
				{ID: "value", Source: "value", SourceHandle: "out", Target: "wait", TargetHandle: "param:value", Kind: "value"},
			}}
			flowID := uuid.New()
			start := func(id uuid.UUID, trigger, payload string, graph *flow.Graph) *engine.RunHandle {
				t.Helper()
				h, err := eng.RunFlow(engine.RunOptions{FlowID: id, FlowName: "condition checks", FlowVersionID: 1, TriggerNodeID: trigger, Graph: graph, InitialMsg: &engine.Msg{Payload: payload, Topic: "original", Meta: map[string]any{"id": payload}}})
				require.NoError(t, err)
				return h
			}
			cancelled := start(flowID, "start", "cancelled", g)
			require.True(t, clock.BlockUntilAfterCalls(1, time.Second))
			waiter := start(flowID, "start", "kept", g)
			require.True(t, clock.BlockUntilAfterCalls(2, time.Second))
			peer := start(flowID, "start", "peer", g)
			require.True(t, clock.BlockUntilAfterCalls(3, time.Second))
			foreignID := uuid.New()
			foreign := start(foreignID, "start", "other flow", g)
			require.True(t, clock.BlockUntilAfterCalls(4, time.Second))
			require.True(t, eng.CancelRun(cancelled.ID))
			assert.Empty(t, conditionRunOutputs(t, cancelled), "cancellation must emit nothing")
			assert.Equal(t, engine.RunStatusCanceled, cancelled.Snapshot().Status)

			setAndCheck := func(value bool, calls int) {
				t.Helper()
				_, err := controls.S.Set(control.ID, value, controls.By(controls.SourceAPI))
				require.NoError(t, err)
				check := start(flowID, "changed", "ignored signal", g)
				assert.Empty(t, conditionRunOutputs(t, check), "the check execution must emit nothing")
				require.Equal(t, engine.RunStatusSucceeded, check.Snapshot().Status)
				require.True(t, clock.BlockUntilAfterCalls(calls, time.Second), "the surviving waiter must wake before its one-minute poll")
			}
			clock.Advance(time.Second)
			setAndCheck(true, 6)
			clock.Advance(time.Second)
			setAndCheck(false, 8)
			clock.Advance(time.Second)
			setAndCheck(true, 10)
			clock.Advance(2 * time.Second)
			if tc.port == "timeout" {
				select {
				case <-waiter.Done():
					require.FailNow(t, "a false observation must restart the eight-second hold")
				default:
				}
				clock.Advance(5 * time.Second)
			}
			peerOut := conditionRunOutputs(t, peer)
			require.Len(t, peerOut[tc.port], 1, "one check must wake every active wait on the node")
			assert.Equal(t, "peer", peerOut[tc.port][0].Payload, "each concurrent wait keeps its own message")
			out := conditionRunOutputs(t, waiter)
			require.Equal(t, engine.RunStatusSucceeded, waiter.Snapshot().Status)
			require.Len(t, out[tc.port], 1)
			assert.Equal(t, "kept", out[tc.port][0].Payload)
			assert.Equal(t, "original", out[tc.port][0].Topic)
			assert.Equal(t, "kept", out[tc.port][0].Meta["id"])
			assert.Equal(t, true, out[tc.port][0].Meta["condition_value"])
			if tc.port == "matched" {
				assert.Equal(t, float64(5000), out[tc.port][0].Meta["condition_wait_ms"], "the false change moves the hold start to three seconds")
				select {
				case <-foreign.Done():
					require.FailNow(t, "a check in one flow must not wake another flow")
				default:
				}
				clock.Advance(5 * time.Second)
			} else {
				assert.Equal(t, float64(10000), out[tc.port][0].Meta["condition_wait_ms"], "signals must not move the ten-second deadline")
			}
			assert.Len(t, conditionRunOutputs(t, foreign)["timeout"], 1, "the other flow retains polling and its own deadline")

			// A check without waiters must succeed even if its unused reader is invalid.
			g.Nodes[2].Config = cfgJSON(t, map[string]any{"kind": "boolean", "control": uuid.New().String()})
			idle := start(flowID, "changed", "no waiter", g)
			assert.Empty(t, conditionRunOutputs(t, idle))
			assert.Equal(t, engine.RunStatusSucceeded, idle.Snapshot().Status, "an idle check must not read a missing control")
		})
	}
}

// TestConditionWaitChecksWithOneWorker pins optional dispatch and execute-once
// when one branch changes a real control and signals another branch's wait.
func TestConditionWaitChecksWithOneWorker(t *testing.T) {
	for _, tc := range []struct {
		name         string
		delayedStart bool
	}{
		{"change wakes a parked wait", false},
		{"early check leaves execute once available", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupControls(t)
			control, err := controls.S.Create("Same-run condition", controls.KindBoolean, "", nil, false)
			require.NoError(t, err)
			clock := engine.NewFakeClock(time.Now())
			eng := conditionCheckEngine(t, clock)
			g := &flow.Graph{Nodes: []flow.Node{
				{ID: "t", Type: "manual"},
				{ID: "later", Type: "delay", Config: cfgJSON(t, map[string]any{"mode": "fixed", "duration": "1s"})},
				{ID: "set", Type: "control-set", Config: cfgJSON(t, map[string]any{"control": control.ID.String(), "source": "value", "value": "true"})},
				{ID: "value", Type: "control-get", Config: cfgJSON(t, map[string]any{"kind": "boolean", "control": control.ID.String()})},
				{ID: "wait", Type: "condition-wait", ExecuteOnce: true, Config: cfgJSON(t, map[string]any{"condition": "payload == true", "timeout": "10s", "poll_interval": "1m"})},
			}, Edges: []flow.Edge{
				{ID: "main", Source: "t", SourceHandle: "out", Target: "wait", TargetHandle: "in"},
				{ID: "later", Source: "t", SourceHandle: "out", Target: "later", TargetHandle: "in"},
				{ID: "set", Source: "later", SourceHandle: "out", Target: "set", TargetHandle: "in"},
				{ID: "check", Source: "set", SourceHandle: "out", Target: "wait", TargetHandle: "check"},
				{ID: "value", Source: "value", SourceHandle: "out", Target: "wait", TargetHandle: "param:value", Kind: "value"},
			}}
			if tc.delayedStart {
				g.Nodes = append(g.Nodes, flow.Node{ID: "start-later", Type: "delay", Config: cfgJSON(t, map[string]any{"mode": "fixed", "duration": "2s"})})
				g.Edges[0].Target = "start-later"
				g.Edges = append(g.Edges, flow.Edge{ID: "start-later", Source: "start-later", SourceHandle: "out", Target: "wait", TargetHandle: "in"})
			}
			h, err := eng.RunFlow(engine.RunOptions{FlowID: uuid.New(), FlowName: "single worker", FlowVersionID: 1, TriggerNodeID: "t", Graph: g, InitialMsg: &engine.Msg{Payload: "original"}})
			require.NoError(t, err)
			require.True(t, clock.BlockUntilAfterCalls(2, time.Second))
			clock.Advance(time.Second)
			if tc.delayedStart {
				require.Eventually(t, func() bool {
					events, err := models.ListRunEvents(h.ID)
					if err != nil {
						return false
					}
					for _, event := range events {
						if event.NodeID == "wait" && event.State == engine.NodeStateSucceeded {
							return true
						}
					}
					return false
				}, time.Second, time.Millisecond)
				clock.Advance(time.Second)
			}
			out := conditionRunOutputs(t, h)
			require.Equal(t, engine.RunStatusSucceeded, h.Snapshot().Status, h.Snapshot().Error)
			require.Len(t, out["matched"], 1, "a signal must not claim or be suppressed by execute once")
			assert.Equal(t, "original", out["matched"][0].Payload)
			assert.Empty(t, out["timeout"])
		})
	}
}
