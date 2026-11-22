package mqttrelay

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mediaPayloadScript is a Squeezebox-style bridge's payload script: one topic
// per action, the value as the payload.
const mediaPayloadScript = `const base = "karakuri-test/mediatest/set/";
if (field === "state") return { topic: base + {playing: "play", paused: "pause", idle: "stop"}[value], payload: "" };
return { topic: base + field, payload: String(value) };`

// TestMediaDeviceCommands covers an MQTT device of kind media: a skip reaches
// the payload script as the command field, and a press the device's options
// leave off is refused before the script runs.
func TestMediaDeviceCommands(t *testing.T) {
	device := &models.MQTTDevice{Name: "Media test", Control: controls.KindMedia,
		Options: `{"power": false}`, PayloadScript: mediaPayloadScript}

	tests := []struct {
		field, topic, payload string
		value                 any
	}{
		{field: "command", value: "next", topic: "karakuri-test/mediatest/set/command", payload: "next"},
		{field: "", value: "paused", topic: "karakuri-test/mediatest/set/pause"},
		{field: "volume", value: "35", topic: "karakuri-test/mediatest/set/volume", payload: "35"},
	}
	for _, tc := range tests {
		field, value, err := ResolveCommand(device, tc.field, tc.value)
		require.NoError(t, err)
		msg, err := RunPayloadScript(context.Background(), device.PayloadScript, CommandVars(field, value, nil, nil))
		require.NoError(t, err)
		assert.Equal(t, tc.topic, msg.Topic)
		assert.Equal(t, tc.payload, msg.Payload)
	}

	_, _, err := ResolveCommand(device, "state", "off")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this player has no power control")
	_, _, err = ResolveCommand(device, "volume", 150)
	require.Error(t, err)
}
