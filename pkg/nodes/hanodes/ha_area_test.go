package hanodes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// driverEC serves a real driver connected to a fake Home Assistant, so the
// nodes read the registries the driver loaded.
type driverEC struct {
	*fakeEC
	driver *homeassistant.Driver
}

func (e *driverEC) Connection(string) (any, error) { return e.driver, nil }

// startHouse runs a driver against a fake house with its states set and
// waits for the registries to load.
func startHouse(t *testing.T) (*hatest.Server, *homeassistant.Driver) {
	t.Helper()
	srv := hatest.New("area-token")
	t.Cleanup(srv.Close)
	srv.SetHouse()
	srv.SetState("light.kitchen_ceiling", "off", nil)
	srv.SetState("light.bedroom_lamp", "off", nil)
	srv.SetState("media_player.kitchen_speaker", "idle", nil)
	srv.SetState("binary_sensor.hallway_motion", "off", map[string]any{"device_class": "motion"})
	srv.SetState("sun.sun", "above_horizon", nil)

	cfg, _ := json.Marshal(homeassistant.Config{BaseURL: srv.URL, Token: "area-token"})
	built, err := homeassistant.New(integrations.ConnModel{ID: uuid.New(), Kind: homeassistant.Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*homeassistant.Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	deadline := time.Now().Add(5 * time.Second)
	for !d.Registry().Loaded(homeassistant.RegistryEntity) {
		require.True(t, time.Now().Before(deadline), "the registries never loaded")
		time.Sleep(10 * time.Millisecond)
	}
	return srv, d
}

// change pushes a state change from old to new.
func change(srv *hatest.Server, entityID, old, new string, attrs map[string]any) {
	srv.PushStateChanged(entityID, hatest.StateObj(entityID, old, attrs), hatest.StateObj(entityID, new, attrs))
}

// armed starts a state trigger and returns its emitted messages.
func armed(t *testing.T, ec engine.ExecContext) chan *engine.Msg {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	emits := make(chan *engine.Msg, 8)
	go func() { _ = StateNode{}.Start(ctx, ec, func(m *engine.Msg) { emits <- m }) }()
	time.Sleep(200 * time.Millisecond)
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
	case <-time.After(200 * time.Millisecond):
	}
}

// TestStateTriggerOnArea covers a trigger on an area and domain: a member
// fires with its area, other entities do not, and an entity moved into the
// area by a registry update fires on its next change without re-arming.
func TestStateTriggerOnArea(t *testing.T) {
	srv, d := startHouse(t)
	ec := &driverEC{fakeEC: &fakeEC{cfg: `{"connection":"c","area_id":"kitchen","domain":"light"}`}, driver: d}
	emits := armed(t, ec)

	change(srv, "light.kitchen_ceiling", "off", "on", nil)
	got := next(t, emits)
	assert.Equal(t, "light.kitchen_ceiling", got["entity_id"])
	assert.Equal(t, "kitchen", got["area_id"])
	assert.Equal(t, "Kitchen", got["area"])

	change(srv, "light.bedroom_lamp", "off", "on", nil)
	change(srv, "media_player.kitchen_speaker", "idle", "playing", nil)
	quiet(t, emits, "a light elsewhere or a speaker in the kitchen fired a kitchen light trigger")

	srv.SetRegistry("entity", []map[string]any{
		{"ei": "light.kitchen_ceiling", "ai": "kitchen"},
		{"ei": "light.bedroom_lamp", "ai": "kitchen"},
	})
	srv.PushRegistryUpdate("entity", map[string]any{"action": "update", "entity_id": "light.bedroom_lamp"})
	deadline := time.Now().Add(5 * time.Second)
	for d.Registry().Describe("light.bedroom_lamp").AreaID != "kitchen" {
		require.True(t, time.Now().Before(deadline), "the move never reached the registry")
		time.Sleep(20 * time.Millisecond)
	}
	change(srv, "light.bedroom_lamp", "on", "off", nil)
	assert.Equal(t, "light.bedroom_lamp", next(t, emits)["entity_id"], "a moved entity counts without re-arming")
}

// TestStateTriggerOnFloorAndLabel covers floor and label targets narrowed by
// device class, and an entity listed alongside a group.
func TestStateTriggerOnFloorAndLabel(t *testing.T) {
	srv, d := startHouse(t)

	floor := armed(t, &driverEC{fakeEC: &fakeEC{cfg: `{"connection":"c","floor_id":"upstairs"}`}, driver: d})
	labelled := armed(t, &driverEC{fakeEC: &fakeEC{
		cfg: `{"connection":"c","label_id":"night","device_class":"motion","entity_id":"sun.sun"}`}, driver: d})

	change(srv, "light.bedroom_lamp", "off", "on", nil)
	assert.Equal(t, "light.bedroom_lamp", next(t, floor)["entity_id"])
	quiet(t, labelled, "a labelled lamp is not a motion sensor")

	change(srv, "binary_sensor.hallway_motion", "off", "on", map[string]any{"device_class": "motion"})
	assert.Equal(t, "binary_sensor.hallway_motion", next(t, labelled)["entity_id"], "the device's label counts")
	change(srv, "sun.sun", "above_horizon", "below_horizon", nil)
	assert.Equal(t, "sun.sun", next(t, labelled)["entity_id"], "a listed entity still fires beside a group")
	quiet(t, floor, "the hallway and the sun are not upstairs")
}

// TestGetStateGroup covers reading an area: every matching state, counted.
func TestGetStateGroup(t *testing.T) {
	_, d := startHouse(t)
	ec := &driverEC{fakeEC: &fakeEC{cfg: `{"connection":"c","area_id":"kitchen"}`}, driver: d}
	out, err := GetStateNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err)
	payload := out["out"][0].Payload.(map[string]any)
	assert.Equal(t, 2, payload["count"], "the ceiling light and the speaker; the hidden strip has no state")
	ids := []string{}
	for _, s := range payload["states"].([]any) {
		ids = append(ids, s.(map[string]any)["entity_id"].(string))
	}
	assert.ElementsMatch(t, []string{"light.kitchen_ceiling", "media_player.kitchen_speaker"}, ids)

	ec = &driverEC{fakeEC: &fakeEC{cfg: `{"connection":"c"}`}, driver: d}
	_, err = GetStateNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err, "a get state with nothing to read fails")
}

// TestCallServiceGroupTarget covers the call service targets: an area that
// holds the domain is passed through, and one that holds none of it fails
// with the area named before anything is sent.
func TestCallServiceGroupTarget(t *testing.T) {
	srv, d := startHouse(t)
	call := func(cfg string) error {
		ec := &driverEC{fakeEC: &fakeEC{cfg: cfg}, driver: d}
		_, err := CallServiceNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {}})
		return err
	}

	require.NoError(t, call(`{"connection":"c","domain":"light","service":"turn_off","area_id":"kitchen","floor_id":"upstairs"}`))
	calls := srv.ServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []any{"kitchen"}, calls[0].Target["area_id"])
	assert.Equal(t, []any{"upstairs"}, calls[0].Target["floor_id"])

	err := call(`{"connection":"c","domain":"light","service":"turn_off","area_id":"hallway"}`)
	require.Error(t, err)
	assert.Equal(t, "no light entities in Hallway", err.Error())
	assert.Len(t, srv.ServiceCalls(), 1, "a call that reaches nothing is not sent")

	require.NoError(t, call(`{"connection":"c","domain":"homeassistant","service":"turn_off","area_id":"hallway"}`),
		"a cross-domain service is not narrowed")
}
