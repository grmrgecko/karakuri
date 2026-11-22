package esphome

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome/esphometest"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// testKey is a fixed 32-byte Noise key.
var testKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// garageNode is a simulated node with one entity of most types.
func garageNode() *esphometest.Node {
	n := &esphometest.Node{
		Name: "garage-controller", MAC: "24:6F:28:AA:BB:CC", Version: "2026.10.1", Board: "esp32dev",
		Entities: []proto.Message{
			&api.ListEntitiesSwitchResponse{Key: 1, ObjectId: "relay", Name: "Relay"},
			&api.ListEntitiesBinarySensorResponse{Key: 2, ObjectId: "door", Name: "Door", DeviceClass: "garage_door"},
			&api.ListEntitiesLightResponse{Key: 3, ObjectId: "porch", Name: "Porch",
				SupportedColorModes: []api.ColorMode{api.ColorMode_COLOR_MODE_RGB, api.ColorMode_COLOR_MODE_COLOR_TEMPERATURE},
				MinMireds:           153, MaxMireds: 500, Effects: []string{"None", "Rainbow"}},
			&api.ListEntitiesCoverResponse{Key: 4, ObjectId: "garage_door", Name: "Garage door", SupportsPosition: true, SupportsStop: true},
			&api.ListEntitiesFanResponse{Key: 5, ObjectId: "fan", Name: "Fan", SupportsSpeed: true, SupportedSpeedCount: 3, SupportsOscillation: true},
			&api.ListEntitiesLockResponse{Key: 6, ObjectId: "lock", Name: "Side lock"},
			&api.ListEntitiesClimateResponse{Key: 7, ObjectId: "heater", Name: "Heater", SupportsCurrentTemperature: true,
				SupportedModes:       []api.ClimateMode{api.ClimateMode_CLIMATE_MODE_OFF, api.ClimateMode_CLIMATE_MODE_HEAT},
				VisualMinTemperature: 5, VisualMaxTemperature: 30, VisualTargetTemperatureStep: 0.5},
			&api.ListEntitiesNumberResponse{Key: 8, ObjectId: "delay", Name: "Delay", MinValue: 0, MaxValue: 60, Step: 1, UnitOfMeasurement: "s"},
			&api.ListEntitiesSelectResponse{Key: 9, ObjectId: "mode", Name: "Mode", Options: []string{"auto", "manual"}},
			&api.ListEntitiesTextResponse{Key: 10, ObjectId: "label", Name: "Label", MaxLength: 32},
			&api.ListEntitiesButtonResponse{Key: 11, ObjectId: "restart", Name: "Restart"},
			&api.ListEntitiesSensorResponse{Key: 12, ObjectId: "temperature", Name: "Temperature", UnitOfMeasurement: "°C", AccuracyDecimals: 1},
			&api.ListEntitiesTextSensorResponse{Key: 13, ObjectId: "wifi_ssid", Name: "Wi-Fi SSID"},
			&api.ListEntitiesEventResponse{Key: 14, ObjectId: "doorbell", Name: "Doorbell", EventTypes: []string{"press"}},
			&api.ListEntitiesMediaPlayerResponse{Key: 15, ObjectId: "speaker", Name: "Speaker"},
		},
		Services: []*api.ListEntitiesServicesResponse{{Name: "beep", Key: 100, Args: []*api.ListEntitiesServicesArgument{
			{Name: "times", Type: api.ServiceArgType_SERVICE_ARG_TYPE_INT},
			{Name: "tone", Type: api.ServiceArgType_SERVICE_ARG_TYPE_STRING},
		}}},
	}
	n.OnCommand = func(cmd proto.Message) []proto.Message {
		if sw, ok := cmd.(*api.SwitchCommandRequest); ok {
			return []proto.Message{&api.SwitchStateResponse{Key: sw.Key, State: sw.State}}
		}
		return nil
	}
	return n
}

// startDriver connects a driver to addr with the given config.
func startDriver(t *testing.T, cfg Config) (*Driver, chan integrations.Status) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	statuses := make(chan integrations.Status, 32)
	built, err := New(integrations.ConnModel{ID: uuid.New(), Name: "garage", Kind: Kind, Config: raw},
		integrations.Deps{PublishStatus: func(s integrations.Status) { statuses <- s }})
	require.NoError(t, err)
	d := built.(*Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	return d, statuses
}

// waitStatus waits for a status in the given state.
func waitStatus(t *testing.T, statuses chan integrations.Status, state string, within time.Duration) integrations.Status {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case s := <-statuses:
			if s.State == state {
				return s
			}
		case <-deadline:
			require.FailNowf(t, "no status", "never reached %s", state)
		}
	}
}

// hostPort splits a listener address into a Config.
func hostPort(t *testing.T, addr string) Config {
	t.Helper()
	cfg := Config{Host: "127.0.0.1"}
	_, port, _ := splitHostPort(addr)
	cfg.Port = port
	return cfg
}

// TestDriverSession covers a full encrypted session: the handshake, node
// information, the entity listing, live states, a command and the state the
// node reports back, and an action call from the node.
func TestDriverSession(t *testing.T) {
	node := garageNode()
	node.Key = testKey
	node.SetState(&api.SensorStateResponse{Key: 12, State: 21.5})
	addr, err := node.Start()
	require.NoError(t, err)
	t.Cleanup(node.Close)

	cfg := hostPort(t, addr)
	cfg.EncryptionKey = testKey
	d, statuses := startDriver(t, cfg)
	waitStatus(t, statuses, integrations.StateConnected, 5*time.Second)

	info := d.Info()
	assert.Equal(t, "garage-controller", info.Name)
	assert.Equal(t, "esp32dev", info.Board)
	assert.Len(t, d.Entities(), 15)
	require.Len(t, d.Actions(), 1)
	assert.Equal(t, []ActionArg{{Name: "times", Type: "int"}, {Name: "tone", Type: "string"}}, d.Actions()[0].Args)

	temps := make(chan map[string]any, 4)
	stop, err := d.WatchEntity("temperature", func(v map[string]any) { temps <- v })
	require.NoError(t, err)
	defer stop()
	select {
	case v := <-temps:
		assert.Equal(t, 21.5, v["value"], "the current state is delivered at once")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "no state")
	}

	relay := make(chan map[string]any, 4)
	stopRelay, _ := d.WatchEntity("relay", func(v map[string]any) { relay <- v })
	defer stopRelay()
	require.NoError(t, d.CommandEntity(context.Background(), "relay", "on", true))
	select {
	case v := <-relay:
		assert.Equal(t, true, v["on"], "the state comes from the node's report")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the node never reported the switch")
	}

	actions := make(chan NodeAction, 1)
	d.WatchActions(func(a NodeAction) { actions <- a })
	node.Send(&api.HomeassistantActionRequest{Service: "notify.phone", Data: []*api.HomeassistantServiceMap{{Key: "message", Value: "Garage open"}}})
	select {
	case a := <-actions:
		assert.Equal(t, NodeAction{Action: "notify.phone", Data: map[string]any{"message": "Garage open"}}, a)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "no node action")
	}

	require.NoError(t, d.CallAction(context.Background(), "beep", map[string]any{"times": 3.0, "tone": "high"}))
	assert.Error(t, d.CallAction(context.Background(), "beep", map[string]any{"times": 2.5, "tone": "high"}), "a fraction is not an int")
	assert.Error(t, d.CallAction(context.Background(), "beep", map[string]any{"times": 1.0}), "a missing argument is refused")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case cmd := <-node.Commands:
			if exec, ok := cmd.(*api.ExecuteServiceRequest); ok {
				assert.Equal(t, uint32(100), exec.Key)
				assert.Equal(t, int32(3), exec.Args[0].Int_)
				assert.Equal(t, "high", exec.Args[1].String_)
				return
			}
		case <-deadline:
			require.FailNow(t, "the action never reached the node")
		}
	}
}

// TestDriverWrongKeyAndPassword covers the failures an operator must be able
// to act on.
func TestDriverWrongKeyAndPassword(t *testing.T) {
	node := garageNode()
	node.Key = testKey
	addr, err := node.Start()
	require.NoError(t, err)
	t.Cleanup(node.Close)
	cfg := hostPort(t, addr)

	cfg.EncryptionKey = base64.StdEncoding.EncodeToString([]byte("ffffffffffffffffffffffffffffffff"))
	_, err = Probe(context.Background(), uuid.Nil, mustJSON(t, cfg))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong key")

	cfg.EncryptionKey = testKey
	detail, err := Probe(context.Background(), uuid.Nil, mustJSON(t, cfg))
	require.NoError(t, err)
	assert.Equal(t, "garage-controller (esp32dev), ESPHome 2026.10.1, MAC 24:6F:28:AA:BB:CC, 15 entities", detail)

	plain := garageNode()
	plain.Password = "secret"
	paddr, err := plain.Start()
	require.NoError(t, err)
	t.Cleanup(plain.Close)
	pcfg := hostPort(t, paddr)
	pcfg.Password = "wrong"
	_, err = Probe(context.Background(), uuid.Nil, mustJSON(t, pcfg))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid password")
	pcfg.Password = "secret"
	_, err = Probe(context.Background(), uuid.Nil, mustJSON(t, pcfg))
	assert.NoError(t, err)
}

// TestDriverReconnects covers a node that drops the connection: the driver
// reports the error, reconnects on its own, and states flow again.
func TestDriverReconnects(t *testing.T) {
	t.Cleanup(SetTimingForTest(100*time.Millisecond, 300*time.Millisecond))
	node := garageNode()
	addr, err := node.Start()
	require.NoError(t, err)
	t.Cleanup(node.Close)
	d, statuses := startDriver(t, hostPort(t, addr))
	waitStatus(t, statuses, integrations.StateConnected, 5*time.Second)

	node.Drop()
	waitStatus(t, statuses, integrations.StateError, 25*time.Second)
	waitStatus(t, statuses, integrations.StateConnected, 10*time.Second)

	got := make(chan map[string]any, 4)
	stop, _ := d.WatchEntity("door", func(v map[string]any) { got <- v })
	defer stop()
	node.SetState(&api.BinarySensorStateResponse{Key: 2, State: true})
	select {
	case v := <-got:
		assert.Equal(t, true, v["on"])
	case <-time.After(2 * time.Second):
		require.FailNow(t, "no state after reconnecting")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}
