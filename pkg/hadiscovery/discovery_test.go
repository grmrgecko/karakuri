package hadiscovery

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is one recorded discovery message.
type fixture struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

// loadConfigs parses a named fixture from testdata/configs.json.
func loadConfigs(t *testing.T, name string) []Config {
	t.Helper()
	raw, err := os.ReadFile("testdata/configs.json")
	require.NoError(t, err)
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &all))
	var f fixture
	require.NoError(t, json.Unmarshal(all[name], &f), "fixture %s must exist", name)
	cfgs, err := Parse("homeassistant", f.Topic, f.Payload)
	require.NoError(t, err)
	return cfgs
}

// TestParseShapes pins the discovery message shapes devices publish:
// abbreviated keys expand, "~" becomes the base topic, a device-based config
// splits into one config per component that inherits the shared state topic,
// Zigbee2MQTT's own configs are recognised by their device identifier, and an
// empty payload withdraws the topic.
func TestParseShapes(t *testing.T) {
	cover := loadConfigs(t, "esphome_cover")
	require.Len(t, cover, 1)
	assert.Equal(t, "cover/ESPcoverdoor", cover[0].Key, "a config with a unique ID is keyed by component and ID")
	assert.Equal(t, "garage/cover/door/state", cover[0].String("state_topic", ""), "~ must expand to the base topic")
	assert.Equal(t, "garage Door", cover[0].Name(), "device and entity names combine")

	split := loadConfigs(t, "device_based")
	require.Len(t, split, 2, "a device-based config yields one config per component")
	byComponent := map[string]Config{}
	for _, c := range split {
		byComponent[c.Component] = c
	}
	assert.Equal(t, "node1/state", byComponent["sensor"].String("state_topic", ""), "components inherit the shared state topic")
	assert.Equal(t, "REBOOT", byComponent["button"].String("payload_press", ""), "component keys expand too")

	assert.True(t, loadConfigs(t, "z2m_light")[0].FromZigbee2MQTT(), "Zigbee2MQTT tags its devices zigbee2mqtt_<ieee>")
	assert.False(t, loadConfigs(t, "tasmota_switch")[0].FromZigbee2MQTT(), "other firmware is not Zigbee2MQTT")

	removed, err := Parse("homeassistant", "homeassistant/switch/x/config", nil)
	require.NoError(t, err)
	require.Len(t, removed, 1)
	assert.True(t, removed[0].Removed, "an empty payload withdraws the config")

	none, err := Parse("homeassistant", "homeassistant/switch/x/state", []byte("ON"))
	require.NoError(t, err)
	assert.Empty(t, none, "only topics ending in /config are configs")
}

// TestMappedScripts runs the generated scripts in the sandbox against the
// messages each firmware publishes, so the template translation and the
// component mapping are checked together: what a state message reads as, and
// what a command publishes.
func TestMappedScripts(t *testing.T) {
	cases := []struct {
		name        string
		fixture     string
		component   string
		kind        string
		topic       string
		message     string
		want        map[string]any
		field       string
		value       any
		wantTopic   string
		wantPublish string
	}{
		{name: "Tasmota relay reads POWER from its JSON state", fixture: "tasmota_switch", kind: "boolean",
			topic: "tele/plug1/STATE", message: `{"Time":"2026-11-22T10:00:00","POWER":"ON"}`, want: map[string]any{"on": true},
			field: "on", value: false, wantTopic: "cmnd/plug1/POWER", wantPublish: "OFF"},
		{name: "Tasmota power sensor reads a nested key with bracket syntax", fixture: "tasmota_power", kind: "number",
			topic: "tele/plug1/SENSOR", message: `{"ENERGY":{"Power":42.5}}`, want: map[string]any{"value": 42.5}},
		{name: "ESPHome JSON light scales brightness 0 to 255 into percent and colour into hex", fixture: "esphome_light", kind: "light",
			topic: "desk/light/desk_lamp/state", message: `{"state":"ON","brightness":128,"color":{"r":255,"g":0,"b":0}}`,
			// 128 / 255 * 100 = 50.2, rounded to 50.
			want:  map[string]any{"on": true, "brightness": float64(50), "color": "#ff0000"},
			field: "brightness", value: float64(100), wantTopic: "desk/light/desk_lamp/command", wantPublish: `{"brightness":255,"state":"ON"}`},
		{name: "ESPHome cover reads a lower-case state and sets position", fixture: "esphome_cover", kind: "cover",
			topic: "garage/cover/door/state", message: "open", want: map[string]any{"state": "open"},
			field: "position", value: float64(40), wantTopic: "garage/cover/door/position/command", wantPublish: "40"},
		{name: "Shelly input reads an inline if over a JSON boolean", fixture: "shelly_binary", kind: "boolean",
			topic: "shellyplus1-abc/status/input:0", message: `{"id":0,"state":true}`, want: map[string]any{"on": true}},
		{name: "climate reads the target temperature and commands the mode", fixture: "climate", kind: "climate",
			topic: "hvac1/target", message: "21.5", want: map[string]any{"target": 21.5},
			field: "mode", value: "heat", wantTopic: "hvac1/mode/set", wantPublish: "heat"},
		{name: "number applies arithmetic in its value template and a JSON command template", fixture: "number_template", kind: "number",
			// 55 / 10 = 5.5.
			topic: "lvl1/state", message: `{"level":55}`, want: map[string]any{"value": 5.5},
			field: "value", value: float64(7), wantTopic: "lvl1/set", wantPublish: `{"level": 7}`},
		{name: "device-based button presses its payload on its command topic", fixture: "device_based", component: "button", kind: "buttons",
			field: "value", value: "REBOOT", wantTopic: "node1/reboot", wantPublish: "REBOOT"},
	}
	ctx := context.Background()
	for _, tc := range cases {
		cfgs := loadConfigs(t, tc.fixture)
		cfg := cfgs[0]
		for _, c := range cfgs {
			if c.Component == tc.component {
				cfg = c
			}
		}
		m, err := Map(cfg, uuid.New())
		require.NoError(t, err, "mapping %s", tc.name)
		assert.Equal(t, tc.kind, m.Definition.Control, "kind for %s", tc.name)
		require.NoError(t, mqttrelay.ValidateDefinition(m.Definition), "definition for %s must be valid", tc.name)

		if tc.topic != "" {
			got, _, err := mqttrelay.TestStateScript(ctx, m.Definition.StateScript, tc.topic, tc.message, nil)
			require.NoError(t, err, "state script for %s", tc.name)
			assert.Equal(t, tc.want, got, "state read for %s", tc.name)
		}
		if tc.field != "" {
			msg, _, err := mqttrelay.RunPayloadScriptWithOutput(ctx, m.Definition.PayloadScript,
				mqttrelay.CommandVars(tc.field, tc.value, nil, nil))
			require.NoError(t, err, "payload script for %s", tc.name)
			assert.Equal(t, tc.wantTopic, msg.Topic, "command topic for %s", tc.name)
			assert.Equal(t, tc.wantPublish, msg.Payload, "command payload for %s", tc.name)
		}
	}
}

// TestUnsupportedTemplate pins that a config whose template needs Jinja
// statements is refused with the reason rather than mapped to a script that
// would read wrong values.
func TestUnsupportedTemplate(t *testing.T) {
	_, err := Map(loadConfigs(t, "blocks")[0], uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{% %} blocks", "the reason names what is not supported")

	_, err = Map(loadConfigs(t, "tasmota_power")[0], uuid.New())
	assert.NoError(t, err, "a plain {{ }} template beside it maps")
}
