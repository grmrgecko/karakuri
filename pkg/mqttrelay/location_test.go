package mqttrelay

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ownTracksScript is the state script docs/mqtt-device-state-script.md
// documents for OwnTracks. Keep the two identical.
const ownTracksScript = `const m = JSON.parse(payload);
if (m._type === "transition") {
  if (m.event === "leave") return { state: "not_home" };
  const zone = String(m.desc || "");
  return { state: zone.toLowerCase() === "home" ? "home" : zone };
}
if (m._type === "location") {
  return { latitude: m.lat, longitude: m.lon, accuracy: m.acc, source: "gps" };
}
return null;`

// Recorded OwnTracks messages, trimmed to the fields the script reads.
const (
	ownTracksEnter    = `{"_type":"transition","event":"enter","desc":"Home","lat":41.8781,"lon":-87.6298,"acc":12,"tst":1793452000}`
	ownTracksLocation = `{"_type":"location","lat":41.8790,"lon":-87.6301,"acc":25,"batt":81,"tst":1793452060}`
	ownTracksLeave    = `{"_type":"transition","event":"leave","desc":"Home","lat":41.8800,"lon":-87.6310,"acc":15,"tst":1793452120}`
	ownTracksWaypoint = `{"_type":"waypoint","desc":"Home","lat":41.8781,"lon":-87.6298,"rad":100}`
)

// TestOwnTracksScript runs the documented script over recorded messages: a
// region enter sets the zone, a location update moves only the coordinates,
// a leave sets not_home, and other message types are ignored.
func TestOwnTracksScript(t *testing.T) {
	ctx := context.Background()

	got, _, err := TestStateScript(ctx, ownTracksScript, "owntracks/james/phone", ownTracksEnter, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"state": "home"}, got, "an enter of the Home region is home")

	got, _, err = TestStateScript(ctx, ownTracksScript, "owntracks/james/phone", ownTracksLocation, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"latitude": 41.879, "longitude": -87.6301, "accuracy": float64(25), "source": "gps"}, got,
		"a location update leaves the zone alone")

	got, _, err = TestStateScript(ctx, ownTracksScript, "owntracks/james/phone", ownTracksLeave, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"state": "not_home"}, got)

	got, _, err = TestStateScript(ctx, ownTracksScript, "owntracks/james/phone", ownTracksWaypoint, nil)
	require.NoError(t, err)
	assert.Nil(t, got, "a waypoint is not a reading")
}

// TestLocationDeviceStampsSince covers since on an MQTT location device. The
// relay sets it, because only the relay knows when the previous message came;
// a fix that keeps the zone keeps it, and a since the script returns is dropped.
func TestLocationDeviceStampsSince(t *testing.T) {
	runner := &Devices{log: zap.NewNop(), running: map[uuid.UUID]*device{}}
	dev := &device{
		model: models.MQTTDevice{Name: "phone", Control: controls.KindLocation, StateScript: `const d = JSON.parse(payload); return d;`},
		state: map[string]any{},
	}
	read := func() map[string]any {
		dev.mu.Lock()
		defer dev.mu.Unlock()
		require.Empty(t, dev.failed)
		return copyState(dev.state)
	}

	runner.receive(dev, "t", []byte(`{"state":"home"}`), false)
	first, ok := read()["since"].(string)
	require.True(t, ok, "entering a state stamps since")

	time.Sleep(1100 * time.Millisecond)
	runner.receive(dev, "t", []byte(`{"latitude":41.9,"since":"2020-01-01T00:00:00Z"}`), false)
	state := read()
	assert.Equal(t, first, state["since"], "a fix in the same zone keeps since, and the script's since is dropped")
	assert.Equal(t, 41.9, state["latitude"])

	runner.receive(dev, "t", []byte(`{"state":"not_home"}`), false)
	assert.NotEqual(t, first, read()["since"], "leaving moves since")
}
