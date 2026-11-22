package mqttnodes_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/grmrgecko/karakuri/pkg/nodes/mqttnodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// deviceCtx is the minimal ExecContext the device nodes are driven through:
// they read config and interpolate a fixed value, and everything else they do
// goes through the device runner. The embedded nil interface makes any other
// call panic, which is the loud failure a test wants.
type deviceCtx struct {
	engine.ExecContext
	cfg string
}

func (c deviceCtx) Config(out any) error {
	return json.Unmarshal([]byte(c.cfg), out)
}

func (c deviceCtx) Logger() *zap.Logger { return zap.NewNop() }

func (c deviceCtx) Interpolate(s string, msg *engine.Msg) (string, error) {
	if msg == nil {
		msg = &engine.Msg{}
	}
	return expr.Interpolate(s, expr.BuildEnv(expr.Env{
		Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta,
	}))
}

// deviceFixture is a device running against a real broker: the mochi server,
// a connected driver, and a Devices runner installed as mqttrelay.D the way
// serve.go installs it, so the nodes find it where they look.
type deviceFixture struct {
	driver *mqtt.Driver
	runner *mqttrelay.Devices
	dev    *models.MQTTDevice
}

// setupDevice stores the given device and brings the whole read path up
// around it. The runner loads the device from the database through Reload,
// exactly as a running server does, rather than being handed the model.
func setupDevice(t *testing.T, dev *models.MQTTDevice) *deviceFixture {
	t.Helper()
	setupDB(t)
	require.NoError(t, db.DB.Exec("DELETE FROM mqtt_devices").Error,
		"each test starts from an empty device set, since the in-memory database is shared")

	_, addr := startBroker(t)
	driver := startDriver(t, addr)

	dev.ConnectionID = uuid.New()
	require.NoError(t, db.DB.Create(dev).Error, "the runner loads devices from the database")

	runner := mqttrelay.NewDevices(func(uuid.UUID) (any, bool) { return driver, true }, zap.NewNop())
	runner.Reload()
	t.Cleanup(runner.Stop)

	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev })

	for _, filter := range dev.TopicList() {
		waitSubscribed(t, driver, filter)
	}
	return &deviceFixture{driver: driver, runner: runner, dev: dev}
}

// startDriver connects a real MQTT driver to the broker at addr.
func startDriver(t *testing.T, addr string) *mqtt.Driver {
	t.Helper()
	cfg, err := json.Marshal(mqtt.Config{BrokerURL: "mqtt://" + addr})
	require.NoError(t, err, "the driver config fixture must encode")
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

// waitSubscribed blocks until the driver reports the filter live on the
// broker; subscribing is asynchronous, so a publish that races it is
// delivered to nobody.
func waitSubscribed(t *testing.T, driver *mqtt.Driver, filter string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if driver.Subscribed(filter) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNowf(t, "the driver never subscribed", "%s never reached the broker", filter)
}

// capture subscribes to a command topic and collects what arrives on it.
func capture(t *testing.T, driver *mqtt.Driver, filter string) chan string {
	t.Helper()
	out := make(chan string, 16)
	drop, err := driver.Subscribe(filter, 0, func(topic string, payload []byte) {
		out <- string(payload)
	})
	require.NoError(t, err, "the test needs to see what the node publishes")
	t.Cleanup(drop)
	waitSubscribed(t, driver, filter)
	return out
}

// TestDeviceSetCommandsThroughKindAndScript covers the parity contract with a
// dashboard press: the value is held to the device's control kind before its
// payload script runs, the script names the topic, and what the script built
// is what reaches the broker. The rejection is checked first so the single
// captured message proves the out-of-bounds value never got that far.
func TestDeviceSetCommandsThroughKindAndScript(t *testing.T) {
	dev := &models.MQTTDevice{
		Name:          "dimmer",
		Control:       "number",
		PayloadScript: `return {topic: "cmd/dimmer", payload: String(value)};`,
	}
	dev.SetOptionMap(map[string]any{"min": 0, "max": 100})
	fx := setupDevice(t, dev)
	commands := capture(t, fx.driver, "cmd/dimmer")

	cfg := `{"device":"` + dev.ID.String() + `","source":"message","property":"payload.level"}`
	run := func(level float64) (map[string][]*engine.Msg, error) {
		return mqttnodes.DeviceSetNode{}.Execute(context.Background(), deviceCtx{cfg: cfg},
			map[string]*engine.Msg{"in": {Payload: map[string]any{"level": level}}})
	}

	_, err := run(150)
	require.Error(t, err,
		"a value above the kind's maximum must fail the node, not reach the payload script")

	out, err := run(42)
	require.NoError(t, err, "a value inside the bounds must command the device")

	select {
	case payload := <-commands:
		assert.Equal(t, "42", payload, "the broker must receive what the payload script built")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the command never reached the broker")
	}
	assert.Empty(t, commands,
		"only the in-bounds command may arrive: the rejected one ran first, so anything it published would already be here")

	require.NotEmpty(t, out["out"], "the message passes through on success")
	msg := out["out"][0]
	assert.Equal(t, "cmd/dimmer", msg.Meta["device_topic"],
		"the topic the script chose is stamped on the message for the flow to see")
	assert.Equal(t, "value", msg.Meta["device_field"],
		"naming no attribute resolves to the kind's primary attribute")
	assert.Equal(t, float64(42), msg.Meta["device_value"], "the coerced value rides the message")
}

// TestDeviceSetNullAction covers the payload script declining a change:
// returning no topic publishes nothing and fails nothing, and the message
// still passes through marked so a flow can see nothing went out. The "go"
// command after it is what proves the null action stayed off the broker,
// since the script runs sequentially and its message arrives alone.
func TestDeviceSetNullAction(t *testing.T) {
	dev := &models.MQTTDevice{
		Name:          "notifier",
		Control:       "text",
		PayloadScript: `if (value === "skip") return null; return {topic: "cmd/notifier", payload: value};`,
	}
	fx := setupDevice(t, dev)
	commands := capture(t, fx.driver, "cmd/notifier")

	run := func(value string) map[string][]*engine.Msg {
		cfg := `{"device":"notifier","source":"value","value":"` + value + `"}`
		out, err := mqttnodes.DeviceSetNode{}.Execute(context.Background(), deviceCtx{cfg: cfg},
			map[string]*engine.Msg{"in": {}})
		require.NoError(t, err, "a null action is the script's decision, not a failure")
		require.NotEmpty(t, out["out"], "the message passes through either way")
		return out
	}

	skipped := run("skip")
	assert.Equal(t, "", skipped["out"][0].Meta["device_topic"],
		"an empty device_topic is how the message says nothing was published")

	run("go")
	select {
	case payload := <-commands:
		assert.Equal(t, "go", payload, "only the second command may reach the broker")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the real command never reached the broker")
	}
	assert.Empty(t, commands, "the null action must have published nothing ahead of it")
}

// TestDeviceGetReadsAccumulatedState covers reading the state the runner
// accumulated from the broker: the whole object with no field named, one
// value with a field named, and nothing rather than an error for a value the
// device has not reported yet — it is a value that has not arrived, not a
// binding that is wrong. The set-by-name path is exercised on the way, since
// graphs may hold either the ID or the name.
func TestDeviceGetReadsAccumulatedState(t *testing.T) {
	dev := &models.MQTTDevice{
		Name:        "lamp",
		Control:     "light",
		StateScript: `return JSON.parse(payload);`,
	}
	dev.SetTopicList([]string{"home/lamp/state"})
	fx := setupDevice(t, dev)

	require.NoError(t, fx.driver.Publish(context.Background(), "home/lamp/state", 0, false,
		[]byte(`{"on":true,"brightness":50}`)), "the state fixture must reach the broker")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if state, _ := fx.runner.State(dev.ID); state["on"] != nil {
			break
		}
		require.True(t, time.Now().Before(deadline), "the device never accumulated the published state")
		time.Sleep(20 * time.Millisecond)
	}

	read := func(cfg string) *engine.Msg {
		out, err := mqttnodes.DeviceGetNode{}.Execute(context.Background(), deviceCtx{cfg: cfg}, nil)
		require.NoError(t, err, "a pure source runs with no input, which is how a value edge pulls it")
		require.NotEmpty(t, out["out"], "reading a device must emit")
		return out["out"][0]
	}

	whole := read(`{"device":"lamp"}`)
	assert.Equal(t, map[string]any{"on": true, "brightness": float64(50)}, whole.Payload,
		"no field named reads the whole accumulated state")
	assert.Equal(t, "lamp", whole.Topic, "the topic names the device, so a flow reading several can tell them apart")

	one := read(`{"device":"` + dev.ID.String() + `","field":"brightness"}`)
	assert.Equal(t, float64(50), one.Payload, "a named field reads that value alone")

	missing := read(`{"device":"lamp","field":"color"}`)
	assert.Nil(t, missing.Payload, "a value the device has not reported reads as nothing rather than failing")
}

// TestDeviceChangedFiltersOnCarriedValue covers the trigger's filter: with a
// field configured it fires only when a message carried that value, and the
// emitted payload splits what just arrived (values) from the merged state —
// the edge a flow filters on versus everything the device knows.
func TestDeviceChangedFiltersOnCarriedValue(t *testing.T) {
	dev := &models.MQTTDevice{
		Name:    "sensor",
		Control: "number",
		StateScript: `
	if (topic.endsWith("/brightness")) return { brightness: Number(payload) };
	if (topic.endsWith("/color")) return { color: payload };
	return null;`,
	}
	dev.SetTopicList([]string{"home/sensor/#"})
	fx := setupDevice(t, dev)

	emits := make(chan *engine.Msg, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := `{"device":"` + dev.ID.String() + `","field":"brightness"}`
	go func() {
		done <- mqttnodes.DeviceChangedNode{}.Start(ctx, deviceCtx{cfg: cfg},
			func(m *engine.Msg) { emits <- m })
	}()

	publish := func(topic, payload string) {
		t.Helper()
		require.NoErrorf(t, fx.driver.Publish(context.Background(), topic, 0, false, []byte(payload)),
			"the test needs %s to reach the broker", topic)
	}

	// The watcher registers on the trigger's goroutine, so publish until an
	// emit shows it is live; duplicates of this warm-up value are drained.
	deadline := time.Now().Add(5 * time.Second)
	for {
		publish("home/sensor/brightness", "10")
		select {
		case <-emits:
		case <-time.After(50 * time.Millisecond):
			require.True(t, time.Now().Before(deadline), "the trigger never began watching the device")
			continue
		}
		break
	}
	for len(emits) > 0 {
		<-emits
	}

	// One filter means one subscription, so decode order follows publish
	// order: if the color message fired, its emit would precede this one.
	publish("home/sensor/color", "#ff0000")
	publish("home/sensor/brightness", "80")

	var msg *engine.Msg
	select {
	case msg = <-emits:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the brightness message never fired the trigger")
	}

	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "the trigger's payload is the change object its port declares")
	assert.Equal(t, map[string]any{"brightness": float64(80)}, payload["values"],
		"values holds only what this message carried; the color message must not have fired")
	assert.Equal(t, "#ff0000", payload["state"].(map[string]any)["color"],
		"state is the merged whole, so the color that did not fire is still readable")
	assert.Equal(t, "home/sensor/brightness", msg.Topic,
		"the topic is the MQTT topic that spoke, which a wildcard device needs")
	assert.Equal(t, float64(80), payload["value"], "the filtered field's value is lifted beside it")

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "cancelling the run context is how a trigger disarms")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the trigger never returned after cancel")
	}
}

// TestDeviceAvailabilityTrigger covers the availability trigger on a real
// broker: a device's availability topic going offline fires one run carrying
// the device, the new and previous availability, and what decided it, and a
// change to online is filtered out by To.
func TestDeviceAvailabilityTrigger(t *testing.T) {
	dev := &models.MQTTDevice{
		Name:                "hallway-motion",
		Control:             "boolean",
		StateScript:         `return JSON.parse(payload);`,
		AvailabilityTopic:   "home/motion/availability",
		AvailabilityOnline:  "online",
		AvailabilityOffline: "offline",
	}
	dev.SetTopicList([]string{"home/motion"})
	fx := setupDevice(t, dev)
	waitSubscribed(t, fx.driver, "home/motion/availability")

	emits := make(chan *engine.Msg, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := `{"device":"` + dev.ID.String() + `","to":"offline"}`
	go func() {
		_ = mqttnodes.DeviceAvailabilityNode{}.Start(ctx, deviceCtx{cfg: cfg}, func(m *engine.Msg) { emits <- m })
	}()

	publish := func(payload string) {
		t.Helper()
		require.NoError(t, fx.driver.Publish(context.Background(), "home/motion/availability", 0, false, []byte(payload)))
	}

	// The watcher registers on the trigger's goroutine, so toggle until an
	// offline emit shows it is live.
	var msg *engine.Msg
	deadline := time.Now().Add(5 * time.Second)
	for msg == nil {
		publish(`{"state":"online"}`)
		publish("offline")
		select {
		case msg = <-emits:
		case <-time.After(100 * time.Millisecond):
			require.True(t, time.Now().Before(deadline), "the trigger never fired")
		}
	}
	payload := msg.Payload.(map[string]any)
	assert.Equal(t, dev.ID.String(), payload["device_id"])
	assert.Equal(t, "offline", payload["availability"])
	assert.Equal(t, "online", payload["previous"], "To filters the online change out but it is still the previous value")
	assert.Equal(t, mqttrelay.SourceTopic, payload["source"])

	availability, _, running := fx.runner.Availability(dev.ID)
	require.True(t, running)
	assert.Equal(t, mqttrelay.AvailabilityOffline, availability)
}

// entityRecorder is an entity connection that records the one command it is
// sent, standing in for an ESPHome node at the transport interface.
type entityRecorder struct {
	objectID, field string
	value           any
}

func (e *entityRecorder) WatchEntity(string, func(map[string]any)) (func(), error) {
	return func() {}, nil
}

func (e *entityRecorder) CommandEntity(_ context.Context, objectID, field string, value any) error {
	e.objectID, e.field, e.value = objectID, field, value
	return nil
}

// TestDeviceSetCommandsEntityDevices locks that Command Device reaches devices
// that are not MQTT. An ESPHome, Frigate, Home Assistant, or openHAB device
// has no payload script, since the connection sends the command itself, so
// "has a payload script" cannot be the test for "takes commands".
func TestDeviceSetCommandsEntityDevices(t *testing.T) {
	setupDB(t)
	require.NoError(t, db.DB.Exec("DELETE FROM mqtt_devices").Error,
		"each test starts from an empty device set, since the in-memory database is shared")

	dev := &models.MQTTDevice{
		Name:         "relay",
		Control:      "boolean",
		Transport:    mqttrelay.TransportESPHome,
		EntityKey:    "relay_1",
		EntityType:   "switch",
		ConnectionID: uuid.New(),
	}
	require.NoError(t, db.DB.Create(dev).Error, "the runner loads devices from the database")

	node := &entityRecorder{}
	runner := mqttrelay.NewDevices(func(uuid.UUID) (any, bool) { return node, true }, zap.NewNop())
	runner.Reload()
	t.Cleanup(runner.Stop)
	prev := mqttrelay.D
	mqttrelay.D = runner
	t.Cleanup(func() { mqttrelay.D = prev })

	cfg := `{"device":"` + dev.ID.String() + `","source":"value","value":"true"}`
	out, err := mqttnodes.DeviceSetNode{}.Execute(context.Background(), deviceCtx{cfg: cfg},
		map[string]*engine.Msg{"in": {}})
	require.NoError(t, err, "a writable entity device must take the command")
	require.NotEmpty(t, out["out"], "the message passes through on success")

	assert.Equal(t, "relay_1", node.objectID, "the command names the entity by its object ID")
	assert.Equal(t, true, node.value, "the value reaches the connection coerced to the boolean kind")
}
