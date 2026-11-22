package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadFixture reads a recorded device list from testdata.
func loadFixture(t *testing.T, name string) []Device {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	devices, err := ParseDevices(raw)
	require.NoError(t, err)
	return devices
}

// byName finds a fixture device by friendly name.
func byName(t *testing.T, devices []Device, name string) Device {
	t.Helper()
	for _, d := range devices {
		if d.FriendlyName == name {
			return d
		}
	}
	require.FailNowf(t, "fixture device missing", "%s", name)
	return Device{}
}

// TestProposeLabDevices pins the mapping against the real Zigbee2MQTT 2.14.2
// list recorded from the lab broker: plugs are toggles with their metering
// kept as fields, the Aqara buttons are a reading of their last action, and
// the air quality sensor is a temperature reading.
func TestProposeLabDevices(t *testing.T) {
	devices := loadFixture(t, "bridge-devices-2.14.json")
	listed := Discover(devices, nil, uuid.New(), "zigbee2mqtt")
	assert.Len(t, listed, 13, "every device but the coordinator")

	kiki := Propose(byName(t, devices, "Kiki"))
	assert.Equal(t, "boolean", kiki.Kind)
	assert.False(t, kiki.ReadOnly)
	assert.Equal(t, []string{"state"}, kiki.Mapped)
	assert.Subset(t, kiki.Fields, []string{"power", "energy", "voltage", "current", "linkquality", "power_on_behavior"})

	button := Propose(byName(t, devices, "0x00158d0008393a9c"))
	assert.Equal(t, "text", button.Kind)
	assert.True(t, button.ReadOnly)
	assert.Equal(t, []string{"action"}, button.Mapped)
	assert.Contains(t, button.Fields, "battery")

	air := Propose(byName(t, devices, "Tuya Air Quality Sensor"))
	assert.Equal(t, "number", air.Kind)
	assert.Equal(t, "°C", air.Options["unit"])
	assert.Subset(t, air.Fields, []string{"humidity", "co2", "voc", "formaldehyd", "pm25"})
}

// TestProposeSyntheticKinds covers the kinds the lab bridge lacks, and the
// devices that cannot be added.
func TestProposeSyntheticKinds(t *testing.T) {
	devices := loadFixture(t, "synthetic.json")

	light := Propose(byName(t, devices, "Kitchen light"))
	assert.Equal(t, "light", light.Kind)
	assert.Equal(t, true, light.Options["brightness"])
	assert.Equal(t, true, light.Options["color"])
	assert.Equal(t, true, light.Options["color_temp"])
	assert.Equal(t, float64(2000), light.Options["min_kelvin"], "1e6 / 500 mireds")
	assert.Equal(t, float64(6536), light.Options["max_kelvin"], "1e6 / 153 mireds, rounded")
	assert.Equal(t, true, light.Options["effects"])

	cover := Propose(byName(t, devices, "Living room blind"))
	assert.Equal(t, "cover", cover.Kind)
	assert.Equal(t, true, cover.Options["position"])
	assert.Equal(t, true, cover.Options["tilt"])

	lock := Propose(byName(t, devices, "Front door lock"))
	assert.Equal(t, "lock", lock.Kind)
	assert.False(t, lock.ReadOnly)

	fan := Propose(byName(t, devices, "Bedroom fan"))
	assert.Equal(t, "fan", fan.Kind)
	assert.Equal(t, true, fan.Options["presets"])

	thermostat := Propose(byName(t, devices, "Hall thermostat"))
	assert.Equal(t, "climate", thermostat.Kind)
	assert.Equal(t, float64(5), thermostat.Options["min_temp"])
	assert.Equal(t, float64(30), thermostat.Options["max_temp"])

	contact := Propose(byName(t, devices, "Back door"))
	assert.Equal(t, "boolean", contact.Kind)
	assert.True(t, contact.ReadOnly)
	assert.Equal(t, []string{"battery", "device_temperature", "linkquality"}, contact.Fields)

	listed := Discover(devices, nil, uuid.New(), "")
	reasons := map[string]string{}
	for _, d := range listed {
		if !d.CanAdd {
			reasons[d.FriendlyName] = d.Reason
		}
	}
	assert.Equal(t, map[string]string{
		"Mystery sensor":     "not supported by Zigbee2MQTT",
		"0x00158d0000000008": "still being interviewed",
	}, reasons)
}

// TestGeneratedStateScripts runs generated state scripts on recorded
// Zigbee2MQTT messages through the same sandbox a running device uses.
func TestGeneratedStateScripts(t *testing.T) {
	lab := loadFixture(t, "bridge-devices-2.14.json")
	synthetic := loadFixture(t, "synthetic.json")
	ctx := context.Background()
	run := func(d Device, payload string) map[string]any {
		t.Helper()
		got, _, err := mqttrelay.TestStateScript(ctx, StateScript(Propose(d)), "zigbee2mqtt/"+d.FriendlyName, payload, nil)
		require.NoError(t, err)
		return got
	}

	plug := run(byName(t, lab, "Kiki"), `{"state":"ON","power":12.5,"energy":3.2,"linkquality":120}`)
	assert.Equal(t, map[string]any{"on": true, "power": 12.5, "energy": 3.2, "linkquality": float64(120)}, plug)

	button := run(byName(t, lab, "0x00158d0008393a9c"), `{"action":"double","battery":97}`)
	assert.Equal(t, "double", button["value"])
	cleared := run(byName(t, lab, "0x00158d0008393a9c"), `{"action":"","battery":97}`)
	assert.NotContains(t, cleared, "value", "an emptied action keeps the last press")

	light := run(byName(t, synthetic, "Kitchen light"),
		`{"state":"ON","brightness":127,"color_temp":250,"color":{"x":0.7006,"y":0.2993}}`)
	assert.Equal(t, true, light["on"])
	assert.Equal(t, float64(50), light["brightness"], "127 of 254 is 50%")
	assert.Equal(t, float64(4000), light["color_temp"], "1e6 / 250 mireds")
	assert.Equal(t, "#ff0000", light["color"], "the red corner of the gamut")

	cover := run(byName(t, synthetic, "Living room blind"), `{"state":"CLOSE","position":0}`)
	assert.Equal(t, map[string]any{"state": "closed", "position": float64(0)}, cover)

	lock := run(byName(t, synthetic, "Front door lock"), `{"state":"LOCK","lock_state":"not_fully_locked"}`)
	assert.Equal(t, "jammed", lock["state"], "lock_state is the better reading")

	thermostat := run(byName(t, synthetic, "Hall thermostat"),
		`{"system_mode":"heat","occupied_heating_setpoint":21,"local_temperature":19.5,"running_state":"heat"}`)
	assert.Equal(t, map[string]any{"mode": "heat", "target": float64(21), "current": 19.5, "action": "heat"}, thermostat)

	contact := run(byName(t, synthetic, "Back door"), `{"contact":true,"battery":90}`)
	assert.Equal(t, map[string]any{"on": true, "battery": float64(90)}, contact)

	none, _, err := mqttrelay.TestStateScript(ctx, StateScript(Propose(byName(t, lab, "Kiki"))), "t", "ON", nil)
	require.NoError(t, err)
	assert.Nil(t, none, "a payload that is not a JSON object is ignored")
}

// TestGeneratedPayloadScripts runs generated payload scripts and checks what
// Zigbee2MQTT would receive: the IEEE topic and its own property names.
func TestGeneratedPayloadScripts(t *testing.T) {
	synthetic := loadFixture(t, "synthetic.json")
	lab := loadFixture(t, "bridge-devices-2.14.json")
	ctx := context.Background()
	send := func(d Device, field string, value any) (string, map[string]any) {
		t.Helper()
		on, isBool := value.(bool)
		var onPtr *bool
		if isBool {
			onPtr = &on
		}
		msg, err := mqttrelay.RunPayloadScript(ctx, PayloadScript(Propose(d), "zigbee2mqtt", d.IEEEAddress),
			mqttrelay.CommandVars(field, value, onPtr, nil))
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(msg.Payload), &payload), msg.Payload)
		return msg.Topic, payload
	}

	plug := byName(t, lab, "Office Lamp")
	topic, payload := send(plug, "on", true)
	assert.Equal(t, "zigbee2mqtt/"+plug.IEEEAddress+"/set", topic)
	assert.Equal(t, map[string]any{"state": "ON"}, payload)

	light := byName(t, synthetic, "Kitchen light")
	_, payload = send(light, "brightness", 50.0)
	assert.Equal(t, map[string]any{"brightness": float64(127)}, payload)
	_, payload = send(light, "color_temp", 4000.0)
	assert.Equal(t, map[string]any{"color_temp": float64(250)}, payload)
	_, payload = send(light, "color", "#00ff00")
	assert.Equal(t, map[string]any{"color": map[string]any{"hex": "#00ff00"}}, payload)

	_, payload = send(byName(t, synthetic, "Living room blind"), "state", "closed")
	assert.Equal(t, map[string]any{"state": "CLOSE"}, payload)
	_, payload = send(byName(t, synthetic, "Front door lock"), "state", "unlocked")
	assert.Equal(t, map[string]any{"state": "UNLOCK"}, payload)
	_, payload = send(byName(t, synthetic, "Hall thermostat"), "target", 21.5)
	assert.Equal(t, map[string]any{"occupied_heating_setpoint": 21.5}, payload)

	assert.Empty(t, PayloadScript(Propose(byName(t, synthetic, "Back door")), "zigbee2mqtt", "x"),
		"a sensor takes no commands")
}

// TestDiscoverMarksAdded covers the added mark and name suffixes.
func TestDiscoverMarksAdded(t *testing.T) {
	devices := loadFixture(t, "synthetic.json")
	conn := uuid.New()
	light := byName(t, devices, "Kitchen light")
	existing := []*models.MQTTDevice{
		{ID: uuid.New(), ConnectionID: conn, Z2MBase: "zigbee2mqtt", Z2MIEEE: light.IEEEAddress},
		{ID: uuid.New(), ConnectionID: uuid.New(), Z2MBase: "zigbee2mqtt", Z2MIEEE: byName(t, devices, "Back door").IEEEAddress},
	}
	for _, d := range Discover(devices, existing, conn, "zigbee2mqtt") {
		switch d.FriendlyName {
		case "Kitchen light":
			assert.False(t, d.CanAdd)
			assert.Equal(t, "already added", d.Reason)
		case "Back door":
			assert.True(t, d.CanAdd, "added on another connection does not count")
		}
	}
	taken := map[string]bool{"kitchen light": true, "kitchen light 2": true}
	assert.Equal(t, "Kitchen light 3", uniqueName("Kitchen light", taken))
}

// TestGeneratedLockScriptIgnoresReportedStates locks that a generated lock
// script sends a command only for locked and unlocked. The lock kind also
// accepts the states a lock reports in between, and the script sent UNLOCK for
// every value but locked, so mirroring a jammed or locking lock onto this one
// unlocked the door.
func TestGeneratedLockScriptIgnoresReportedStates(t *testing.T) {
	lock := byName(t, loadFixture(t, "synthetic.json"), "Front door lock")
	script := PayloadScript(Propose(lock), "zigbee2mqtt", lock.IEEEAddress)
	tests := []struct {
		value   string
		payload string
	}{
		{"locked", `{"state":"LOCK"}`},
		{"unlocked", `{"state":"UNLOCK"}`},
		{"locking", ""},
		{"unlocking", ""},
		{"jammed", ""},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			msg, err := mqttrelay.RunPayloadScript(context.Background(), script,
				mqttrelay.CommandVars("state", tc.value, nil, nil))
			require.NoError(t, err)
			if tc.payload == "" {
				assert.Empty(t, msg.Topic, "%s is a reading, so nothing is published, but %s was", tc.value, msg.Payload)
				return
			}
			assert.JSONEq(t, tc.payload, msg.Payload)
		})
	}
}
