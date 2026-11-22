package agenthub_test

import (
	"context"
	"crypto/tls"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
)

// TestExecuteRealScriptOverWire proves the whole run path: the host executes an
// agent node, the node runs a real OS process, and it reaches back over the
// same connection for a host callback (the run variables). Both directions of
// the reverse gRPC transport are exercised at once.
func TestExecuteRealScriptOverWire(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test node shells out with /bin/sh")
	}
	setupDB(t)

	registry := engine.NewRegistry()
	host := pluginhost.NewHost(registry)
	mgr := pluginhost.NewManager(host, zap.NewNop())

	hub, err := agenthub.New(mgr, zap.NewNop(), "127.0.0.1:0", 15*time.Minute, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))
	defer hub.Stop()

	raw, _, err := models.NewEnrollmentToken("runner", "", nil, 15*time.Minute)
	require.NoError(t, err)
	token, err := ca.EncodeJoin(hub.Bundle().PEM(), raw)
	require.NoError(t, err)
	join, err := ca.ParseJoin(token)
	require.NoError(t, err)
	keyPEM, err := ca.GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := ca.CreateCSR(keyPEM, "runner-host")
	require.NoError(t, err)
	resp := enroll(t, hub.Addr(), join, agentwire.EnrollRequest{
		Token: join.Secret, CSR: string(csrPEM), Hostname: "runner-host", OS: "linux", Arch: "amd64",
	})
	require.Empty(t, resp.Error)

	clientCert, err := tls.X509KeyPair([]byte(resp.Cert), keyPEM)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sdk.ServeAgent(ctx, sdk.AgentTransport{
			ServerAddr: hub.Addr(),
			TLSConfig: func() (*tls.Config, error) {
				return &tls.Config{
					MinVersion:   tls.VersionTLS13,
					Certificates: []tls.Certificate{clientCert},
					RootCAs:      join.Pool(),
					ServerName:   ca.ServerName,
				}, nil
			},
			Logger: zap.NewNop(),
		}, runnerPlugin{})
	}()

	want := pluginhost.AgentTypePrefix(resp.AgentID) + "runner"
	var node engine.NodeType
	require.Eventually(t, func() bool {
		n, ok := registry.Get(want)
		if ok {
			node = n
		}
		return ok
	}, 5*time.Second, 20*time.Millisecond)

	// Execute the node the way the engine does: a config the agent reads, and a
	// fake ExecContext whose Vars() the node fetches back over the callback
	// channel.
	ec := &fakeCtx{vars: map[string]any{"greeting": "hi"}}
	out, err := node.Execute(context.Background(), ec, nil)
	require.NoError(t, err, "executing the agent node must run the real script and return")

	msgs := out["out"]
	require.Len(t, msgs, 1)
	result, ok := msgs[0].Payload.(map[string]any)
	require.Truef(t, ok, "payload is %T, want the object the node returned", msgs[0].Payload)
	assert.Equal(t, "hello from the agent", result["stdout"], "the real process's stdout must cross back")
	assert.Equal(t, "hi", result["greeting"], "a host callback (Vars) must resolve over the same connection")
}

// runnerPlugin registers a node that runs a real shell command and reads a run
// variable, so executing it proves both directions of the transport.
type runnerPlugin struct{}

func (runnerPlugin) Manifest() plugin.Manifest { return plugin.Manifest{Name: "agent"} }
func (runnerPlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(runnerNode{})
	return nil
}

type runnerNode struct{}

func (runnerNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:    "runner",
		Label:   "Runner",
		Outputs: []engine.PortSpec{{ID: "out", Type: flow.PortAny}},
	}
}

func (runnerNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf 'hello from the agent'")
	stdout, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	greeting, _ := ec.Vars()["greeting"].(string)
	return map[string][]*engine.Msg{
		"out": {{Payload: map[string]any{"stdout": strings.TrimSpace(string(stdout)), "greeting": greeting}}},
	}, nil
}

// fakeCtx is a minimal ExecContext for driving a node in a test. Only the
// methods the runner touches carry behavior; the rest are inert.
type fakeCtx struct {
	vars map[string]any
}

func (c *fakeCtx) Config(out any) error                                { return nil }
func (c *fakeCtx) Logger() *zap.Logger                                 { return zap.NewNop() }
func (c *fakeCtx) Clock() engine.Clock                                 { return engine.RealClock{} }
func (c *fakeCtx) Eval(string, *engine.Msg) (any, error)               { return nil, nil }
func (c *fakeCtx) Interpolate(s string, _ *engine.Msg) (string, error) { return s, nil }
func (c *fakeCtx) NodeID() string                                      { return "n1" }
func (c *fakeCtx) Ports() flow.PortSet                                 { return flow.PortSet{} }
func (c *fakeCtx) Wired(string) bool                                   { return true }
func (c *fakeCtx) Debug(any, string)                                   {}
func (c *fakeCtx) GetState(string) (any, bool)                         { return nil, false }
func (c *fakeCtx) SetState(string, any)                                {}
func (c *fakeCtx) GetVar(k string) (any, bool)                         { v, ok := c.vars[k]; return v, ok }
func (c *fakeCtx) SetVar(string, any)                                  {}
func (c *fakeCtx) Vars() map[string]any                                { return c.vars }
func (c *fakeCtx) GetScoped(string, string) (any, bool)                { return nil, false }
func (c *fakeCtx) SetScoped(string, string, any) error                 { return nil }
func (c *fakeCtx) ScopedVars(string) map[string]any                    { return map[string]any{} }
func (c *fakeCtx) NodeStore() engine.NodeStore                         { return engine.NewMemNodeStore() }
func (c *fakeCtx) DurableStore() engine.NodeStore                      { return engine.NewMemDurableStore() }
func (c *fakeCtx) Park(context.Context, time.Duration) error           { return nil }
func (c *fakeCtx) Emit(string, *engine.Msg)                            {}
func (c *fakeCtx) CallTool(context.Context, string, map[string]any, time.Duration) (*engine.Msg, error) {
	return nil, errors.New("not supported in tests")
}
func (c *fakeCtx) Connection(string) (any, error) { return nil, nil }
