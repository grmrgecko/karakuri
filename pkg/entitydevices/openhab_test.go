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
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab/openhabtest"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestOpenHABItemsBecomeDevices covers openHAB items as devices: the listing
// by location with kinds, commands mapped to openHAB commands, values
// following openHAB's reports, a switch group taking commands, NULL as no
// value, a forbidden value refused before sending, and a removed item marking
// its device missing.
func TestOpenHABItemsBecomeDevices(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.House()

	connID := uuid.New()
	cfg, _ := json.Marshal(openhab.Config{BaseURL: srv.URL})
	built, err := openhab.New(integrations.ConnModel{ID: connID, Kind: openhab.Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	driver := built.(*openhab.Driver)
	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)
	runner := mqttrelay.NewDevices(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev; runner.Stop() })
	src := OpenHAB(driver)
	eventually(t, src.Connected, "the openHAB source never connected")

	byID := map[string]Discovered{}
	for _, d := range Discover(src, nil, connID) {
		byID[d.ObjectID] = d
	}
	assert.Equal(t, "light", byID["LivingDimmer"].Kind)
	assert.Equal(t, "Ground floor · Living room", byID["LivingDimmer"].Group)
	assert.Equal(t, "cover", byID["KitchenBlind"].Kind)
	assert.False(t, byID["FrontDoor"].Commandable, "a contact takes no commands")
	assert.False(t, byID["Snapshot"].CanAdd, "an image item is not supported")

	result, err := Add(src, connID, []string{"LivingDimmer", "KitchenBlind", "Volume", "LastSeen", "gLights"})
	require.NoError(t, err)
	require.Len(t, result.Added, 5)
	runner.Reload()
	devices := map[string]*models.MQTTDevice{}
	for _, m := range mustDevices(t) {
		devices[m.EntityKey] = m
	}
	dimmer, blind, volume, seen := devices["LivingDimmer"], devices["KitchenBlind"], devices["Volume"], devices["LastSeen"]
	assert.True(t, mqttrelay.Commandable(devices["gLights"]), "a group with a writable base type takes commands")
	eventually(t, func() bool { s, _ := runner.State(blind.ID); return s["position"] == float64(100) },
		"the blind's state reaches its device, open as 100")

	ctx := context.Background()
	_, err = runner.Command(ctx, dimmer, "brightness", 40.0, nil, nil)
	require.NoError(t, err)
	_, err = runner.Command(ctx, blind, "position", 30.0, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []openhabtest.Command{{Item: "LivingDimmer", Command: "40"}, {Item: "KitchenBlind", Command: "70"}},
		srv.Commands())
	eventually(t, func() bool { s, _ := runner.State(dimmer.ID); return s["brightness"] == float64(40) },
		"the dimmer follows openHAB's report")

	var invalid *mqttrelay.InvalidCommandError
	_, err = runner.Command(ctx, volume, "", 70.0, nil, nil)
	assert.True(t, errors.As(err, &invalid), "a volume past the item's maximum is refused")
	assert.Len(t, srv.Commands(), 2, "a refused write sends nothing")

	time.Sleep(100 * time.Millisecond)
	state, _ := runner.State(seen.ID)
	assert.Empty(t, state, "a NULL item gives its device no value")

	tracker := NewTracker(zap.NewNop())
	t.Cleanup(tracker.Stop)
	tracker.Attach(connID, src)
	srv.RemoveItem("Volume")
	srv.Announce("Volume", "removed")
	within(t, 5*time.Second, func() bool {
		m, err := models.FindMQTTDeviceByID(volume.ID)
		return err == nil && m.Missing
	}, "a removed item's device is marked missing")
}

// TestReadOnlyOpenHABItemIsAReading locks that a device made from an item
// openHAB marks read only is not offered as commandable. openHAB marks most
// sensor channels this way. The item's type alone decided it, so a temperature
// sensor listed as a reading in the add dialog became a device that dashboards
// drew as an input, and a write to it failed as a connection error rather
// than a refused command.
func TestReadOnlyOpenHABItemIsAReading(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	srv := openhabtest.New()
	t.Cleanup(srv.Close)
	srv.SetItem(map[string]any{"name": "OutsideTemp", "label": "Outside temperature", "type": "Number:Temperature",
		"state": "4.5 °C", "stateDescription": map[string]any{"pattern": "%.1f %unit%", "readOnly": true}})
	srv.SetItem(map[string]any{"name": "Setpoint", "label": "Setpoint", "type": "Number:Temperature",
		"state": "21 °C", "stateDescription": map[string]any{"pattern": "%.1f %unit%", "readOnly": false}})

	connID := uuid.New()
	cfg, _ := json.Marshal(openhab.Config{BaseURL: srv.URL})
	built, err := openhab.New(integrations.ConnModel{ID: connID, Kind: openhab.Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	driver := built.(*openhab.Driver)
	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)
	runner := mqttrelay.NewDevices(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev; runner.Stop() })
	src := OpenHAB(driver)
	eventually(t, src.Connected, "the openHAB source never connected")

	listed := map[string]bool{}
	for _, d := range Discover(src, nil, connID) {
		listed[d.ObjectID] = d.Commandable
	}
	require.Equal(t, map[string]bool{"OutsideTemp": false, "Setpoint": true}, listed,
		"the add dialog lists the read-only item as a reading")

	result, err := Add(src, connID, []string{"OutsideTemp", "Setpoint"})
	require.NoError(t, err)
	require.Len(t, result.Added, 2)
	added := map[string]bool{}
	for _, v := range result.Added {
		added[v.EntityKey] = v.Commandable
	}
	assert.Equal(t, listed, added, "the device says what the add dialog said about it")

	var sensor *models.MQTTDevice
	for _, m := range mustDevices(t) {
		if m.EntityKey == "OutsideTemp" {
			sensor = m
		}
	}
	require.NotNil(t, sensor)
	_, err = runner.Command(context.Background(), sensor, "", 20.0, nil, nil)
	var invalid *mqttrelay.InvalidCommandError
	assert.True(t, errors.As(err, &invalid), "a write to a read-only item is a refused command: %v", err)
	assert.ErrorIs(t, err, mqttrelay.ErrNoCommands)
	assert.Empty(t, srv.Commands(), "a refused write sends nothing")
}
