package homeassistant

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startRegistryDriver starts a driver against a fake house and waits for its
// registries to load.
func startRegistryDriver(t *testing.T, srv *hatest.Server) *Driver {
	t.Helper()
	d := newTestDriver(t, srv.URL, srv.Token)
	d.regDebounce = 20 * time.Millisecond
	require.NoError(t, d.Start(t.Context()))
	t.Cleanup(d.Stop)
	waitFor(t, 5*time.Second, func() bool { return d.Registry().Loaded(RegistryEntity) },
		"the registries never loaded")
	return d
}

// TestRegistryDescribe covers where entities sit: an entity's own area, its
// device's area when it has none, floors through the area, device labels
// counted as the entity's, and disabled devices left out.
func TestRegistryDescribe(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetHouse()
	r := startRegistryDriver(t, srv).Registry()

	for _, name := range registryNames {
		assert.Equal(t, "loaded", r.Status[name], name)
	}
	assert.Equal(t, []string{"ground", "upstairs"}, []string{r.Floors[0].ID, r.Floors[1].ID}, "floors order by level")

	speaker := r.Describe("media_player.kitchen_speaker")
	assert.Equal(t, "kitchen", speaker.AreaID, "an entity without an area takes its device's")
	assert.Equal(t, "Kitchen", speaker.Area)
	assert.Equal(t, "Ground floor", speaker.Floor)
	assert.Equal(t, "Kitchen speaker", speaker.Device, "a user's device name wins")

	motion := r.Describe("binary_sensor.hallway_motion")
	assert.Equal(t, "Hallway", motion.Area)
	assert.Empty(t, motion.Floor, "the hallway is on no floor")
	assert.Equal(t, []string{"night"}, motion.Labels, "a device's labels are the entity's for targeting")
	assert.Equal(t, []string{"Night"}, motion.LabelNames, "and their names are searchable")

	assert.Equal(t, "diagnostic", r.Describe("sensor.kitchen_speaker_signal").Category)
	assert.True(t, r.Describe("light.hidden_strip").Hidden)
	assert.Empty(t, r.Describe("sun.sun").AreaID)
	_, ok := r.Device("dev-old")
	assert.False(t, ok, "a disabled device is left out")
}

// TestRegistryTargets covers matching by area, floor, device, and label,
// narrowed by domain and device class.
func TestRegistryTargets(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetHouse()
	r := startRegistryDriver(t, srv).Registry()

	motion := map[string]any{"device_class": "motion"}
	tests := []struct {
		name   string
		target Target
		entity string
		attrs  map[string]any
		want   bool
	}{
		{name: "own area", target: Target{AreaIDs: []string{"kitchen"}}, entity: "light.kitchen_ceiling", want: true},
		{name: "device area", target: Target{AreaIDs: []string{"kitchen"}}, entity: "media_player.kitchen_speaker", want: true},
		{name: "other area", target: Target{AreaIDs: []string{"kitchen"}}, entity: "light.bedroom_lamp"},
		{name: "floor", target: Target{FloorIDs: []string{"upstairs"}}, entity: "light.bedroom_lamp", want: true},
		{name: "device", target: Target{DeviceIDs: []string{"dev-speaker"}}, entity: "sensor.kitchen_speaker_signal", want: true},
		{name: "own label", target: Target{LabelIDs: []string{"night"}}, entity: "light.bedroom_lamp", want: true},
		{name: "device label", target: Target{LabelIDs: []string{"night"}}, entity: "binary_sensor.hallway_motion", attrs: motion, want: true},
		{name: "area label", target: Target{LabelIDs: []string{"away"}}, entity: "media_player.kitchen_speaker", want: true},
		{name: "other area's label", target: Target{LabelIDs: []string{"away"}}, entity: "light.bedroom_lamp"},
		{name: "domain narrows", target: Target{AreaIDs: []string{"kitchen"}, Domain: "light"}, entity: "media_player.kitchen_speaker"},
		{name: "class narrows", target: Target{AreaIDs: []string{"hallway"}, DeviceClass: "door"}, entity: "binary_sensor.hallway_motion", attrs: motion},
		{name: "class matches", target: Target{AreaIDs: []string{"hallway"}, DeviceClass: "motion"}, entity: "binary_sensor.hallway_motion", attrs: motion, want: true},
		{name: "unknown entity", target: Target{AreaIDs: []string{"kitchen"}}, entity: "light.nowhere"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, r.Matches(tc.target, tc.entity, tc.attrs))
		})
	}
	assert.Equal(t, "in Kitchen or on Upstairs or labelled Night",
		r.DescribeTarget(Target{AreaIDs: []string{"kitchen"}, FloorIDs: []string{"upstairs"}, LabelIDs: []string{"night"}}))
}

// TestRegistryFollowsUpdates covers an entity moved by an update event, the
// rename a device follows, and a fresh read after a reconnect.
func TestRegistryFollowsUpdates(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetHouse()
	d := startRegistryDriver(t, srv)
	waitFor(t, 2*time.Second, func() bool { return srv.SubscribeCount("entity_registry_updated") > 0 },
		"the driver never subscribed to entity registry updates")

	var changes []RegistryChange
	changed := make(chan RegistryChange, 4)
	stop := d.WatchRegistry(func(c RegistryChange) { changed <- c })
	defer stop()

	srv.SetRegistry("entity", []map[string]any{
		{"ei": "light.kitchen_ceiling", "ai": "bedroom"},
		{"ei": "light.bedroom_lamp_2", "ai": "bedroom"},
	})
	srv.PushRegistryUpdate("entity", map[string]any{"action": "update", "entity_id": "light.kitchen_ceiling"})
	srv.PushRegistryUpdate("entity", map[string]any{"action": "update",
		"entity_id": "light.bedroom_lamp_2", "old_entity_id": "light.bedroom_lamp"})

	select {
	case c := <-changed:
		changes = append(changes, c)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "no registry change after update events")
	}
	assert.Equal(t, "bedroom", changes[0].Registry.Describe("light.kitchen_ceiling").AreaID)
	assert.Equal(t, map[string]string{"light.bedroom_lamp": "light.bedroom_lamp_2"}, changes[0].Renamed)
	select {
	case extra := <-changed:
		assert.Fail(t, "a burst of events reread more than once", "%v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	srv.SetRegistry("area", []map[string]any{{"area_id": "garage", "name": "Garage"}})
	srv.CloseClients()
	select {
	case c := <-changed:
		_, ok := c.Registry.Area("garage")
		assert.True(t, ok, "a reconnect rereads every registry")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no registry read after a reconnect")
	}
}

// TestRegistryOlderHomeAssistant covers a Home Assistant without floors and
// labels: the rest loads, and the two say why they are empty.
func TestRegistryOlderHomeAssistant(t *testing.T) {
	srv := hatest.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetHouse()
	srv.UnknownCommand("config/floor_registry/list")
	srv.UnknownCommand("config/label_registry/list")
	r := startRegistryDriver(t, srv).Registry()

	assert.True(t, r.Loaded(RegistryArea))
	assert.Contains(t, r.Status[RegistryFloor], "Unknown command")
	assert.Contains(t, r.Status[RegistryLabel], "Unknown command")
	assert.Empty(t, r.Floors)
	info := r.Describe("light.bedroom_lamp")
	assert.Equal(t, "Bedroom", info.Area)
	assert.Empty(t, info.Floor)
}
