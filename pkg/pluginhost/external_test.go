package pluginhost_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	pb "github.com/grmrgecko/karakuri/pkg/plugin/proto"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildExample compiles the fixture plugin once per test binary into a
// plugins-layout directory and returns that directory's parent. The fixture
// lives in testdata rather than plugins/ because it exercises the boundary
// rather than shipping with the product.
var buildExample = sync.OnceValues(func() (string, error) {
	root, err := os.MkdirTemp("", "karakuri-plugins-*")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "example")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "example"), "./exampleplugin")
	cmd.Dir = "testdata"
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", &buildError{output: string(out), err: err}
	}
	manifest := "name: \"example\"\nversion: \"0.1.0\"\nentryPoint: \"example\"\n"
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		return "", err
	}
	return root, nil
})

type buildError struct {
	output string
	err    error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.output }

// loadExample brings the example plugin up against a fresh host and returns
// the registry it registered into.
func loadExample(t *testing.T) (*engine.Registry, *pluginhost.Host, *pluginhost.Manager) {
	t.Helper()
	root, err := buildExample()
	require.NoError(t, err, "the fixture plugin must compile before the boundary can be exercised")

	reg := engine.NewRegistry()
	host := pluginhost.NewHost(reg)
	mgr := pluginhost.NewManager(host, zap.NewNop())
	require.NoError(t, mgr.LoadAll([]string{root}), "the built fixture must be discovered and started")
	t.Cleanup(mgr.Stop)
	return reg, host, mgr
}

// TestExternalPluginRegistersAndExecutes covers the whole boundary: the
// subprocess loads, its nodes register like built-ins, Execute crosses the
// wire, and node-store state round-trips through the callback channel with
// CAS semantics intact across calls.
func TestExternalPluginRegistersAndExecutes(t *testing.T) {
	reg, host, _ := loadExample(t)

	plugins := host.Plugins()
	require.Len(t, plugins, 1, "the fixture directory holds one plugin: %+v", plugins)
	assert.Equal(t, "example", plugins[0].Name, "the plugin is identified by its manifest name")
	assert.False(t, plugins[0].Builtin, "an external plugin must not be reported as compiled in, or it could never be unloaded")

	counter, ok := reg.Get("example-counter")
	require.True(t, ok, "a remote node type must land in the same registry a built-in does")
	assert.Equal(t, flow.InvocationStateUnknown, counter.Meta().InvocationState,
		"an external node that does not declare store use must not inherit the built-in stateless default")
	_, isTrigger := counter.(engine.TriggerType)
	assert.False(t, isTrigger,
		"the proxy must only implement TriggerType for nodes that really are triggers, or enabling a flow would arm this one")

	interval, ok := reg.Get("example-interval")
	require.True(t, ok, "the plugin's trigger node must register too")
	_, isTrigger = interval.(engine.TriggerType)
	assert.True(t, isTrigger, "a remote trigger must proxy as one, or it can never be armed")

	meta := interval.Meta()
	assert.True(t, meta.IsTrigger(), "the metadata must cross the wire intact")
	assert.Equal(t, "Example", meta.Folder, "a plugin brings its own palette folder, which is what puts its nodes together")

	// Two calls, because the count comes from the node store: the second
	// proves state written through the callback channel survived to the next
	// Execute rather than living in the subprocess.
	ec := newFakeCtx()
	writes := &setCounter{NodeStore: ec.store}
	ec.store = writes
	for want := 1.0; want <= 2; want++ {
		out, err := counter.Execute(context.Background(), ec, map[string]*engine.Msg{
			"in": {Payload: "hello", Topic: "greetings"},
		})
		require.NoError(t, err, "executing a remote node must cross the wire and return")

		msgs := out["out"]
		require.Lenf(t, msgs, 1, "outputs = %+v, want one message on out", out)

		payload, ok := msgs[0].Payload.(map[string]any)
		require.Truef(t, ok, "payload is %T, want the object the plugin returned", msgs[0].Payload)
		assert.Equalf(t, want, payload["count"],
			"the node store lives host-side, so the count must keep climbing across calls")
		assert.Equal(t, "hello", payload["value"], "the input payload must reach the plugin intact")
		assert.Equal(t, "greetings", msgs[0].Topic, "the whole message crosses the wire, not just the payload")
		assert.Equal(t, true, payload["wired_out"],
			"a wired output must read wired plugin-side, carried by the request's wired_outputs")
		assert.Equal(t, false, payload["wired_aux"],
			"an unwired output must read unwired plugin-side, not default open")
	}
	assert.Zero(t, writes.sets.Load(),
		"an update that changes the stored map in place must commit by compare-and-swap, not the unguarded fallback write")
}

// setCounter counts plain Set calls on a store. The remote Update only calls
// Set after it gives up on compare-and-swap.
type setCounter struct {
	engine.NodeStore
	sets atomic.Int32
}

func (s *setCounter) Set(key string, value any) {
	s.sets.Add(1)
	s.NodeStore.Set(key, value)
}

// TestExternalTriggerStreamsOnHostClock covers the trigger path: Start runs in
// the plugin process, waits on the host's injectable clock over the callback
// channel, and emitted messages stream back to the host.
func TestExternalTriggerStreamsOnHostClock(t *testing.T) {
	reg, _, _ := loadExample(t)

	nt, ok := reg.Get("example-interval")
	require.True(t, ok, "the trigger node must be registered")
	trigger, ok := nt.(engine.TriggerType)
	require.True(t, ok, "the node must proxy as a trigger to be armed")

	clock := engine.NewFakeClock(time.Now())
	ec := newFakeCtx()
	ec.clock = clock

	ctx, cancel := context.WithCancel(context.Background())
	emitted := make(chan *engine.Msg, 4)
	done := make(chan error, 1)
	go func() {
		done <- trigger.Start(ctx, ec, func(m *engine.Msg) { emitted <- m })
	}()

	// The plugin's clock wait arrives over the wire; give it a moment to
	// register with the fake clock before firing it.
	deadline := time.Now().Add(5 * time.Second)
	for clock.PendingWaiters() == 0 {
		if time.Now().After(deadline) {
			require.FailNow(t,
				"the plugin never waited on the host's clock, so a fake clock cannot drive a plugin trigger at all")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The trigger runs its default 60s interval (the fake context supplies no
	// config), so jump past it.
	clock.Advance(61 * time.Second)

	select {
	case m := <-emitted:
		payload, ok := m.Payload.(map[string]any)
		require.Truef(t, ok, "payload is %T, want the object the plugin emitted", m.Payload)
		assert.Equal(t, 1.0, payload["tick"],
			"a message emitted inside the plugin process must stream back to the host")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the clock moved past the interval but nothing was emitted")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled, "disarming a remote trigger must be a clean stop")
		}
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Start did not return after cancellation, so disabling a flow would hang")
	}
}

// TestWireMsgRoundTrip covers the message codec's one hard case. Everything
// else on the wire survives a JSON round trip, but bytes do not: JSON has no
// byte type, so a payload encoded and decoded naively comes back as the base64
// string it was rendered as. A plugin reading a file or an MQTT payload would
// then hand the host text where its port declared binary.
func TestWireMsgRoundTrip(t *testing.T) {
	in := &engine.Msg{
		ID:      "m1",
		Payload: []byte{0x00, 0x01, 0xff},
		Topic:   "bytes",
		Meta:    map[string]any{"path": "/tmp/x"},
		Group:   []engine.GroupFrame{{ID: "g", Index: 1, Count: 3}},
	}
	w, err := plugin.MsgToWire(in)
	require.NoError(t, err, "a message with a binary payload must encode for the wire")
	out, err := plugin.MsgFromWire(w)
	require.NoError(t, err, "the encoded message must decode again")

	b, ok := out.Payload.([]byte)
	require.Truef(t, ok, "payload is %T, want real bytes rather than the base64 text JSON would leave", out.Payload)
	assert.Equal(t, []byte{0x00, 0x01, 0xff}, b, "the bytes must survive exactly, including the ones no string can carry")

	assert.Equal(t, "/tmp/x", out.Meta["path"], "metadata must cross the wire with the payload")
	require.Lenf(t, out.Group, 1, "the group stack must cross the wire: %+v", out.Group)
	assert.Equal(t, 3, out.Group[0].Count,
		"a fan-out frame must survive, or a plugin node inside a split breaks the join after it")

	_ = pb.WireMsg{} // The generated types stay importable for helpers.
}

// fakeCtx is the engine's real ExecContext interface with the minimum behind
// it: a real in-memory node store and a settable clock, so the two capabilities
// these tests actually exercise across the wire are the production ones, and
// everything else is inert.
type fakeCtx struct {
	store   engine.NodeStore
	durable engine.NodeStore
	clock   engine.Clock
	state   map[string]any
	vars    map[string]any
}

func newFakeCtx() *fakeCtx {
	return &fakeCtx{
		store:   engine.NewMemNodeStore(),
		durable: engine.NewMemDurableStore(),
		clock:   engine.RealClock{},
		state:   map[string]any{},
		vars:    map[string]any{},
	}
}

func (c *fakeCtx) Config(out any) error { return nil }
func (c *fakeCtx) Logger() *zap.Logger  { return zap.NewNop() }
func (c *fakeCtx) Clock() engine.Clock  { return c.clock }
func (c *fakeCtx) NodeID() string       { return "n1" }

// Ports declares two outputs with only one wired, so the request builder has
// a per-port wiring answer to stamp and the plugin side can be seen reading it.
func (c *fakeCtx) Ports() flow.PortSet {
	return flow.PortSet{
		Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortObject}, {ID: "aux", Type: flow.PortAny}},
		Known:   true,
	}
}
func (c *fakeCtx) Wired(port string) bool                              { return port == "out" }
func (c *fakeCtx) Debug(payload any, n string)                         {}
func (c *fakeCtx) Eval(src string, m *engine.Msg) (any, error)         { return nil, nil }
func (c *fakeCtx) Interpolate(s string, m *engine.Msg) (string, error) { return s, nil }
func (c *fakeCtx) GetState(key string) (any, bool) {
	v, ok := c.state[key]
	return v, ok
}
func (c *fakeCtx) SetState(key string, value any) { c.state[key] = value }
func (c *fakeCtx) GetVar(key string) (any, bool) {
	v, ok := c.vars[key]
	return v, ok
}
func (c *fakeCtx) SetVar(key string, value any)                 { c.vars[key] = value }
func (c *fakeCtx) Vars() map[string]any                         { return c.vars }
func (c *fakeCtx) GetScoped(scope, key string) (any, bool)      { return nil, false }
func (c *fakeCtx) SetScoped(scope, key string, value any) error { return nil }
func (c *fakeCtx) ScopedVars(scope string) map[string]any       { return map[string]any{} }
func (c *fakeCtx) NodeStore() engine.NodeStore                  { return c.store }
func (c *fakeCtx) DurableStore() engine.NodeStore               { return c.durable }
func (c *fakeCtx) Park(ctx context.Context, d time.Duration) error {
	select {
	case <-c.clock.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *fakeCtx) Emit(port string, msg *engine.Msg) {}
func (c *fakeCtx) CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*engine.Msg, error) {
	return nil, errors.New("not supported in tests")
}
func (c *fakeCtx) Connection(id string) (any, error) { return nil, nil }

// skipRecorder is a fake trigger context that records skipped events.
type skipRecorder struct {
	*fakeCtx
	mu      sync.Mutex
	reasons []string
	msgs    []*engine.Msg
}

func (s *skipRecorder) Skipped(msg *engine.Msg, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasons = append(s.reasons, reason)
	s.msgs = append(s.msgs, msg)
}

// TestExternalProbeAndSkippedCrossTheBoundary covers two host additions over
// the real process boundary: a plugin connection kind's probe detail reaches
// the host's kind registry, and a plugin trigger's skipped event reaches the
// trigger context it was armed with.
func TestExternalProbeAndSkippedCrossTheBoundary(t *testing.T) {
	reg, _, _ := loadExample(t)

	kind, ok := integrations.KindFor("example-probe")
	require.True(t, ok, "the plugin's connection kind registers with the host")
	require.NotNil(t, kind.Probe, "a testable plugin kind answers through Probe")
	detail, err := kind.Probe(context.Background(), uuid.New(), []byte(`{"name":"family"}`))
	require.NoError(t, err)
	assert.Equal(t, "found family", detail, "the plugin's detail crosses the wire")
	detail, err = kind.Probe(context.Background(), uuid.New(), []byte(`{}`))
	assert.EqualError(t, err, "name is required")
	assert.Equal(t, "nothing to probe", detail, "a failing probe still carries its detail")

	nt, ok := reg.Get("example-skip")
	require.True(t, ok)
	rec := &skipRecorder{fakeCtx: newFakeCtx()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = nt.(engine.TriggerType).Start(ctx, rec, func(*engine.Msg) {}) }()
	require.Eventually(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.reasons) == 1
	}, 5*time.Second, 10*time.Millisecond, "the skipped event reaches the host's trigger context")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, "missed; dropped", rec.reasons[0])
	assert.Equal(t, map[string]any{"event": "standup"}, rec.msgs[0].Payload)
}
