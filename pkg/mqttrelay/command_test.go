package mqttrelay_test

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPayloadScript covers the command half of an MQTT device. The browser
// sends only the interaction — which field, which value — and the script turns
// it into a topic and bytes, which is why there is no command-topic field: the
// script names its own topic, so one control can address different topics
// depending on what was pressed.
//
// The rows are the arrangements real devices force. A Tasmota infrared blaster
// has one code per direction and no notion of state; a multi-value device routes
// by which field changed; and a device expecting a JSON document gets one, while
// a string payload goes on the wire as written.
func TestPayloadScript(t *testing.T) {
	cases := []struct {
		name      string
		script    string
		vars      map[string]any
		wantTopic string
		want      string
	}{
		{
			// The case the device was shaped around: two infrared codes behind
			// one control, chosen by the direction pressed.
			name: "an infrared blaster with a code per direction",
			script: `return {
				topic: "cmnd/obk1F39F2A9/IRSend",
				payload: on ? "Samsung 0x707 0x99 1" : "Samsung 0x707 0x98 1",
			};`,
			vars:      map[string]any{"on": true},
			wantTopic: "cmnd/obk1F39F2A9/IRSend",
			want:      "Samsung 0x707 0x99 1",
		},
		{
			// A device with several values routes by which one was set.
			name: "a script routes by the field being set",
			script: `if (field === "brightness") {
				return { topic: "home/lamp/brightness/set", payload: String(value) };
			}
			return { topic: "home/lamp/set", payload: { state: on ? "ON" : "OFF" } };`,
			vars:      map[string]any{"field": "brightness", "value": 128},
			wantTopic: "home/lamp/brightness/set",
			want:      "128",
		},
		{
			name: "an object payload is encoded as JSON",
			script: `return { topic: "home/lamp/set",
				payload: { state: on ? "ON" : "OFF", transition: 2 } };`,
			vars:      map[string]any{"on": false},
			wantTopic: "home/lamp/set",
			want:      `{"state":"OFF","transition":2}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := mqttrelay.RunPayloadScript(context.Background(), c.script, c.vars)
			require.NoError(t, err, "this script must run")
			assert.Equal(t, c.wantTopic, got.Topic, "the script names its own topic, which is why there is no topic field")
			assert.Equal(t, c.want, got.Payload,
				"a string payload goes on the wire as written; anything else is JSON-encoded")
		})
	}
}

// TestPayloadScriptNullAction covers a script deciding a change means nothing —
// the command-side counterpart of a state script returning nothing for a topic
// it does not recognise. A device that ignores half its presses is a device
// working as written, so returning no topic is silence rather than failure; an
// error would put a red state on a device that is behaving correctly.
//
// The shapes are the ways a script says it: an explicit null, falling off the
// end without returning, and returning an object with no topic in it, with or
// without a payload.
func TestPayloadScriptNullAction(t *testing.T) {
	for _, script := range []string{
		`return null;`,
		`if (on) return { topic: "home/lamp/set", payload: "ON" };`,
		`return { payload: "ON" };`,
		// The fallthrough of every generated Zigbee2MQTT payload script.
		`return {};`,
	} {
		got, err := mqttrelay.RunPayloadScript(context.Background(), script, map[string]any{"on": false})
		require.NoErrorf(t, err, "%q says the change means nothing, which is not an error", script)
		assert.Emptyf(t, got.Topic, "%q must publish nothing: no topic is the null action", script)
	}
}

// TestPayloadScriptRejects covers what is a mistake rather than a decision,
// against the null actions above. The distinction is the whole point: a script
// that deliberately publishes nothing is silent, while one that meant to
// publish and cannot say where or what has to fail — otherwise an author
// pressing a button would watch nothing happen with nothing to explain it.
func TestPayloadScriptRejects(t *testing.T) {
	cases := []struct{ script, why string }{
		{`return {`, "a script that does not compile cannot have meant to publish nothing"},
		{`return "just a payload";`, "there is something to send but nowhere to send it"},
		{`return { topic: "home/lamp/set" };`, "there is somewhere to send but nothing to send"},
	}
	for _, c := range cases {
		_, err := mqttrelay.RunPayloadScript(context.Background(), c.script, map[string]any{})
		assert.Errorf(t, err, "%q must fail rather than be treated as the null action: %s", c.script, c.why)
	}
}
