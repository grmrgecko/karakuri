package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testCtx is the ExecContext nodes are driven through here. It is the real
// interface with in-memory backing rather than a mock: nodes touch the world
// only through this type, so standing it up is what lets a node run with no
// engine, no database, and no clock of its own.
type testCtx struct {
	cfg    string
	clock  engine.Clock
	nodeID string
	state  map[string]any
	vars   map[string]any
	// flowVars and globalVars stand in for the engine's persisted scopes.
	flowVars   map[string]any
	globalVars map[string]any
	store      engine.NodeStore
	durable    engine.NodeStore
	debugs     []debugCall
	mu         sync.Mutex
	emits      []emitCall
	parks      []time.Duration
	// ports stands in for the node's resolved signature. It defaults to the
	// one-in/one-out shape almost every node has; tests for nodes that declare
	// their own ports replace it with usePorts.
	ports flow.PortSet
}

// usePorts gives the context the signature a placed node of this type and
// config would have, for the node types whose ports come from config.
func (c *testCtx) usePorts(meta engine.NodeMeta, cfg string) *testCtx {
	c.ports = flow.NodePorts(meta, &flow.Node{Type: meta.Type, Config: json.RawMessage(cfg)}, nil)
	return c
}

type debugCall struct {
	payload any
	note    string
}

// emitCall records one mid-execution emission for assertions.
type emitCall struct {
	port string
	msg  *engine.Msg
}

func newTestCtx(cfg string, clock engine.Clock) *testCtx {
	if clock == nil {
		clock = engine.RealClock{}
	}
	return &testCtx{
		cfg:        cfg,
		clock:      clock,
		nodeID:     "n1",
		state:      make(map[string]any),
		vars:       make(map[string]any),
		flowVars:   make(map[string]any),
		globalVars: make(map[string]any),
		store:      engine.NewMemNodeStore(),
		durable:    engine.NewMemDurableStore(),
		ports: flow.PortSet{
			Inputs:  []flow.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
			Outputs: []flow.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny}},
			Known:   true,
		},
	}
}

// emitted returns a copy of the emissions recorded so far.
func (c *testCtx) emitted() []emitCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]emitCall, len(c.emits))
	copy(out, c.emits)
	return out
}

func (c *testCtx) Config(out any) error {
	if c.cfg == "" {
		return nil
	}
	return json.Unmarshal([]byte(c.cfg), out)
}
func (c *testCtx) Logger() *zap.Logger { return zap.NewNop() }
func (c *testCtx) Clock() engine.Clock { return c.clock }
func (c *testCtx) Eval(src string, msg *engine.Msg) (any, error) {
	return expr.Eval(src, c.testEnv(msg))
}
func (c *testCtx) Interpolate(s string, msg *engine.Msg) (string, error) {
	return expr.Interpolate(s, c.testEnv(msg))
}

// testEnv mirrors the engine's message environment without a nodes map.
func (c *testCtx) testEnv(msg *engine.Msg) map[string]any {
	if msg == nil {
		msg = &engine.Msg{}
	}
	return expr.BuildEnv(expr.Env{
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    msg.Meta,
		Vars:    c.vars,
		Flow:    c.flowVars,
		Global:  c.globalVars,
	})
}
func (c *testCtx) NodeID() string { return c.nodeID }

func (c *testCtx) Ports() flow.PortSet { return c.ports }

func (c *testCtx) Wired(port string) bool { return true }
func (c *testCtx) Debug(payload any, note string) {
	c.debugs = append(c.debugs, debugCall{payload: payload, note: note})
}
func (c *testCtx) GetState(key string) (any, bool) {
	v, ok := c.state[key]
	return v, ok
}
func (c *testCtx) SetState(key string, value any) { c.state[key] = value }
func (c *testCtx) NodeStore() engine.NodeStore    { return c.store }
func (c *testCtx) DurableStore() engine.NodeStore { return c.durable }

func (c *testCtx) GetVar(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.vars[key]
	return v, ok
}

func (c *testCtx) SetVar(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value == nil {
		delete(c.vars, key)
		return
	}
	c.vars[key] = value
}

func (c *testCtx) Vars() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]any, len(c.vars))
	for k, v := range c.vars {
		out[k] = v
	}
	return out
}

// scopeMap returns the map backing one scope, or nil for an unknown name.
func (c *testCtx) scopeMap(scope string) map[string]any {
	switch scope {
	case engine.ScopeRun:
		return c.vars
	case engine.ScopeFlow:
		return c.flowVars
	case engine.ScopeGlobal:
		return c.globalVars
	default:
		return nil
	}
}

func (c *testCtx) GetScoped(scope, key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.scopeMap(scope)
	if target == nil {
		return nil, false
	}
	v, ok := target[key]
	return v, ok
}

func (c *testCtx) SetScoped(scope, key string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.scopeMap(scope)
	if target == nil {
		return fmt.Errorf("cannot write to scope %q", scope)
	}
	if value == nil {
		delete(target, key)
		return nil
	}
	target[key] = value
	return nil
}

func (c *testCtx) ScopedVars(scope string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{}
	for k, v := range c.scopeMap(scope) {
		out[k] = v
	}
	return out
}

// Park waits on the test clock so fake-clock tests drive node waits directly.
func (c *testCtx) Park(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.parks = append(c.parks, d)
	c.mu.Unlock()
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-c.clock.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *testCtx) Emit(port string, msg *engine.Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emits = append(c.emits, emitCall{port: port, msg: msg})
}

func (c *testCtx) CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*engine.Msg, error) {
	return nil, errors.New("tool calls not implemented")
}

func (c *testCtx) Connection(id string) (any, error) {
	return nil, errors.New("connections not implemented")
}

// TestManualNodePassesThroughInitialMsg covers a test run seeded with a
// payload. The seeded message wins over the node's configured value outright —
// the point of typing a payload into the test-run dialog is to run the graph
// with that message rather than the one stored on the node.
func TestManualNodePassesThroughInitialMsg(t *testing.T) {
	ec := newTestCtx(`{"payload":{"a":1}}`, nil)
	initial := &engine.Msg{Payload: map[string]any{"x": true}}

	out, err := ManualNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{engine.TriggerInput: initial})
	require.NoError(t, err, "a seeded manual node must execute")
	assert.Same(t, initial, only(out, "out"),
		"the seeded message must pass straight through, or the test-run payload is ignored")
}

// TestManualNodeBuildsFromConfig covers the other half: with nothing seeded,
// the node emits the value stored on it. An unconfigured node emits an empty
// object rather than nil, so a downstream node reading a field gets a miss
// instead of a nil dereference.
func TestManualNodeBuildsFromConfig(t *testing.T) {
	out, err := ManualNode{}.Execute(context.Background(), newTestCtx(`{"payload":{"a":1}}`, nil), nil)
	require.NoError(t, err, "a configured manual node must execute with no input")

	payload, ok := only(out, "out").Payload.(map[string]any)
	require.True(t, ok, "a JSON object payload must decode as a map")
	assert.Equal(t, float64(1), payload["a"], "the stored value is what the node emits when nothing is seeded")

	out, err = ManualNode{}.Execute(context.Background(), newTestCtx("", nil), nil)
	require.NoError(t, err, "an unconfigured manual node must still execute")

	empty, ok := only(out, "out").Payload.(map[string]any)
	require.Truef(t, ok, "an unconfigured node must emit an object, not nil: %v", only(out, "out").Payload)
	assert.Empty(t, empty, "the empty object gives a downstream read a miss rather than a nil dereference")
}

// TestDelayNodeWaitsOnClock covers the delay node against a fake clock, which
// is the only way to assert the wait without spending it. The node must not
// return before time moves — a delay that returned early would be a delay that
// does nothing — and it must go through the injectable clock rather than
// time.After, or it could not be driven at all.
func TestDelayNodeWaitsOnClock(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"duration":"5s"}`, fc)
	in := &engine.Msg{Payload: "data"}

	type result struct {
		out map[string][]*engine.Msg
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := DelayNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
		done <- result{out, err}
	}()

	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second),
		"the delay must wait on the injectable clock, or it cannot be driven by anything but real time")
	assert.Equal(t, 5*time.Second, fc.AfterCalls()[0], "the configured duration is what the node waits")

	select {
	case <-done:
		require.FailNow(t, "the delay returned before the clock advanced, so it delayed nothing")
	default:
	}
	fc.Advance(5 * time.Second)

	res := <-done
	require.NoError(t, res.err, "the delay must complete once its time has passed")
	assert.True(t, sameMessage(only(res.out, "out"), in),
		"a delay only postpones a message; it must arrive intact: %v", res.out)
}

// TestDelayNodeHonorsCancellation covers cancelling a run parked in a delay.
// The clock is never advanced, so the node can only return if cancellation
// interrupts the wait — a delay of an hour must not keep a cancelled run alive
// for an hour.
func TestDelayNodeHonorsCancellation(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"duration":"1m"}`, fc)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := DelayNode{}.Execute(ctx, ec, map[string]*engine.Msg{"in": {}})
		done <- err
	}()
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the delay must be waiting before it can be cancelled out of")
	cancel()
	assert.ErrorIs(t, <-done, context.Canceled,
		"cancelling must interrupt the wait rather than let a long delay outlive the run")
}

// TestDelayNodeDurationParsing covers the duration field, which is free text an
// author types. A value Go can parse is waited; anything else fails the node
// onto its error branch rather than being read as zero, which would silently
// turn a delay into a passthrough.
func TestDelayNodeDurationParsing(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	go func() {
		_, _ = DelayNode{}.Execute(context.Background(), newTestCtx(`{"duration":"250ms"}`, fc), nil)
	}()
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "a parseable duration must reach the clock")
	assert.Equal(t, 250*time.Millisecond, fc.AfterCalls()[0], "the parsed duration is what is waited")
	fc.Advance(250 * time.Millisecond)

	_, err := DelayNode{}.Execute(context.Background(), newTestCtx(`{"duration":"nonsense"}`, nil), nil)
	assert.Error(t, err,
		"an unparseable duration must fail the node, not read as zero and turn the delay into a passthrough")
}

// TestDebugNodeEmitsDebugEvent covers the debug node, which is how an author
// sees what a message holds. It reports through the context and emits nothing:
// a debug node with an output would change what runs downstream depending on
// whether it was there, which is the opposite of what an inspection tool is
// for.
func TestDebugNodeEmitsDebugEvent(t *testing.T) {
	ec := newTestCtx(`{"note":"checkpoint"}`, nil)
	in := &engine.Msg{Payload: map[string]any{"k": "v"}}
	out, err := DebugNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "the debug node must never fail a run: it is an inspection tool")
	assert.Nil(t, out, "a debug node must emit nothing, or adding one changes what runs downstream")

	require.Len(t, ec.debugs, 1, "the message must be reported exactly once")
	assert.Equal(t, "checkpoint", ec.debugs[0].note, "the author's note is what labels the entry in the debug panel")

	payload, ok := ec.debugs[0].payload.(map[string]any)
	require.True(t, ok, "the reported payload must be the message's own")
	assert.Equal(t, "v", payload["k"], "the debug panel shows this payload, so it must be what arrived")
}

// TestSwitchNodeRouting covers the operators a switch rule can use and where it
// reads the value from. Exactly one branch is taken per message, which is what
// makes a switch a router rather than a fan-out: the rows check both that the
// expected port fired and that the other did not.
//
// The path forms are part of the contract too — a bare path is read from the
// payload, so an author who typed "room" gets payload.room rather than nothing.
func TestSwitchNodeRouting(t *testing.T) {
	msg := &engine.Msg{
		Payload: map[string]any{
			"temperature": float64(21),
			"room":        "kitchen",
			"nested":      map[string]any{"on": true},
		},
		Topic: "home/kitchen",
		Meta:  map[string]any{"source": "test"},
	}

	// One rule plus an otherwise branch: "match" is taken when the rule holds,
	// "else" when it does not.
	cases := []struct {
		name string
		cfg  string
		want string // expected taken port
	}{
		{"eq true", `{"property":"payload.room","rules":[{"id":"match","operator":"eq","value":"kitchen"}`, "match"},
		{"eq false", `{"property":"payload.room","rules":[{"id":"match","operator":"eq","value":"bedroom"}`, "else"},
		{"eq numeric", `{"property":"payload.temperature","rules":[{"id":"match","operator":"eq","value":"21"}`, "match"},
		{"neq", `{"property":"payload.room","rules":[{"id":"match","operator":"neq","value":"bedroom"}`, "match"},
		{"lt true", `{"property":"payload.temperature","rules":[{"id":"match","operator":"lt","value":"25"}`, "match"},
		{"lt false", `{"property":"payload.temperature","rules":[{"id":"match","operator":"lt","value":"20"}`, "else"},
		{"gt", `{"property":"payload.temperature","rules":[{"id":"match","operator":"gt","value":"20"}`, "match"},
		{"contains", `{"property":"topic","rules":[{"id":"match","operator":"contains","value":"kitchen"}`, "match"},
		{"exists true", `{"property":"payload.nested.on","rules":[{"id":"match","operator":"exists"}`, "match"},
		{"exists false", `{"property":"payload.missing","rules":[{"id":"match","operator":"exists"}`, "else"},
		{"meta path", `{"property":"meta.source","rules":[{"id":"match","operator":"eq","value":"test"}`, "match"},
		{"bare path", `{"property":"room","rules":[{"id":"match","operator":"eq","value":"kitchen"}`, "match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ec := newTestCtx(tc.cfg+`,{"id":"else","operator":"otherwise"}]}`, nil)
			out, err := SwitchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
			require.NoError(t, err, "a well-formed rule must evaluate")
			assert.Truef(t, sameMessage(only(out, tc.want), msg),
				"the message must leave on the %q branch, intact: %v", tc.want, out)

			other := "else"
			if tc.want == "else" {
				other = "match"
			}
			assert.Nilf(t, only(out, other),
				"only one branch may fire, or the switch fans out instead of routing: %v", out)
		})
	}
}

// TestSwitchNodeNonNumericComparison covers a numeric operator meeting text.
// Comparing "kitchen" with lt has no answer, and the node fails onto its error
// branch rather than picking one: treating it as false would route the message
// down a branch on the strength of a comparison that never happened.
func TestSwitchNodeNonNumericComparison(t *testing.T) {
	ec := newTestCtx(`{"property":"payload.room","rules":[{"id":"r1","operator":"lt","value":"5"}]}`, nil)
	msg := &engine.Msg{Payload: map[string]any{"room": "kitchen"}}

	_, err := SwitchNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
	assert.Error(t, err,
		"a numeric comparison against text has no answer, so it must fail rather than silently route as false")
}

// TestManualNodeIsAValueNode covers the manual node's nature: it holds a value
// and is not a trigger, so a graph built only from these never fires on its own.
// It stays pullable over a value edge, and the test-run API can still start a
// run from it when a graph has no trigger.
func TestManualNodeIsAValueNode(t *testing.T) {
	meta := ManualNode{}.Meta()

	_, isTrigger := any(ManualNode{}).(engine.TriggerType)
	assert.False(t, isTrigger, "a value node must not implement TriggerType, or enabling a flow would arm it")
	assert.False(t, meta.IsTrigger(), "a graph built only from value nodes must never fire on its own")
	assert.Empty(t, meta.Category, "a value node has no behavioral kind: it is neither trigger, annotation, nor port")
	assert.True(t, meta.Pure, "a value node must be pure, or a value edge is not allowed to pull it")
	assert.Empty(t, meta.Inputs, "a value node holds a value and takes no message: %v", meta.Inputs)
}

// only returns the single message a port emitted, or nil when the branch was
// not taken. Most nodes emit one message per port, so tests read through it
// instead of indexing the fan-out slice.
func only(out map[string][]*engine.Msg, port string) *engine.Msg {
	msgs := out[port]
	if len(msgs) == 0 {
		return nil
	}
	return msgs[0]
}

// sameMessage reports whether an emitted message carries what went in. Nodes
// that pass a message through rebuild its envelope rather than forwarding the
// pointer, because the envelope is merged from every input port (see inbound)
// and because the engine records the incoming message in run history and must
// not see it mutated. Identity is therefore the wrong question; what matters is
// that nothing was dropped on the way.
func sameMessage(got, want *engine.Msg) bool {
	if got == nil || want == nil {
		return got == want
	}
	if !reflect.DeepEqual(got.Payload, want.Payload) || got.Topic != want.Topic {
		return false
	}
	if len(got.Meta) != len(want.Meta) {
		return false
	}
	for k, v := range want.Meta {
		if !reflect.DeepEqual(got.Meta[k], v) {
			return false
		}
	}
	return reflect.DeepEqual(got.Group, want.Group)
}
