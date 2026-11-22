package mqttrelay_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// startBroker runs an embedded MQTT broker on a random port.
func startBroker(t *testing.T) (*mochi.Server, string) {
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
	return server, tcp.Address()
}

// startDriver builds a connected MQTT driver against the broker.
func startDriver(t *testing.T, addr string) *mqtt.Driver {
	t.Helper()
	cfg, _ := json.Marshal(mqtt.Config{BrokerURL: "mqtt://" + addr})
	d, err := mqtt.New(integrations.ConnModel{
		ID: uuid.New(), Name: "test", Kind: mqtt.Kind, Config: cfg,
	}, integrations.Deps{PublishStatus: func(integrations.Status) {}})
	require.NoError(t, err, "the driver must build from a valid config")

	driver, ok := d.(*mqtt.Driver)
	require.True(t, ok, "the mqtt kind's factory must produce an mqtt driver")
	require.NoError(t, driver.Start(context.Background()), "the driver must connect to the test broker")
	t.Cleanup(driver.Stop)
	waitFor(t, 5*time.Second, func() bool {
		return driver.Status().State == integrations.StateConnected
	}, "driver did not connect")
	return driver
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, msg)
}

// TestRelayFollowsDemand covers the whole reason the relay is demand-driven,
// against a real broker. Unlike Home Assistant, which streams every entity
// whether or not anyone is looking, an MQTT subscription is per topic and costs
// traffic — so the broker's own subscriber count is the signal: the first
// watcher opens the upstream subscription and the last one to leave closes it.
//
// Both ends have to be checked, because either alone looks like it works.
// Without the first, a dashboard shows nothing; without the second, an instance
// that ever displayed a topic subscribes to it for the rest of its life.
func TestRelayFollowsDemand(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr)
	connID := uuid.New()

	relay := mqttrelay.New(func(id uuid.UUID) (any, bool) {
		if id != connID {
			return nil, false
		}
		return driver, true
	}, zap.NewNop())
	relay.Start()
	t.Cleanup(relay.Stop)

	topic := "mqtt:" + connID.String() + ":zigbee/kitchen/state"

	// Nothing is relayed before a watcher exists.
	_ = server.Publish("zigbee/kitchen/state", []byte(`{"state":"ON"}`), false, 0)
	time.Sleep(100 * time.Millisecond)

	var mu sync.Mutex
	var received []string
	subID := mq.B.Subscribe(topic, func(msg *mq.Message) {
		var body struct {
			Topic   string `json:"topic"`
			Payload string `json:"payload"`
			Binary  bool   `json:"binary"`
		}
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			return
		}
		mu.Lock()
		received = append(received, body.Topic+"="+body.Payload)
		mu.Unlock()
	})

	// With a watcher, the device's messages reach the broker topic.
	waitFor(t, 5*time.Second, func() bool {
		_ = server.Publish("zigbee/kitchen/state", []byte(`{"state":"ON"}`), false, 0)
		mu.Lock()
		defer mu.Unlock()
		return len(received) > 0
	}, "relay did not deliver the message to its watcher")

	mu.Lock()
	assert.Equal(t, `zigbee/kitchen/state={"state":"ON"}`, received[0],
		"the payload is relayed exactly as it arrived: a relay that decoded on the device's behalf would be guessing")
	mu.Unlock()

	// The last watcher leaving withdraws the upstream subscription.
	mq.B.Unsubscribe(subID)
	time.Sleep(200 * time.Millisecond)

	var late int
	lateSub := mq.B.Subscribe("mqtt:*", func(*mq.Message) {
		mu.Lock()
		late++
		mu.Unlock()
	})
	t.Cleanup(func() { mq.B.Unsubscribe(lateSub) })

	_ = server.Publish("zigbee/kitchen/state", []byte(`{"state":"OFF"}`), false, 0)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	assert.Zero(t, late,
		"the last watcher leaving must close the upstream subscription, or every topic ever viewed is subscribed forever")
	mu.Unlock()
}

// TestRelayFlagsBinaryPayloads covers a payload that is not text. The relay
// carries messages to the browser as JSON, which has no byte type, so arbitrary
// bytes are base64-encoded — and the flag is what tells the widget that is what
// happened. Without it a widget would render the base64 as though it were the
// device's own text.
//
// The bytes are chosen to be invalid UTF-8, so a relay that passed them through
// as a string would mangle them rather than happening to survive.
func TestRelayFlagsBinaryPayloads(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr)
	connID := uuid.New()

	relay := mqttrelay.New(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	relay.Start()
	t.Cleanup(relay.Stop)

	topic := "mqtt:" + connID.String() + ":sensor/raw"
	var mu sync.Mutex
	var binary bool
	var payload string
	subID := mq.B.Subscribe(topic, func(msg *mq.Message) {
		var body struct {
			Payload string `json:"payload"`
			Binary  bool   `json:"binary"`
		}
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			return
		}
		mu.Lock()
		binary = body.Binary
		payload = body.Payload
		mu.Unlock()
	})
	t.Cleanup(func() { mq.B.Unsubscribe(subID) })

	waitFor(t, 5*time.Second, func() bool {
		_ = server.Publish("sensor/raw", []byte{0xff, 0xfe, 0x00}, false, 0)
		mu.Lock()
		defer mu.Unlock()
		return payload != ""
	}, "relay did not deliver the binary message")

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, binary,
		"the flag is how a widget knows to decode rather than render the base64 as the device's own text")
	assert.Equal(t, "//4A", payload, "0xff 0xfe 0x00 is //4A in base64")
}

// TestRelayIgnoresStaleCount covers a count announcement overtaken by a newer
// one. The broker publishes a subscription count after releasing its lock, so
// one client leaving (count 0) and another arriving (count 1) can announce in
// the opposite order. The late zero must not withdraw the upstream
// subscription the remaining watcher depends on.
func TestRelayIgnoresStaleCount(t *testing.T) {
	server, addr := startBroker(t)
	driver := startDriver(t, addr)
	connID := uuid.New()

	relay := mqttrelay.New(func(id uuid.UUID) (any, bool) { return driver, id == connID }, zap.NewNop())
	relay.Start()
	t.Cleanup(relay.Stop)

	topic := "mqtt:" + connID.String() + ":sensor/stale"
	var mu sync.Mutex
	var got int
	subID := mq.B.Subscribe(topic, func(*mq.Message) {
		mu.Lock()
		got++
		mu.Unlock()
	})
	t.Cleanup(func() { mq.B.Unsubscribe(subID) })
	received := func() int {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
	waitFor(t, 5*time.Second, func() bool {
		_ = server.Publish("sensor/stale", []byte("1"), false, 0)
		return received() > 0
	}, "relay did not deliver before the stale count")

	mq.B.Publish("topic:"+topic+":count", "subscription_count", map[string]any{"topic": topic, "count": 0})
	before := received()
	waitFor(t, 5*time.Second, func() bool {
		_ = server.Publish("sensor/stale", []byte("2"), false, 0)
		return received() > before
	}, "a stale zero count withdrew the upstream subscription of a live watcher")
}
