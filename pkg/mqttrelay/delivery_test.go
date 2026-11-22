package mqttrelay

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// liveRunner is a device runner resolving every connection to a live driver.
func liveRunner(t *testing.T, driver *mqtt.Driver) *Devices {
	t.Helper()
	runner := &Devices{
		resolve: func(uuid.UUID) (any, bool) { return driver, true },
		log:     zap.NewNop(),
		running: map[uuid.UUID]*device{},
	}
	t.Cleanup(runner.Stop)
	return runner
}

// captured subscribes a filter fresh and returns the first message, which is
// the retained one when the broker holds it.
func captured(t *testing.T, driver *mqtt.Driver, filter string) mqtt.Message {
	t.Helper()
	got := make(chan mqtt.Message, 1)
	unsub, err := driver.SubscribeMessages(filter, 2, func(m mqtt.Message) {
		select {
		case got <- m:
		default:
		}
	})
	require.NoError(t, err)
	defer unsub()
	select {
	case m := <-got:
		return m
	case <-time.After(5 * time.Second):
		require.FailNow(t, "nothing was retained on "+filter)
		return mqtt.Message{}
	}
}

// TestCommandDelivery covers the QoS and retain flag commands are published
// with: the device's settings, a payload script's choice for one message, a
// refusal of a QoS the script cannot ask for, and the event log entry carrying
// the delivery used.
func TestCommandDelivery(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(&models.Event{}))
	events.R = events.NewRecorder(nil, nil)
	events.R.Start()
	t.Cleanup(func() { events.R = nil })
	driver := liveDriver(t)
	runner := liveRunner(t, driver)
	device := &models.MQTTDevice{ID: uuid.New(), Name: "Delivery test", CommandQoS: 1, CommandRetain: true,
		PayloadScript: `if (field === "plain") return {topic: "t/plain", payload: String(value), retain: false};
if (field === "bad") return {topic: "t/bad", payload: "x", qos: 3};
return {topic: "t/" + field, payload: String(value)};`}
	ctx := context.Background()

	result, err := runner.Command(ctx, device, "level", 5, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, byte(1), result.QoS)
	assert.True(t, result.Retain)
	held := captured(t, driver, "t/level")
	assert.True(t, held.Retained, "the command was published retained")
	assert.Equal(t, byte(1), held.QoS, "and at the device's QoS")

	result, err = runner.Command(ctx, device, "plain", 1, nil, nil)
	require.NoError(t, err)
	assert.False(t, result.Retain, "the script's retain wins for its message")
	assert.Equal(t, byte(1), result.QoS)

	_, err = runner.Command(ctx, device, "bad", 1, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "qos 3; it must be 0, 1, or 2")

	// Clear the retained command the test left.
	require.NoError(t, driver.Publish(ctx, "t/level", 1, true, nil))

	events.R.Stop()
	var rows []models.Event
	require.NoError(t, db.DB.Order("id").Find(&rows).Error)
	require.Len(t, rows, 2, "the two sent commands are logged")
	assert.Equal(t, float64(1), rows[0].Details["qos"])
	assert.Equal(t, true, rows[0].Details["retain"])
	assert.Equal(t, false, rows[1].Details["retain"], "the log carries the retain the message was sent with")
}

// TestStateScriptSeesRetained covers the flag a state script reads: a message
// waiting in the retained store when the device subscribes is retained, one
// published afterwards is not.
func TestStateScriptSeesRetained(t *testing.T) {
	driver := liveDriver(t)
	runner := liveRunner(t, driver)
	ctx := context.Background()
	require.NoError(t, driver.Publish(ctx, "dev/button", 1, true, []byte("single")))

	model := models.MQTTDevice{ID: uuid.New(), Name: "button", StateQoS: 1,
		StateScript: `return { [retained ? "replayed" : "live"]: payload };`}
	model.SetTopicList([]string{"dev/button"})
	runner.start(model)
	waitSubscribed(t, driver, "dev/button")

	wait := func(key string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if state, _ := runner.State(model.ID); state[key] != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		require.FailNow(t, "the state script never reported "+key)
	}
	wait("replayed")
	require.NoError(t, driver.Publish(ctx, "dev/button", 1, false, []byte("double")))
	wait("live")
	state, _ := runner.State(model.ID)
	assert.Equal(t, "single", state["replayed"])
	assert.Equal(t, "double", state["live"])
	require.NoError(t, driver.Publish(ctx, "dev/button", 1, true, nil))
}

// TestStateQoSChangeResubscribes covers a saved state QoS reaching the
// broker: a running device whose only change is its QoS subscribes again at
// the new one.
func TestStateQoSChangeResubscribes(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	driver := liveDriver(t)
	runner := liveRunner(t, driver)
	runner.watchers = map[int]func(DeviceChange){}
	model := models.MQTTDevice{ID: uuid.New(), Name: "qos test", ConnectionID: uuid.New(),
		StateScript: `return { value: payload };`}
	model.SetTopicList([]string{"dev/qos"})
	require.NoError(t, models.SaveMQTTDevice(&model))
	runner.Reload()
	waitSubscribed(t, driver, "dev/qos")
	granted, _ := runner.GrantedQoS(&model)
	require.Equal(t, 0, granted)

	model.StateQoS = 1
	require.NoError(t, models.SaveMQTTDevice(&model))
	runner.Reload()
	deadline := time.Now().Add(5 * time.Second)
	for granted != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		granted, _ = runner.GrantedQoS(&model)
	}
	assert.Equal(t, 1, granted, "the broker never saw the new QoS")
}

// TestScriptTimeLimits covers a device's own limits: a slow script fails
// under the default and succeeds under a raised limit, and a limit outside its
// range is refused on save and for a script test.
func TestScriptTimeLimits(t *testing.T) {
	slow := `const end = Date.now() + 400; while (Date.now() < end) {} return { done: true };`
	_, _, err := TestStateScriptWith(context.Background(), slow, "t", "x", nil, false, 0)
	require.Error(t, err, "400 ms is past the default of 250")
	got, _, err := TestStateScriptWith(context.Background(), slow, "t", "x", nil, false, time.Second)
	require.NoError(t, err)
	assert.Equal(t, true, got["done"])

	slowPayload := `const end = Date.now() + 400; while (Date.now() < end) {} return {topic: "t", payload: "x"};`
	_, _, err = RunPayloadScriptWithin(context.Background(), slowPayload, map[string]any{}, 300*time.Millisecond)
	require.Error(t, err)
	_, _, err = RunPayloadScriptWithin(context.Background(), slowPayload, map[string]any{}, time.Second)
	require.NoError(t, err)

	base := Definition{Name: "d", Topics: []string{"t"}, StateScript: "return null;"}
	for _, tc := range []struct {
		name string
		edit func(*Definition)
		err  string
	}{
		{"state limit too low", func(d *Definition) { d.StateTimeoutMS = 10 }, "state_timeout_ms must be 0 for the default, or 50 to 5000"},
		{"payload limit too high", func(d *Definition) { d.PayloadTimeoutMS = 20000 }, "payload_timeout_ms must be 0 for the default, or 100 to 10000"},
		{"qos 3", func(d *Definition) { d.CommandQoS = 3 }, "command_qos must be 0, 1, or 2"},
		{"in range", func(d *Definition) { d.StateTimeoutMS, d.PayloadTimeoutMS, d.StateQoS = 5000, 100, 2 }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := base
			tc.edit(&def)
			err := ValidateDefinition(def)
			if tc.err == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
	assert.Error(t, CheckTestTimeout("state", 600000), "a test runs under no limit a device could not set")
	assert.Error(t, CheckTestTimeout("payload", 50))
	assert.NoError(t, CheckTestTimeout("payload", 10000))
	assert.NoError(t, CheckTestTimeout("state", 0))
}
