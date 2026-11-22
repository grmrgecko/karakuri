package mqttrelay

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// feed pushes messages through a device and returns the state it accumulated.
// It drives receive directly, so the decoding and merging are exercised without
// a broker or a connection in the way.
func feed(t *testing.T, script string, messages [][2]string) map[string]any {
	t.Helper()
	runner := &Devices{log: zap.NewNop(), running: map[uuid.UUID]*device{}}
	dev := &device{
		model: models.MQTTDevice{Name: "test", StateScript: script},
		state: map[string]any{},
	}
	for _, m := range messages {
		runner.receive(dev, m[0], []byte(m[1]), false)
	}
	dev.mu.Lock()
	defer dev.mu.Unlock()
	require.Empty(t, dev.failed, "the state script must read every message in this fixture")
	return copyState(dev.state)
}

// TestDeviceMergesPartialUpdates covers the case a topic and a value path
// cannot: a device that reports only what changed. The second message says
// nothing about brightness, and the brightness has to survive it.
func TestDeviceMergesPartialUpdates(t *testing.T) {
	script := `const d = JSON.parse(payload); return d;`
	state := feed(t, script, [][2]string{
		{"zigbee2mqtt/lamp", `{"state":"ON","brightness":128}`},
		{"zigbee2mqtt/lamp", `{"color":{"r":255,"g":0,"b":0}}`},
	})

	assert.Equal(t, float64(128), state["brightness"],
		"the second message said nothing about brightness, so the accumulated value must survive it")
	assert.Equal(t, "ON", state["state"], "a value the second message did not mention must survive too")

	color, ok := state["color"].(map[string]any)
	require.Truef(t, ok, "color = %v, want the second message's object merged in", state["color"])
	assert.Equal(t, float64(255), color["r"], "the new value must be merged, not appended alongside the old state")
}

// TestDeviceReadsSeveralTopics covers a device that spreads its state out: the
// decoder is told which topic a message arrived on and says what it meant.
func TestDeviceReadsSeveralTopics(t *testing.T) {
	script := `
	if (topic.endsWith("/brightness")) return { brightness: Number(payload) };
	if (topic.endsWith("/color")) return { color: payload };
	return null;`

	state := feed(t, script, [][2]string{
		{"home/lamp/brightness", "128"},
		{"home/lamp/color", "#ff0000"},
		{"home/lamp/telemetry", "ignored"},
	})

	assert.Equal(t, float64(128), state["brightness"], "the script decides what a message on this topic meant")
	assert.Equal(t, "#ff0000", state["color"], "a value from another topic lands in the same accumulated state")
	assert.Len(t, state, 2,
		`returning nothing means "not for me", which is what a wildcard subscription needs: %v`, state)
}

// TestDeviceDecoderSeesPriorState covers a decoder that needs the old value to
// work out the new one — a counter, a delta, a value split across messages.
func TestDeviceDecoderSeesPriorState(t *testing.T) {
	script := `return { total: (state.total || 0) + Number(payload) };`
	got := feed(t, script, [][2]string{
		{"meter/pulse", "3"},
		{"meter/pulse", "4"},
	})
	assert.Equal(t, float64(7), got["total"],
		"3 + 4 = 7: the script must see what it accumulated last time, or a counter can only ever count one pulse")
}

// TestDeviceDecoderErrorIsHeld covers a decoder that cannot read a message: the
// device records the problem rather than merging nonsense into its state.
func TestDeviceDecoderErrorIsHeld(t *testing.T) {
	runner := &Devices{log: zap.NewNop(), running: map[uuid.UUID]*device{}}
	dev := &device{
		model: models.MQTTDevice{Name: "test", StateScript: `return JSON.parse(payload);`},
		state: map[string]any{"brightness": float64(128)},
	}
	runner.receive(dev, "zigbee2mqtt/lamp", []byte("not json"), false)

	dev.mu.Lock()
	defer dev.mu.Unlock()
	assert.NotEmpty(t, dev.failed,
		"the failure must be recorded, or a device whose script broke looks like one that simply stopped reporting")
	assert.Equal(t, float64(128), dev.state["brightness"],
		"the last good state must be kept: a dashboard showing a stale value beats one showing nothing")
}

// TestStateScriptTestRun covers the editor's Test button: the script runs
// against a sample without a device, and what it printed comes back with what
// it returned, because a test that hid the console would not be a debugging
// tool.
func TestStateScriptTestRun(t *testing.T) {
	got, output, err := TestStateScript(
		context.Background(),
		`console.log("saw", topic); return { on: payload === "ON" };`,
		"home/lamp", "ON", nil,
	)
	require.NoError(t, err, "a working script must run against a sample")
	assert.Equal(t, true, got["on"], "the script's return value is what the device would have merged")

	require.NotEmpty(t, output, "console output must come back, or the Test button is not a debugging tool")
	assert.Contains(t, output[0], "home/lamp",
		"the script can read the topic it was given, and what it printed must be reported verbatim")
}

// TestStateScriptTestRunReportsIgnored covers a script that returns nothing:
// an answer, not a failure.
func TestStateScriptTestRunReportsIgnored(t *testing.T) {
	got, _, err := TestStateScript(context.Background(), `return null;`, "home/lamp", "x", nil)
	require.NoError(t, err,
		`returning nothing means "not for me", which a wildcard subscription needs, so it is an answer rather than a failure`)
	assert.Nil(t, got, "no values means the device would have merged nothing: %v", got)
}

// TestStatusRecordsHearingApartFromDecoding covers the wiring question the
// decoded values cannot answer: a device that hears nothing and a decoder that
// returns nothing look identical from the state, so the last-heard stamp must
// move on a message the decoder ignored while the values stay untouched.
func TestStatusRecordsHearingApartFromDecoding(t *testing.T) {
	runner := &Devices{log: zap.NewNop(), running: map[uuid.UUID]*device{}}
	id := uuid.New()
	dev := &device{
		model: models.MQTTDevice{Name: "test", StateScript: `return null;`},
		state: map[string]any{},
	}
	runner.running[id] = dev

	status, running := runner.Status(id)
	require.True(t, running, "a device in the running set reports as running")
	assert.True(t, status.LastMessage.IsZero(), "before any message the device has heard nothing")

	runner.receive(dev, "zigbee2mqtt/other", []byte(`{}`), false)

	status, _ = runner.Status(id)
	assert.False(t, status.LastMessage.IsZero(),
		"an ignored message still proves the topics are wired; the stamp must not wait for a decode")
	assert.Empty(t, status.State, "a message the decoder said was not for it must not invent values")
	assert.Empty(t, status.Err, "returning nothing is a decision, not a failure")
}
