package engine_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHistory stands in for the database the run history is written to. It
// records the events so what a run persists can be asserted without one.
type fakeHistory struct {
	mu     sync.Mutex
	events []*models.RunEvent
}

func (h *fakeHistory) RunStarted(run *models.Run) {}
func (h *fakeHistory) Event(ev *models.RunEvent) {
	h.mu.Lock()
	h.events = append(h.events, ev)
	h.mu.Unlock()
}
func (h *fakeHistory) RunFinished(runID uuid.UUID, status string, endedAt time.Time, errMsg string, hadFailures bool) {
}
func (h *fakeHistory) Close() {}

// nodeEvents returns the recorded node_state events for one node.
func (h *fakeHistory) nodeEvents(nodeID string) []*models.RunEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*models.RunEvent
	for _, ev := range h.events {
		if ev.NodeID == nodeID && ev.Event == "node_state" {
			out = append(out, ev)
		}
	}
	return out
}

// TestRunFromNodeExecutesOnlyDownstream covers run-from-here, the editor's
// replay: an author picks a node, seeds it with the last run's inputs, and only
// that node and what follows it execute. Everything upstream is skipped rather
// than re-run, which is the whole point — re-running an upstream HTTP request
// or a device write would have side effects the author did not ask for.
func TestRunFromNodeExecutesOnlyDownstream(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var executed []string
	var mu sync.Mutex
	record := func(id string) func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		return func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			mu.Lock()
			executed = append(executed, id)
			mu.Unlock()
			return passthrough("out")(ctx, ec, in)
		}
	}
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts, fn: record("a")},
		&stubNode{typ: "b", inputs: []string{"in"}, outputs: outPorts, fn: record("b")},
		&stubNode{typ: "c", inputs: []string{"in"}, outputs: outPorts, fn: record("c")},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a"), gnode("b", "b"), gnode("c", "c")},
		[]flow.Edge{
			gedge("t", "out", "a", "in"),
			gedge("a", "out", "b", "in"),
			gedge("b", "out", "c", "in"),
		},
	)

	seed := &engine.Msg{Payload: map[string]any{"seeded": true}}
	snap := waitDone(t, startRun(t, e, g, func(ro *engine.RunOptions) {
		ro.StartNodeID = "b"
		ro.SeedInputs = map[string]*engine.Msg{"in": seed}
		ro.InitialMsg = nil
	}))

	require.Equalf(t, "succeeded", snap.Status, "a seeded partial run must complete: %s", snap.Error)

	mu.Lock()
	assert.Equal(t, []string{"b", "c"}, executed,
		"only the start node and what follows may run: re-running upstream would repeat its side effects")
	mu.Unlock()

	last := pub.lastStates()
	for id, want := range map[string]string{"t": "skipped", "a": "skipped", "b": "succeeded", "c": "succeeded"} {
		assert.Equalf(t, want, last[id], "node %s must end %q, which is what the editor draws its card from", id, want)
	}
}

// TestRunFromNodeRejectsMissingSeed pairs with the case above: run-from-here is
// only meaningful with inputs to seed the start node, and asking for one
// without them has to fail at the call rather than start a run whose first node
// waits forever on an input that will never arrive.
func TestRunFromNodeRejectsMissingSeed(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a")},
		[]flow.Edge{gedge("t", "out", "a", "in")},
	)
	_, err := e.RunFlow(engine.RunOptions{
		FlowID:        uuid.New(),
		TriggerNodeID: "t",
		Graph:         g,
		StartNodeID:   "a",
	})
	require.Error(t, err,
		"without seed inputs the start node would wait forever, so the run must be refused rather than started")
}

// TestCrossNodeExpressionEnv covers the `nodes` binding, which is what lets a
// node read an upstream node's output by id rather than only the message it was
// handed. The sum is 42 from node a plus 42 carried in the message = 84, so a
// binding that resolved to nothing would show up as a different number rather
// than a passing test.
func TestCrossNodeExpressionEnv(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var got any
	var evalErr error
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return map[string]*engine.Msg{"out": {Payload: map[string]any{"value": 42}}}, nil
			}},
		&stubNode{typ: "b", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				got, evalErr = ec.Eval("nodes.a.payload.value + payload.value", in["in"])
				return nil, evalErr
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a"), gnode("b", "b")},
		[]flow.Edge{gedge("t", "out", "a", "in"), gedge("a", "out", "b", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the expression is evaluated during the run: %s", snap.Error)
	require.NoError(t, evalErr, "the expression must evaluate against a live run's environment")

	// 42 from node a's stored output plus 42 carried in the message.
	assert.Equal(t, 84, got, "nodes.a.payload.value must resolve to the upstream node's output, not to nothing")
}

// TestNodeOutputsPersistedInSucceededEvent covers what a succeeded node writes
// to history. The stored outputs are what the debug panel shows and what
// run-from-here seeds a replay with, so a node that succeeded without recording
// them leaves both with nothing to work from.
func TestNodeOutputsPersistedInSucceededEvent(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	hist := &fakeHistory{}
	e, _ := testEngine(t, fc, engine.Options{History: hist},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return map[string]*engine.Msg{"out": {Payload: map[string]any{"answer": 42}, Topic: "topic-a"}}, nil
			}},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a")},
		[]flow.Edge{gedge("t", "out", "a", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the node must succeed for a succeeded event to exist: %s", snap.Error)

	events := hist.nodeEvents("a")
	require.NotEmpty(t, events, "a node that ran must record its states, or the run view has nothing to draw")

	final := events[len(events)-1]
	require.Equal(t, "succeeded", final.State, "the last recorded state is the node's outcome")

	var body struct {
		Outputs map[string]struct {
			Payload map[string]any `json:"payload"`
			Topic   string         `json:"topic"`
		} `json:"outputs"`
	}
	require.NoErrorf(t, json.Unmarshal([]byte(final.Payload), &body),
		"the event payload is stored as JSON and read back by the API: %q", final.Payload)

	out, ok := body.Outputs["out"]
	require.Truef(t, ok, "the emitted port must appear in the stored outputs: %q", final.Payload)
	assert.Equal(t, float64(42), out.Payload["answer"], "the debug panel and run-from-here both read this payload back")
	assert.Equal(t, "topic-a", out.Topic, "a message is more than its payload, so the topic must be stored too")
}

// TestWebhookRegistry covers the token table behind /hook/{token}. Resolution
// answers two separate questions — is this token armed, and is this method
// allowed — because an armed token called with the wrong method is a 405 while
// an unknown token is a 404, and collapsing them would leak which tokens exist.
//
// The collision case is the one worth the setup: redeploying a flow arms the
// new registration before the old one unregisters, so the loser's unregister
// must not disarm the winner and leave the webhook dead.
func TestWebhookRegistry(t *testing.T) {
	reg := engine.NewWebhookRegistry(nil)

	var fired []*engine.Msg
	unregister := reg.Register(engine.WebhookRegistration{Token: "tok", Methods: "POST", Emit: func(m *engine.Msg) { fired = append(fired, m) }})

	_, found, _ := reg.Resolve("nope", "POST")
	assert.False(t, found, "an unarmed token must not resolve, which is what makes it a 404")

	_, found, allowed := reg.Resolve("tok", "GET")
	assert.True(t, found, "the token is armed, so it is found even for a method it refuses")
	assert.False(t, allowed, "a POST-only token must refuse GET as a 405 rather than answer it")

	entry, found, allowed := reg.Resolve("tok", "POST")
	require.True(t, found, "the armed token must resolve for its own method")
	require.True(t, allowed, "the armed token must allow the method it registered")
	require.NotNil(t, entry.Emit, "resolving is only useful if it yields the callback that starts the run")

	entry.Emit(&engine.Msg{})
	assert.Len(t, fired, 1, "emitting must reach the registration's own callback")

	// ANY accepts both methods.
	reg2 := engine.NewWebhookRegistry(nil)
	reg2.Register(engine.WebhookRegistration{Token: "any", Methods: "ANY", Emit: func(*engine.Msg) {}})
	for _, method := range []string{"GET", "POST"} {
		_, _, allowed := reg2.Resolve("any", method)
		assert.Truef(t, allowed, "a token registered as ANY must allow %s", method)
	}

	// Collision: the last registration wins and survives the loser's
	// unregister.
	var winner bool
	unregister2 := reg.Register(engine.WebhookRegistration{Token: "tok", Methods: "POST", Emit: func(*engine.Msg) { winner = true }})
	unregister() // Loser's unregister must not remove the winner.

	entry, found, allowed = reg.Resolve("tok", "POST")
	require.True(t, found, "a redeploy arms the new registration before the old unregisters; the token must stay armed")
	require.True(t, allowed, "the surviving registration keeps its allowed methods")

	entry.Emit(&engine.Msg{})
	assert.True(t, winner, "emitting must reach the last registration, or a redeployed flow's webhook starts the old graph")

	unregister2()
	_, found, _ = reg.Resolve("tok", "POST")
	assert.False(t, found, "the active registration unregistering is what actually disarms the token")
}
