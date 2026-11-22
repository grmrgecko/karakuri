package openhabnodes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab/openhabtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testCtx is the ExecContext the nodes run under: config, the one
// connection, and interpolation. Anything else panics through the nil embed.
type testCtx struct {
	engine.ExecContext
	cfg    string
	driver *openhab.Driver
}

func (c testCtx) Config(out any) error           { return json.Unmarshal([]byte(c.cfg), out) }
func (c testCtx) Logger() *zap.Logger            { return zap.NewNop() }
func (c testCtx) Connection(string) (any, error) { return c.driver, nil }
func (c testCtx) Interpolate(s string, msg *engine.Msg) (string, error) {
	return expr.Interpolate(s, expr.BuildEnv(expr.Env{Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta}))
}

// startHouse runs a fake openHAB house and a connected driver.
func startHouse(t *testing.T) (*openhabtest.Server, *openhab.Driver) {
	t.Helper()
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()
	raw, _ := json.Marshal(openhab.Config{BaseURL: srv.URL})
	built, err := openhab.New(integrations.ConnModel{ID: uuid.New(), Kind: openhab.Kind, Config: raw}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*openhab.Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	deadline := time.Now().Add(5 * time.Second)
	for !d.Connected() {
		require.True(t, time.Now().Before(deadline), "the driver never connected")
		time.Sleep(10 * time.Millisecond)
	}
	return srv, d
}

// arm starts a trigger and returns what it emits.
func arm(t *testing.T, d *openhab.Driver, cfg string) chan *engine.Msg {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	emits := make(chan *engine.Msg, 8)
	go func() { _ = ItemChangedNode{}.Start(ctx, testCtx{cfg: cfg, driver: d}, func(m *engine.Msg) { emits <- m }) }()
	time.Sleep(100 * time.Millisecond)
	return emits
}

func next(t *testing.T, emits chan *engine.Msg) map[string]any {
	t.Helper()
	select {
	case m := <-emits:
		return m.Payload.(map[string]any)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the trigger did not fire")
		return nil
	}
}

func quiet(t *testing.T, emits chan *engine.Msg, why string) {
	t.Helper()
	select {
	case m := <-emits:
		assert.Fail(t, why, "%v", m.Payload)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestItemChanged covers triggers by item, by group member, and by location
// narrowed by type, with from and to filters, and the caught-up mark.
func TestItemChanged(t *testing.T) {
	srv, d := startHouse(t)
	byItem := arm(t, d, `{"connection":"c","items":"KitchenSwitch","to":"ON"}`)
	byGroup := arm(t, d, `{"connection":"c","groups":"gLights"}`)
	byPlace := arm(t, d, `{"connection":"c","location":"gGround","item_type":"Contact","from":"CLOSED"}`)

	srv.SetState("KitchenSwitch", "ON")
	got := next(t, byItem)
	assert.Equal(t, "KitchenSwitch", got["item"])
	assert.Equal(t, "OFF", got["old"])
	assert.Equal(t, "Kitchen", got["location"])
	assert.Equal(t, "KitchenSwitch", next(t, byGroup)["item"], "a member of the group fires it")
	quiet(t, byPlace, "a switch is not a contact")

	srv.SetState("LivingDimmer", "40")
	assert.Equal(t, "LivingDimmer", next(t, byGroup)["item"])
	quiet(t, byItem, "another item does not fire an item trigger")

	srv.SetState("FrontDoor", "OPEN")
	assert.Equal(t, "FrontDoor", next(t, byPlace)["item"], "a contact on the ground floor fires the location trigger")
	srv.SetState("KitchenSwitch", "OFF")
	quiet(t, byItem, "to ON does not fire for OFF")

	ignoring := arm(t, d, `{"connection":"c","items":"Volume","ignore_caught_up":true}`)
	reporting := arm(t, d, `{"connection":"c","items":"Volume"}`)
	srv.Drop()
	srv.SetStateQuietly("Volume", "25")
	got = next(t, reporting)
	assert.Equal(t, true, got["caught_up"])
	quiet(t, ignoring, "caught-up changes are ignored when asked")
}

// TestSendAndGet covers sending a templated command, openHAB's refusal as a
// node failure, and reading an item.
func TestSendAndGet(t *testing.T) {
	srv, d := startHouse(t)
	ctx := context.Background()
	in := map[string]*engine.Msg{"in": {Payload: map[string]any{"level": 30}}}

	_, err := SendCommandNode{}.Execute(ctx, testCtx{cfg: `{"connection":"c","item":"gLights","command":"OFF"}`, driver: d}, in)
	require.NoError(t, err)
	_, err = SendCommandNode{}.Execute(ctx, testCtx{cfg: `{"connection":"c","item":"Volume","command":"{{ payload.level }}"}`, driver: d}, in)
	require.NoError(t, err)
	assert.Equal(t, []openhabtest.Command{{Item: "gLights", Command: "OFF"}, {Item: "Volume", Command: "30"}}, srv.Commands())

	srv.RefuseCommands("FrontDoor")
	_, err = SendCommandNode{}.Execute(ctx, testCtx{cfg: `{"connection":"c","item":"FrontDoor","command":"OPEN"}`, driver: d}, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot parse command OPEN")

	out, err := GetItemNode{}.Execute(ctx, testCtx{cfg: `{"connection":"c","item":"KitchenTemp"}`, driver: d}, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"item": "KitchenTemp", "state": "21.5 °C", "type": "Number:Temperature",
		"label": "Kitchen temperature", "location": "Kitchen"}, out["out"][0].Payload)
	_, err = GetItemNode{}.Execute(ctx, testCtx{cfg: `{"connection":"c","item":"Nope"}`, driver: d}, nil)
	assert.Error(t, err)
}
