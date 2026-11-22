package mqttrelay

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// liveDriver runs an in-process broker and a driver connected to it, so a
// device can be exercised over a real subscription rather than by calling its
// decode path directly.
func liveDriver(t *testing.T) *mqtt.Driver {
	t.Helper()
	server := mochi.New(&mochi.Options{
		InlineClient: true,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, server.AddHook(new(auth.AllowHook), nil), "the test broker accepts any client")
	tcp := listeners.NewTCP(listeners.Config{ID: "test", Address: "127.0.0.1:0"})
	require.NoError(t, server.AddListener(tcp), "the test broker needs a loopback listener")
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })

	addr := tcp.Address()
	cfg, _ := json.Marshal(mqtt.Config{BrokerURL: "mqtt://" + addr})
	built, err := mqtt.New(
		integrations.ConnModel{ID: uuid.New(), Name: "test", Kind: mqtt.Kind, Config: cfg},
		integrations.Deps{},
	)
	require.NoError(t, err, "the driver must build from a valid config")

	driver, ok := built.(*mqtt.Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")
	require.NoError(t, driver.Start(context.Background()), "the driver must start")
	t.Cleanup(driver.Stop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if driver.Status().State == integrations.StateConnected {
			return driver
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, "the driver never connected to the test broker")
	return nil
}

// waitSubscribed blocks until the driver reports the filter live on the broker.
func waitSubscribed(t *testing.T, driver *mqtt.Driver, filter string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if driver.Subscribed(filter) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNowf(t, "the driver never subscribed",
		"%s never reached the broker, so a publish would be delivered to nobody", filter)
}

// TestDeviceReadsAWildcardLive runs the whole path against a broker: a device
// subscribes to a wildcard, three messages arrive on three topics, and the
// state it accumulates is the union of what each one carried. This is the
// arrangement a real device forces — one value per message, spread over the
// topics it happens to use — and the case a topic plus a value path cannot read.
func TestDeviceReadsAWildcardLive(t *testing.T) {
	driver := liveDriver(t)
	runner := &Devices{
		resolve: func(uuid.UUID) (any, bool) { return driver, true },
		log:     zap.NewNop(),
		running: map[uuid.UUID]*device{},
	}
	t.Cleanup(runner.Stop)

	model := models.MQTTDevice{ID: uuid.New(), Name: "lamp", StateScript: `
	if (topic.endsWith("/brightness")) return { brightness: Number(payload) };
	if (topic.endsWith("/color")) return { color: payload };
	if (topic.endsWith("/state")) return JSON.parse(payload);
	return null;`}
	model.SetTopicList([]string{"home/lamp/#"})
	runner.start(model)
	// Subscribing is asynchronous: the driver's reconcile loop applies the
	// filter after Subscribe returns, so a publish that races it is delivered
	// to nobody. Wait for the broker to have the subscription.
	waitSubscribed(t, driver, "home/lamp/#")

	publish := func(topic, payload string) {
		t.Helper()
		require.NoErrorf(t, driver.Publish(context.Background(), topic, 0, false, []byte(payload)),
			"the test needs %s to reach the broker", topic)
	}
	publish("home/lamp/brightness", "128")
	publish("home/lamp/color", "#ff0000")
	publish("home/lamp/state", `{"on":true}`)
	publish("home/lamp/rssi", "-52")

	// Wait for the three the decoder claims; the fourth must never appear.
	var state map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _ = runner.State(model.ID)
		if len(state) == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	assert.Equal(t, float64(128), state["brightness"], "a value from one topic must reach the accumulated state")
	assert.Equal(t, "#ff0000", state["color"], "a value from another topic must join it rather than replace it")
	assert.Equal(t, true, state["on"], "a value from a third topic must join them both")
	assert.NotContains(t, state, "rssi",
		`the script returned nothing for this topic, which means "not for me": a wildcard needs that to be possible`)
}

// TestDeviceKeepsWhatAMessageDidNotMentionLive is the partial-update case over
// a real subscription: the second message says nothing about brightness, and
// the brightness has to still be there afterwards.
func TestDeviceKeepsWhatAMessageDidNotMentionLive(t *testing.T) {
	driver := liveDriver(t)
	runner := &Devices{
		resolve: func(uuid.UUID) (any, bool) { return driver, true },
		log:     zap.NewNop(),
		running: map[uuid.UUID]*device{},
	}
	t.Cleanup(runner.Stop)

	model := models.MQTTDevice{ID: uuid.New(), Name: "lamp", StateScript: `return JSON.parse(payload);`}
	model.SetTopicList([]string{"home/lamp"})
	runner.start(model)
	waitSubscribed(t, driver, "home/lamp")

	send := func(payload string, want int) map[string]any {
		t.Helper()
		require.NoError(t, driver.Publish(context.Background(), "home/lamp", 0, false, []byte(payload)),
			"the test needs the message to reach the broker")

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			state, _ := runner.State(model.ID)
			if len(state) == want {
				return state
			}
			time.Sleep(20 * time.Millisecond)
		}
		state, _ := runner.State(model.ID)
		require.FailNowf(t, "the device's state never reached the expected size",
			"settled at %v, want %d values", state, want)
		return nil
	}

	send(`{"state":"ON","brightness":128}`, 2)
	state := send(`{"color":"#00ff00"}`, 3)

	assert.Equal(t, float64(128), state["brightness"],
		"the second message said nothing about brightness, so the accumulated value must survive it")
	assert.Equal(t, "#00ff00", state["color"], "the value the second message did carry must be merged in")
}
