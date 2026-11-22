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
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome/esphometest"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// fixture is a simulated node, a driver connected to it, and a device runner
// that resolves the connection to that driver, with a real database.
type fixture struct {
	node   *esphometest.Node
	driver *esphome.Driver
	runner *mqttrelay.Devices
	connID uuid.UUID
}

func setup(t *testing.T, extra ...proto.Message) *fixture {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())

	node := &esphometest.Node{
		Name: "garage", MAC: "AA:BB", Version: "2026.10.1", Board: "esp32dev",
		Commands: make(chan proto.Message, 16),
		Entities: []proto.Message{
			&api.ListEntitiesSwitchResponse{Key: 1, ObjectId: "relay", Name: "Relay"},
			&api.ListEntitiesLightResponse{Key: 2, ObjectId: "porch", Name: "Porch",
				SupportedColorModes: []api.ColorMode{api.ColorMode_COLOR_MODE_BRIGHTNESS}},
			&api.ListEntitiesSensorResponse{Key: 3, ObjectId: "temperature", Name: "Temperature", UnitOfMeasurement: "°C", AccuracyDecimals: 1},
			&api.ListEntitiesMediaPlayerResponse{Key: 4, ObjectId: "speaker", Name: "Speaker"},
			&api.ListEntitiesCameraResponse{Key: 5, ObjectId: "doorcam", Name: "Door camera"},
		},
		OnCommand: func(cmd proto.Message) []proto.Message {
			switch c := cmd.(type) {
			case *api.SwitchCommandRequest:
				return []proto.Message{&api.SwitchStateResponse{Key: c.Key, State: c.State}}
			case *api.LightCommandRequest:
				return []proto.Message{&api.LightStateResponse{Key: c.Key, State: c.State, Brightness: c.Brightness,
					ColorMode: api.ColorMode_COLOR_MODE_BRIGHTNESS}}
			case *api.MediaPlayerCommandRequest:
				state := &api.MediaPlayerStateResponse{Key: c.Key, State: api.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING, Volume: 0.5}
				switch {
				case c.HasVolume:
					state.Volume = c.Volume
				case c.Command == api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PAUSE:
					state.State = api.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED
				case c.Command == api.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_MUTE:
					state.Muted = true
				}
				return []proto.Message{state}
			}
			return nil
		},
	}
	node.Entities = append(node.Entities, extra...)
	node.SetState(&api.SensorStateResponse{Key: 3, State: 18.25})
	node.SetState(&api.SwitchStateResponse{Key: 1, State: false})
	addr, err := node.Start()
	require.NoError(t, err)
	t.Cleanup(node.Close)

	host, port := splitAddr(t, addr)
	raw, err := json.Marshal(esphome.Config{Host: host, Port: port})
	require.NoError(t, err)
	connID := uuid.New()
	statuses := make(chan integrations.Status, 32)
	built, err := esphome.New(integrations.ConnModel{ID: connID, Name: "Garage", Kind: esphome.Kind, Config: raw},
		integrations.Deps{PublishStatus: func(s integrations.Status) { statuses <- s }})
	require.NoError(t, err)
	driver := built.(*esphome.Driver)
	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)
	waitConnected(t, driver)

	runner := mqttrelay.NewDevices(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev; runner.Stop() })
	return &fixture{node: node, driver: driver, runner: runner, connID: connID}
}

func waitConnected(t *testing.T, d *esphome.Driver) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for d.Status().State != integrations.StateConnected {
		require.True(t, time.Now().Before(deadline), "the driver never connected")
		time.Sleep(10 * time.Millisecond)
	}
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	var host string
	var port int
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host = addr[:i]
			_, err := jsonNumber(addr[i+1:], &port)
			require.NoError(t, err)
			break
		}
	}
	return host, port
}

func jsonNumber(s string, out *int) (int, error) { return 0, json.Unmarshal([]byte(s), out) }

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		require.True(t, time.Now().Before(deadline), msg)
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEntitiesBecomeDevices covers adding a node's entities as devices: the
// listing with kinds and the unsupported type, names from the node, states
// flowing into the runner, commands that change state only when the node
// reports, and a sensor that cannot be commanded.
func TestEntitiesBecomeDevices(t *testing.T) {
	fx := setup(t)

	listed := Discover(ESPHome(fx.driver), nil, fx.connID)
	require.Len(t, listed, 5)
	byID := map[string]Discovered{}
	for _, d := range listed {
		byID[d.ObjectID] = d
	}
	assert.Equal(t, "boolean", byID["relay"].Kind)
	assert.True(t, byID["relay"].Commandable)
	assert.Equal(t, "number", byID["temperature"].Kind)
	assert.False(t, byID["temperature"].Commandable)
	assert.Equal(t, "media", byID["speaker"].Kind)
	assert.True(t, byID["speaker"].Commandable)
	assert.False(t, byID["doorcam"].CanAdd)

	result, err := Add(ESPHome(fx.driver), fx.connID, []string{"relay", "porch", "temperature", "speaker", "doorcam"})
	require.NoError(t, err)
	require.Len(t, result.Added, 4)
	require.Len(t, result.Failed, 1)
	assert.Equal(t, "doorcam", result.Failed[0].ObjectID)
	assert.Equal(t, "garage Relay", result.Added[0].Name)
	assert.Equal(t, "esphome", result.Added[0].Transport)

	again := Discover(ESPHome(fx.driver), mustDevices(t), fx.connID)
	for _, d := range again {
		if d.ObjectID == "relay" {
			assert.Equal(t, "already added", d.Reason)
		}
	}

	fx.runner.Reload()
	devices := map[string]*models.MQTTDevice{}
	for _, m := range mustDevices(t) {
		devices[m.EntityKey] = m
	}
	eventually(t, func() bool {
		state, _ := fx.runner.State(devices["temperature"].ID)
		return state["value"] == float64(float32(18.25))
	}, "the sensor's current state reaches the device")
	availability, _, _ := fx.runner.Availability(devices["relay"].ID)
	assert.Equal(t, mqttrelay.AvailabilityOnline, availability)

	ctx := context.Background()
	_, err = fx.runner.Command(ctx, devices["relay"], "", true, nil, nil)
	require.NoError(t, err)
	eventually(t, func() bool {
		state, _ := fx.runner.State(devices["relay"].ID)
		return state["on"] == true
	}, "the switch state comes back from the node")

	_, err = fx.runner.Command(ctx, devices["porch"], "brightness", 40.0, nil, nil)
	require.NoError(t, err)
	eventually(t, func() bool {
		state, _ := fx.runner.State(devices["porch"].ID)
		return state["brightness"] == float64(40)
	}, "the light reports the brightness it was given")

	_, err = fx.runner.Command(ctx, devices["porch"], "brightness", 140.0, nil, nil)
	var invalid *mqttrelay.InvalidCommandError
	assert.True(t, errors.As(err, &invalid), "a percentage above 100 is refused before it is sent")
	_, err = fx.runner.Command(ctx, devices["temperature"], "", 20.0, nil, nil)
	assert.True(t, errors.As(err, &invalid), "a sensor takes no commands")

	speaker := devices["speaker"]
	assert.Equal(t, "media", speaker.Control)
	_, err = fx.runner.Command(ctx, speaker, "state", "paused", nil, nil)
	require.NoError(t, err)
	eventually(t, func() bool {
		state, _ := fx.runner.State(speaker.ID)
		return state["state"] == "paused"
	}, "the player reports the pause")
	_, err = fx.runner.Command(ctx, speaker, "volume", 30.0, nil, nil)
	require.NoError(t, err)
	eventually(t, func() bool {
		state, _ := fx.runner.State(speaker.ID)
		return state["volume"] == float64(30) && state["state"] == "playing"
	}, "the player reports the volume as a percentage")
	_, err = fx.runner.Command(ctx, speaker, "muted", true, nil, nil)
	require.NoError(t, err)
	eventually(t, func() bool {
		state, _ := fx.runner.State(speaker.ID)
		return state["muted"] == true
	}, "the player reports the mute")
	for len(fx.node.Commands) > 0 {
		<-fx.node.Commands
	}
	_, err = fx.runner.Command(ctx, speaker, "command", "next", nil, nil)
	assert.True(t, errors.As(err, &invalid), "an ESPHome player has no skip")
	select {
	case cmd := <-fx.node.Commands:
		t.Fatalf("a refused skip reached the node: %v", cmd)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestMissingEntities covers a node reflashed without an entity: its device is
// marked missing, not deleted, and the mark clears when the entity returns.
func TestMissingEntities(t *testing.T) {
	fx := setup(t)
	_, err := Add(ESPHome(fx.driver), fx.connID, []string{"relay", "temperature"})
	require.NoError(t, err)

	tracker := NewTracker(zap.NewNop())
	t.Cleanup(tracker.Stop)
	without := []Entity{}
	for _, e := range ESPHome(fx.driver).Entities() {
		if e.ObjectID != "relay" {
			without = append(without, e)
		}
	}
	tracker.apply(fx.connID, mqttrelay.TransportESPHome, without)
	missing := func(key string) bool {
		for _, m := range mustDevices(t) {
			if m.EntityKey == key {
				return m.Missing
			}
		}
		return false
	}
	assert.True(t, missing("relay"))
	assert.False(t, missing("temperature"))

	tracker.apply(fx.connID, mqttrelay.TransportESPHome, ESPHome(fx.driver).Entities())
	assert.False(t, missing("relay"))
}

func mustDevices(t *testing.T) []*models.MQTTDevice {
	t.Helper()
	list, err := models.ListMQTTDevices()
	require.NoError(t, err)
	return list
}

// statusFeed is a connection status source fed by the driver's own reports.
type statusFeed struct {
	first   integrations.Status
	updates chan integrations.Status
}

func (f *statusFeed) WatchStatus(uuid.UUID) (integrations.Status, bool, <-chan integrations.Status, func()) {
	return f.first, true, f.updates, func() {}
}

// TestNodeGoesSilent covers a node that hangs with its socket open: the
// driver notices within its ping timeout, the node's devices become unknown,
// the driver reconnects when the node answers again, and devices are online
// with fresh states.
func TestNodeGoesSilent(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	t.Cleanup(esphome.SetTimingForTest(100*time.Millisecond, 300*time.Millisecond))

	node := &esphometest.Node{Name: "garage", Entities: []proto.Message{
		&api.ListEntitiesSensorResponse{Key: 3, ObjectId: "temperature", Name: "Temperature"},
	}}
	node.SetState(&api.SensorStateResponse{Key: 3, State: 18})
	addr, err := node.Start()
	require.NoError(t, err)
	t.Cleanup(node.Close)
	host, port := splitAddr(t, addr)
	raw, _ := json.Marshal(esphome.Config{Host: host, Port: port})

	connID := uuid.New()
	feed := &statusFeed{first: integrations.Status{State: integrations.StateConnecting}, updates: make(chan integrations.Status, 64)}
	built, err := esphome.New(integrations.ConnModel{ID: connID, Name: "Garage", Kind: esphome.Kind, Config: raw},
		integrations.Deps{PublishStatus: func(s integrations.Status) { feed.updates <- s }})
	require.NoError(t, err)
	driver := built.(*esphome.Driver)

	runner := mqttrelay.NewDevices(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	runner.SetStatusWatcher(feed)
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev; runner.Stop() })

	require.NoError(t, driver.Start(context.Background()))
	t.Cleanup(driver.Stop)
	waitConnected(t, driver)
	_, err = Add(ESPHome(driver), connID, []string{"temperature"})
	require.NoError(t, err)
	runner.Reload()
	device := mustDevices(t)[0]
	availability := func() string {
		a, _, _ := runner.Availability(device.ID)
		return a
	}
	eventually(t, func() bool { return availability() == mqttrelay.AvailabilityOnline }, "online while connected")

	node.SetSilent(true)
	eventually(t, func() bool { return availability() == mqttrelay.AvailabilityUnknown }, "unknown once the node stops answering")

	node.SetSilent(false)
	node.Drop()
	node.SetState(&api.SensorStateResponse{Key: 3, State: 19})
	deadline := time.Now().Add(5 * time.Second)
	for availability() != mqttrelay.AvailabilityOnline {
		require.True(t, time.Now().Before(deadline), "online again after reconnecting")
		time.Sleep(20 * time.Millisecond)
	}
	eventually(t, func() bool {
		state, _ := runner.State(device.ID)
		return state["value"] == float64(19)
	}, "the state after reconnecting is fresh")
}

// TestCodeLockIsAReading locks that a lock whose node requires a code is not
// offered as commandable. The native API command built for a device carries
// no code, so the add dialog lists such a lock as a reading. The entity type
// alone then decided what the added device was, which made it a lock
// dashboards drew with a button the node would never act on.
func TestCodeLockIsAReading(t *testing.T) {
	fx := setup(t, &api.ListEntitiesLockResponse{Key: 6, ObjectId: "front_door", Name: "Front door", RequiresCode: true})
	src := ESPHome(fx.driver)

	var listed Discovered
	for _, d := range Discover(src, nil, fx.connID) {
		if d.ObjectID == "front_door" {
			listed = d
		}
	}
	require.Equal(t, "lock", listed.Kind)
	require.False(t, listed.Commandable, "the add dialog lists a code lock as a reading")

	result, err := Add(src, fx.connID, []string{"front_door"})
	require.NoError(t, err)
	require.Len(t, result.Added, 1)
	assert.False(t, result.Added[0].Commandable, "the device says what the add dialog said about it")

	device := mustDevices(t)[0]
	_, err = fx.runner.Command(context.Background(), device, "", "unlocked", nil, nil)
	assert.ErrorIs(t, err, mqttrelay.ErrNoCommands, "a write to a code lock is a refused command")
	select {
	case cmd := <-fx.node.Commands:
		assert.Failf(t, "the node was sent a command", "%T reached a lock that needs a code", cmd)
	case <-time.After(200 * time.Millisecond):
	}
}
