package esphomenodes

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome/esphometest"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testCtx is the ExecContext the nodes run under: config, the one
// connection, and interpolation. Anything else panics through the nil embed.
type testCtx struct {
	engine.ExecContext
	cfg    string
	driver *esphome.Driver
}

func (c testCtx) Config(out any) error           { return json.Unmarshal([]byte(c.cfg), out) }
func (c testCtx) Logger() *zap.Logger            { return zap.NewNop() }
func (c testCtx) Connection(string) (any, error) { return c.driver, nil }
func (c testCtx) Eval(src string, msg *engine.Msg) (any, error) {
	return expr.Eval(src, expr.BuildEnv(expr.Env{Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta}))
}
func (c testCtx) Interpolate(s string, msg *engine.Msg) (string, error) {
	return expr.Interpolate(s, expr.BuildEnv(expr.Env{Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta}))
}

// startNode runs a simulated node with a beep action and a connected driver.
func startNode(t *testing.T) (*esphometest.Node, *esphome.Driver) {
	t.Helper()
	node := &esphometest.Node{Name: "garage", Services: []*api.ListEntitiesServicesResponse{{Name: "beep", Key: 7,
		Args: []*api.ListEntitiesServicesArgument{{Name: "times", Type: api.ServiceArgType_SERVICE_ARG_TYPE_INT}}}}}
	addr, err := node.Start()
	require.NoError(t, err)
	t.Cleanup(node.Close)
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)
	raw, _ := json.Marshal(esphome.Config{Host: host, Port: port})
	built, err := esphome.New(integrations.ConnModel{ID: uuid.New(), Name: "garage", Kind: esphome.Kind, Config: raw}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*esphome.Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	deadline := time.Now().Add(5 * time.Second)
	for d.Status().State != integrations.StateConnected {
		require.True(t, time.Now().Before(deadline))
		time.Sleep(10 * time.Millisecond)
	}
	return node, d
}

// TestActionNode covers calling a node's action with arguments taken from
// the message, and refusing an argument of the wrong type.
func TestActionNode(t *testing.T) {
	node, d := startNode(t)
	in := map[string]*engine.Msg{"in": {Payload: map[string]any{"count": 3}}}

	out, err := ActionNode{}.Execute(context.Background(),
		testCtx{cfg: `{"connection":"x","action":"beep","args":{"times":"{{ payload.count }}"}}`, driver: d}, in)
	require.NoError(t, err)
	assert.Len(t, out["out"], 1, "the message passes through")
	select {
	case cmd := <-node.Commands:
		exec, ok := cmd.(*api.ExecuteServiceRequest)
		require.True(t, ok)
		assert.Equal(t, int32(3), exec.Args[0].Int_)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the action never reached the node")
	}

	_, err = ActionNode{}.Execute(context.Background(),
		testCtx{cfg: `{"connection":"x","action":"beep","args":{"times":"loud"}}`, driver: d}, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `argument "times"`)
}

// TestNodeActionTrigger covers the trigger on a node's action calls, with the
// action filter applied.
func TestNodeActionTrigger(t *testing.T) {
	node, d := startNode(t)
	emits := make(chan *engine.Msg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = NodeActionTrigger{}.Start(ctx, testCtx{cfg: `{"connection":"x","action":"notify.phone"}`, driver: d},
			func(m *engine.Msg) { emits <- m })
	}()
	time.Sleep(100 * time.Millisecond)

	node.Send(&api.HomeassistantActionRequest{Service: "light.turn_on"})
	node.Send(&api.HomeassistantActionRequest{Service: "notify.phone",
		Data: []*api.HomeassistantServiceMap{{Key: "message", Value: "Garage open"}}})
	select {
	case m := <-emits:
		payload := m.Payload.(map[string]any)
		assert.Equal(t, "notify.phone", payload["action"])
		assert.Equal(t, map[string]any{"message": "Garage open"}, payload["data"])
		assert.Equal(t, "garage", payload["node"])
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the trigger never fired")
	}
	select {
	case extra := <-emits:
		assert.Fail(t, "the filter let another action through", "%v", extra.Payload)
	default:
	}
}
