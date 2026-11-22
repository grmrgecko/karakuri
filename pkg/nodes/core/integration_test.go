package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive the real nodes through the real engine. Every node here is
// covered on its own elsewhere; what only appears when they are wired together
// is the interaction between fan-out scheduling, group frames, and node state
// that outlives a run — a node can be correct in isolation and still be wrong
// about which instance it belongs to.

// coreEngine builds an engine over a registry holding every core node type.
func coreEngine(t *testing.T) *engine.Engine {
	t.Helper()
	reg := engine.NewRegistry()
	RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg})
	eng.Start()
	t.Cleanup(eng.Stop)
	return eng
}

// cfgJSON marshals a node config.
func cfgJSON(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err, "the node config fixture must encode")
	return raw
}

// runGraph runs a graph seeded at node "t" and waits for it to settle.
func runGraph(t *testing.T, eng *engine.Engine, g *flow.Graph, initial *engine.Msg) engine.RunSnapshot {
	t.Helper()
	h, err := eng.RunFlow(engine.RunOptions{
		FlowID:        uuid.New(),
		FlowName:      "integration",
		FlowVersionID: 1,
		TriggerNodeID: "t",
		Graph:         g,
		InitialMsg:    initial,
	})
	require.NoError(t, err, "the run must start before anything about it can be asserted")

	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		require.FailNowf(t, "the run never settled",
			"a run that hangs is the failure, not the timeout: %+v", h.Snapshot())
	}
	return h.Snapshot()
}

// TestSplitChangeJoinThroughTheEngine covers the fan-out path end to end with
// real nodes. The change node runs as three separate instances, one per part,
// and each has to interpolate against its own message rather than a shared one
// — which is scheduling behaviour no single-node test can reach.
func TestSplitChangeJoinThroughTheEngine(t *testing.T) {
	eng := coreEngine(t)
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "s", Type: "split", Config: cfgJSON(t, map[string]any{
				"mode": "array", "property": "payload.rooms",
			})},
			{ID: "c", Type: "change", Config: cfgJSON(t, map[string]any{
				"rules": []any{map[string]any{
					"op": "set", "target": "payload", "from": "string",
					"value": "{{ payload }}-checked",
				}},
			})},
			{ID: "j", Type: "join", Config: cfgJSON(t, map[string]any{"mode": "array"})},
			{ID: "d", Type: "debug"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "s", TargetHandle: "in"},
			{ID: "e2", Source: "s", SourceHandle: "out", Target: "c", TargetHandle: "in"},
			{ID: "e3", Source: "c", SourceHandle: "out", Target: "j", TargetHandle: "in"},
			{ID: "e4", Source: "j", SourceHandle: "out", Target: "d", TargetHandle: "in"},
		},
	}
	initial := &engine.Msg{Payload: map[string]any{
		"rooms": []any{"kitchen", "hall", "study"},
	}}

	snap := runGraph(t, eng, g, initial)
	require.Equalf(t, "succeeded", snap.Status,
		"split, per-part change, and join must complete as one run: %s", snap.Error)
}

// TestSplitFanOutCallsAnEndpointPerPart is the clearest statement of what
// fan-out is for: three parts must produce three separate HTTP calls, each to
// the URL its own part interpolated. A scheduler that ran the node once would
// call the endpoint once, and the whole feature would be decoration.
func TestSplitFanOutCallsAnEndpointPerPart(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	eng := coreEngine(t)
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "s", Type: "split", Config: cfgJSON(t, map[string]any{"mode": "array"})},
			{ID: "h", Type: "http-request", Config: cfgJSON(t, map[string]any{
				"method": "GET", "url": srv.URL + "/device/{{ payload }}",
			})},
			{ID: "j", Type: "join", Config: cfgJSON(t, map[string]any{"mode": "array"})},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "s", TargetHandle: "in"},
			{ID: "e2", Source: "s", SourceHandle: "out", Target: "h", TargetHandle: "in"},
			{ID: "e3", Source: "h", SourceHandle: "out", Target: "j", TargetHandle: "in"},
		},
	}

	snap := runGraph(t, eng, g, &engine.Msg{Payload: []any{"a", "b", "c"}})
	require.Equalf(t, "succeeded", snap.Status, "every part's call must succeed: %s", snap.Error)

	mu.Lock()
	defer mu.Unlock()
	require.Lenf(t, paths, 3, "the endpoint must be called once per part, got %v", paths)
	// Parts run concurrently, so the order they arrive in is not fixed.
	assert.ElementsMatch(t, []string{"/device/a", "/device/b", "/device/c"}, paths,
		"each call must carry the URL its own part interpolated, not a shared one")
}

// statePub is the engine's real publisher interface with an in-memory map
// behind it, recording the last state each node reached. It is how a test tells
// a node that ran from one that was skipped, which is the only observable
// difference a blocked message makes.
type statePub struct {
	mu     sync.Mutex
	states map[string]string
}

func newStatePub() *statePub { return &statePub{states: map[string]string{}} }

func (p *statePub) Publish(topic, msgType string, payload any) {
	if msgType != "node_state" {
		return
	}
	body, ok := payload.(map[string]any)
	if !ok {
		return
	}
	nodeID, _ := body["node_id"].(string)
	state, _ := body["state"].(string)
	p.mu.Lock()
	p.states[nodeID] = state
	p.mu.Unlock()
}

// reset clears the recorded states between runs.
func (p *statePub) reset() {
	p.mu.Lock()
	p.states = map[string]string{}
	p.mu.Unlock()
}

// state returns a node's last recorded state.
func (p *statePub) state(nodeID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.states[nodeID]
}

// TestFilterAcrossRunsThroughTheEngine covers the filter's memory surviving the
// run boundary, which is the shape it is actually used in: a sensor sends one
// message per run, so a filter whose state lived only for a run would pass
// every reading and do nothing at all. Nothing in the single-node tests can
// show this, because they reuse one context by construction.
func TestFilterAcrossRunsThroughTheEngine(t *testing.T) {
	reg := engine.NewRegistry()
	RegisterAll(reg)
	pub := newStatePub()
	eng := engine.New(engine.Options{Registry: reg, Publisher: pub})
	eng.Start()
	t.Cleanup(eng.Stop)

	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "f", Type: "filter", Config: cfgJSON(t, map[string]any{
				"mode": "changed", "property": "payload.temperature",
			})},
			{ID: "d", Type: "debug"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "f", TargetHandle: "in"},
			{ID: "e2", Source: "f", SourceHandle: "out", Target: "d", TargetHandle: "in"},
		},
	}

	// One flow ID across every run, so they share the node store.
	flowID := uuid.New()
	// send runs the flow once and reports whether the message reached debug.
	send := func(temp float64) bool {
		t.Helper()
		pub.reset()
		h, err := eng.RunFlow(engine.RunOptions{
			FlowID:        flowID,
			FlowName:      "filter-flow",
			FlowVersionID: 1,
			TriggerNodeID: "t",
			Graph:         g,
			InitialMsg:    &engine.Msg{Payload: map[string]any{"temperature": temp}},
		})
		require.NoError(t, err, "each reading's run must start")

		select {
		case <-h.Done():
		case <-time.After(15 * time.Second):
			require.FailNow(t, "the run never settled")
		}
		snap := h.Snapshot()
		require.Equalf(t, "succeeded", snap.Status, "the run for %g must succeed whether or not the message passed: %s", temp, snap.Error)
		return pub.state("d") == "succeeded"
	}

	assert.True(t, send(20), "the first reading has nothing to compare against and must pass")
	assert.False(t, send(20),
		"a repeat must be blocked, which can only happen if the filter's memory outlived the first run")
	assert.True(t, send(21), "a changed reading must pass")
	assert.False(t, send(21), "the repeat of the changed reading must be blocked in turn")

	// The filter's memory is durable: a redeploy purges only the memory-only
	// stores, so the identical reading stays blocked. Deleting the flow is
	// what forgets it.
	eng.PurgeNodeState(flowID)
	assert.False(t, send(21),
		"a redeploy must not reset the filter; its baseline lives in the durable store")
	eng.PurgeDurableNodeState(flowID)
	assert.True(t, send(21),
		"deleting the flow drops the durable store, so the same reading is new again")
}

// TestTriggerResetThroughTheEngine covers an optional port under real
// scheduling. A switch feeds the trigger's gating input on one branch and its
// optional reset on the other, so on a reset only the optional port has a
// message. Without optional ports the trigger would wait for its main input
// forever and the run would hang — which is why the assertion that matters here
// is simply that the run settles at all.
func TestTriggerResetThroughTheEngine(t *testing.T) {
	eng := coreEngine(t)
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "w", Type: "switch", Config: cfgJSON(t, map[string]any{
				"mode": "rules", "property": "payload.motion",
				"operator": "eq", "value": "true",
			})},
			{ID: "g", Type: "trigger", Config: cfgJSON(t, map[string]any{
				"first_source": "original", "duration": "10ms",
				"second_source": "payload", "second_payload": map[string]any{"state": "off"},
			})},
			{ID: "d", Type: "debug"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "w", TargetHandle: "in"},
			{ID: "e2", Source: "w", SourceHandle: "true", Target: "g", TargetHandle: "in"},
			{ID: "e3", Source: "w", SourceHandle: "false", Target: "g", TargetHandle: "reset"},
			{ID: "e4", Source: "g", SourceHandle: "expired", Target: "d", TargetHandle: "in"},
		},
	}

	// Motion true takes the gating branch and the trigger runs its wait.
	snap := runGraph(t, eng, g, &engine.Msg{Payload: map[string]any{"motion": true}})
	require.Equalf(t, "succeeded", snap.Status, "the ordinary path through the gating input must complete: %s", snap.Error)

	// Motion false takes only the reset branch.
	snap = runGraph(t, eng, g, &engine.Msg{Payload: map[string]any{"motion": false}})
	require.Equalf(t, "succeeded", snap.Status,
		"the node must run on its optional port alone, or the run hangs waiting for an input nothing will send: %s", snap.Error)
}

// TestBatchThenJoinAcrossRuns covers a group created in one run out of messages
// that arrived in three. The batch node holds state across runs like the
// filter, and the group it stamps on release has to be a group the join
// recognises — two separate mechanisms that only meet here.
func TestBatchThenJoinAcrossRuns(t *testing.T) {
	reg := engine.NewRegistry()
	RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg})
	eng.Start()
	t.Cleanup(eng.Stop)

	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "b", Type: "batch", Config: cfgJSON(t, map[string]any{
				"mode": "count", "count": "3",
			})},
			{ID: "j", Type: "join", Config: cfgJSON(t, map[string]any{"mode": "array"})},
			{ID: "d", Type: "debug"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "b", TargetHandle: "in"},
			{ID: "e2", Source: "b", SourceHandle: "out", Target: "j", TargetHandle: "in"},
			{ID: "e3", Source: "j", SourceHandle: "out", Target: "d", TargetHandle: "in"},
		},
	}

	flowID := uuid.New()
	for i := 1; i <= 3; i++ {
		h, err := eng.RunFlow(engine.RunOptions{
			FlowID:        flowID,
			FlowName:      "batch-flow",
			FlowVersionID: 1,
			TriggerNodeID: "t",
			Graph:         g,
			InitialMsg:    &engine.Msg{Payload: i},
		})
		require.NoErrorf(t, err, "run %d must start", i)

		select {
		case <-h.Done():
		case <-time.After(15 * time.Second):
			require.FailNowf(t, "a run never settled",
				"run %d hung, which is what a batch released into a group the join does not recognise looks like", i)
		}
		require.Equalf(t, "succeeded", h.Snapshot().Status, "run %d must succeed: %s", i, h.Snapshot().Error)
	}
}

// TestErrorBranchOnAFannedOutPart covers a failure inside a fan-out, which is
// where the failure design and the grouping design meet. One part's HTTP call
// fails and is routed onto its error branch; the run still succeeds because the
// failure was handled, the other two parts are unaffected, and the run is still
// flagged as having had failures so the history does not read as clean.
func TestErrorBranchOnAFannedOutPart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/device/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	eng := coreEngine(t)
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "manual"},
			{ID: "s", Type: "split", Config: cfgJSON(t, map[string]any{"mode": "array"})},
			{ID: "h", Type: "http-request",
				// One attempt only, so the test does not sit through backoff.
				Retry: &flow.RetryPolicy{MaxAttempts: 1},
				Config: cfgJSON(t, map[string]any{
					"method": "GET", "url": srv.URL + "/device/{{ payload }}",
				})},
			{ID: "ok", Type: "debug"},
			{ID: "bad", Type: "debug"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "s", TargetHandle: "in"},
			{ID: "e2", Source: "s", SourceHandle: "out", Target: "h", TargetHandle: "in"},
			{ID: "e3", Source: "h", SourceHandle: "out", Target: "ok", TargetHandle: "in"},
			{ID: "e4", Source: "h", SourceHandle: "error", Target: "bad", TargetHandle: "in"},
		},
	}

	snap := runGraph(t, eng, g, &engine.Msg{Payload: []any{"good", "bad", "fine"}})
	require.Equalf(t, "succeeded", snap.Status,
		"one part failing onto a wired error branch is handled, so the run succeeds: %s", snap.Error)
	assert.True(t, snap.HadFailures,
		"a handled failure must still mark the run, or its history reads as clean")
}
