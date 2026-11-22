package entitydevices

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// haFixture is a fake Home Assistant house, a driver connected to it, and a
// device runner resolving the connection to that driver.
type haFixture struct {
	srv    *hatest.Server
	driver *homeassistant.Driver
	runner *mqttrelay.Devices
	connID uuid.UUID
}

func setupHA(t *testing.T) *haFixture {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())

	srv := hatest.New("devices-token")
	t.Cleanup(srv.Close)
	srv.SetHouse()
	srv.SetState("light.kitchen_ceiling", "on", map[string]any{"friendly_name": "Kitchen Ceiling",
		"supported_color_modes": []any{"brightness"}, "brightness": 255})
	srv.SetState("media_player.kitchen_speaker", "playing", map[string]any{"friendly_name": "Kitchen speaker",
		"supported_features": 1 | 4 | 8 | 4096 | 16384, "volume_level": 0.5})
	srv.SetState("binary_sensor.hallway_motion", "off", map[string]any{"friendly_name": "Hallway motion", "device_class": "motion"})
	srv.SetState("light.bedroom_lamp", "off", map[string]any{"friendly_name": "Bedroom lamp", "supported_color_modes": []any{"onoff"}})

	connID := uuid.New()
	cfg, _ := json.Marshal(homeassistant.Config{BaseURL: srv.URL, Token: "devices-token"})
	built, err := homeassistant.New(integrations.ConnModel{ID: connID, Kind: homeassistant.Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	driver := built.(*homeassistant.Driver)
	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)

	runner := mqttrelay.NewDevices(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev; runner.Stop() })

	src := HomeAssistant(driver)
	// The source lists entities once the first full state read has seeded
	// the driver, which the first state watcher starts.
	stop := src.WatchEntityList(func([]Entity) {})
	t.Cleanup(stop)
	eventually(t, func() bool { return src.Connected() && driver.Registry().Loaded(homeassistant.RegistryEntity) },
		"the Home Assistant source never became ready")
	return &haFixture{srv: srv, driver: driver, runner: runner, connID: connID}
}

// TestHomeAssistantEntitiesBecomeDevices covers adding Home Assistant entities
// as devices: the listing by area with kinds, commands mapped to services and
// refused before sending when the entity's features lack them, values that
// change only on reported state and drop what it no longer reports,
// availability, a rename, and a removal.
func TestHomeAssistantEntitiesBecomeDevices(t *testing.T) {
	fx := setupHA(t)
	src := HomeAssistant(fx.driver)

	listed := Discover(src, nil, fx.connID)
	byID := map[string]Discovered{}
	var order []string
	for _, d := range listed {
		byID[d.ObjectID] = d
		order = append(order, d.ObjectID)
	}
	assert.Equal(t, []string{"light.kitchen_ceiling", "media_player.kitchen_speaker", "light.bedroom_lamp",
		"binary_sensor.hallway_motion"}, order, "listed by floor and area")
	assert.Equal(t, "Ground floor · Kitchen", byID["media_player.kitchen_speaker"].Group)
	assert.Equal(t, "media", byID["media_player.kitchen_speaker"].Kind)
	assert.Equal(t, "light", byID["light.kitchen_ceiling"].Kind)
	assert.False(t, byID["binary_sensor.hallway_motion"].Commandable, "a reading takes no commands")

	result, err := Add(src, fx.connID, []string{"light.kitchen_ceiling", "media_player.kitchen_speaker",
		"binary_sensor.hallway_motion", "light.bedroom_lamp"})
	require.NoError(t, err)
	require.Len(t, result.Added, 4)
	assert.Equal(t, "Kitchen Ceiling", result.Added[0].Name)
	assert.Equal(t, "homeassistant", result.Added[0].Transport)

	fx.runner.Reload()
	devices := map[string]*models.MQTTDevice{}
	for _, m := range mustDevices(t) {
		devices[m.EntityKey] = m
	}
	light, speaker, motion := devices["light.kitchen_ceiling"], devices["media_player.kitchen_speaker"], devices["binary_sensor.hallway_motion"]
	eventually(t, func() bool {
		state, _ := fx.runner.State(speaker.ID)
		return state["state"] == "playing" && state["volume"] == float64(50)
	}, "the speaker's state reaches its device")
	eventually(t, func() bool {
		state, _ := fx.runner.State(motion.ID)
		return state["value"] == "off"
	}, "a reading reaches its device under the kind's primary attribute")

	ctx := context.Background()
	_, err = fx.runner.Command(ctx, light, "brightness", 40.0, nil, nil)
	require.NoError(t, err)
	_, err = fx.runner.Command(ctx, speaker, "state", "paused", nil, nil)
	require.NoError(t, err)
	calls := fx.srv.ServiceCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, hatest.ServiceCall{Domain: "light", Service: "turn_on",
		ServiceData: map[string]any{"brightness_pct": float64(40)},
		Target:      map[string]any{"entity_id": []any{"light.kitchen_ceiling"}}}, calls[0])
	assert.Equal(t, "media_pause", calls[1].Service)
	state, _ := fx.runner.State(speaker.ID)
	assert.Equal(t, "playing", state["state"], "the value waits for Home Assistant to report the pause")

	var invalid *mqttrelay.InvalidCommandError
	_, err = fx.runner.Command(ctx, speaker, "command", "next", nil, nil)
	assert.True(t, errors.As(err, &invalid), "a speaker without skip refuses next")
	_, err = fx.runner.Command(ctx, motion, "", "on", nil, nil)
	assert.True(t, errors.As(err, &invalid), "a reading takes no commands")
	assert.Len(t, fx.srv.ServiceCalls(), 2, "refused writes send nothing")

	fx.srv.PushStateChanged("media_player.kitchen_speaker", nil,
		hatest.StateObj("media_player.kitchen_speaker", "playing", map[string]any{"supported_features": 1 | 4 | 8 | 4096 | 16384,
			"volume_level": 0.5, "media_title": "Morning"}))
	eventually(t, func() bool {
		state, _ := fx.runner.State(speaker.ID)
		return state["title"] == "Morning"
	}, "the track title reaches the device")
	fx.srv.PushStateChanged("media_player.kitchen_speaker",
		hatest.StateObj("media_player.kitchen_speaker", "playing", nil),
		hatest.StateObj("media_player.kitchen_speaker", "paused", map[string]any{"supported_features": 1 | 4 | 8 | 4096 | 16384, "volume_level": 0.5}))
	eventually(t, func() bool {
		state, _ := fx.runner.State(speaker.ID)
		return state["state"] == "paused"
	}, "the reported pause reaches the device")
	state, _ = fx.runner.State(speaker.ID)
	assert.NotContains(t, state, "title", "a title Home Assistant no longer reports is cleared")

	fx.srv.PushStateChanged("light.kitchen_ceiling", nil, hatest.StateObj("light.kitchen_ceiling", "unavailable", nil))
	eventually(t, func() bool {
		availability, _, _ := fx.runner.Availability(light.ID)
		return availability == mqttrelay.AvailabilityOffline
	}, "an unavailable entity makes its device offline")

	tracker := NewTracker(zap.NewNop())
	t.Cleanup(tracker.Stop)
	tracker.Attach(fx.connID, src)

	fx.srv.SetRegistry("entity", []map[string]any{
		{"ei": "light.kitchen_main", "ai": "kitchen"},
		{"ei": "media_player.kitchen_speaker", "di": "dev-speaker"},
		{"ei": "binary_sensor.hallway_motion", "di": "dev-motion"},
		{"ei": "light.bedroom_lamp", "ai": "bedroom"},
	})
	fx.srv.RemoveState("light.kitchen_ceiling")
	fx.srv.PushStateChanged("light.kitchen_main", nil, hatest.StateObj("light.kitchen_main", "on", nil))
	fx.srv.PushRegistryUpdate("entity", map[string]any{"action": "update",
		"entity_id": "light.kitchen_main", "old_entity_id": "light.kitchen_ceiling"})
	// The registry is reread two seconds after its last update event, so the
	// rename needs longer than the usual wait.
	within(t, 10*time.Second, func() bool {
		m, err := models.FindMQTTDeviceByID(light.ID)
		return err == nil && m.EntityKey == "light.kitchen_main" && !m.Missing
	}, "a renamed entity's device follows the new ID")

	fx.srv.PushStateChanged("light.bedroom_lamp", hatest.StateObj("light.bedroom_lamp", "off", nil), nil)
	within(t, 10*time.Second, func() bool {
		m, err := models.FindMQTTDeviceByID(devices["light.bedroom_lamp"].ID)
		return err == nil && m.Missing
	}, "a removed entity's device is marked missing")
}

// within polls cond until it holds or the timeout passes.
func within(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		require.True(t, time.Now().Before(deadline), msg)
		time.Sleep(20 * time.Millisecond)
	}
}
