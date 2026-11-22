package mqttrelay

import (
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stateTestDevice stores a device row and returns a persisting runner and an
// unsubscribed running device for it, so receive and the restore path run
// against the real table without a broker.
func stateTestDevice(t *testing.T, kind string) (*Devices, *device) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	m := models.MQTTDevice{ID: uuid.New(), Name: "thermostat-" + uuid.NewString()[:8], ConnectionID: uuid.New(),
		Control: kind, StateScript: `return JSON.parse(payload);`}
	m.SetTopicList([]string{"home/thermostat"})
	require.NoError(t, models.SaveMQTTDevice(&m))
	runner := &Devices{log: zap.NewNop(), running: map[uuid.UUID]*device{}, watchers: map[int]func(DeviceChange){}, persist: true}
	dev := &device{model: m, state: map[string]any{}}
	runner.running[m.ID] = dev
	return runner, dev
}

// restart simulates a server restart for one device: a fresh device for the
// same definition, restored the way start restores it.
func restart(runner *Devices, model models.MQTTDevice) *device {
	dev := &device{model: model, state: map[string]any{}}
	runner.restoreState(dev)
	runner.running = map[uuid.UUID]*device{model.ID: dev}
	return dev
}

// TestDeviceStateSurvivesRestart covers US5: a device's last merged state is
// stored, comes back marked stale with its report time, does not fire device
// triggers on the way back, and stops being stale on the next report.
func TestDeviceStateSurvivesRestart(t *testing.T) {
	runner, dev := stateTestDevice(t, "climate")
	runner.receive(dev, "home/thermostat", []byte(`{"temperature": 20.5, "target": 21}`), false)
	runner.receive(dev, "home/thermostat", []byte(`{"temperature": 20.7}`), false)
	runner.saveDirty()

	var fired int
	runner.Watch(func(DeviceChange) { fired++ })
	restored := restart(runner, dev.model)
	status, running := runner.Status(dev.model.ID)
	require.True(t, running)
	assert.Equal(t, map[string]any{"temperature": 20.7, "target": float64(21)}, status.State,
		"the merged state before the restart is what comes back")
	assert.True(t, status.Stale, "restored state is unconfirmed until the device reports")
	assert.WithinDuration(t, time.Now(), status.ReportedAt, time.Minute, "the report time is the one stored")
	assert.Zero(t, fired, "restoring state is not a report, so it fires no device trigger")

	runner.receive(restored, "home/thermostat", []byte(`{"target": 22}`), false)
	status, _ = runner.Status(dev.model.ID)
	assert.False(t, status.Stale, "a new report confirms the state")
	assert.Equal(t, float64(22), status.State["target"])
	assert.Equal(t, 20.7, status.State["temperature"], "a partial report merges onto the restored values")
	assert.Equal(t, 1, fired, "the live report fires as usual")
}

// TestDeviceStateWritesOnlyWhatChanged covers the coalescing writer: several
// reports between passes become one write, and a pass with nothing new writes
// nothing.
func TestDeviceStateWritesOnlyWhatChanged(t *testing.T) {
	runner, dev := stateTestDevice(t, "climate")
	for i := 0; i < 5; i++ {
		runner.receive(dev, "home/thermostat", []byte(`{"temperature": 20}`), false)
	}
	runner.saveDirty()
	stored, err := models.FindMQTTDeviceState(dev.model.ID)
	require.NoError(t, err)
	first := stored.UpdatedAt

	runner.saveDirty()
	stored, err = models.FindMQTTDeviceState(dev.model.ID)
	require.NoError(t, err)
	assert.Equal(t, first, stored.UpdatedAt, "a pass with no new report leaves the row alone")
}

// TestDeviceStateDroppedForAnotherKind covers a kind change: values stored
// for one control kind are not restored onto another.
func TestDeviceStateDroppedForAnotherKind(t *testing.T) {
	runner, dev := stateTestDevice(t, "climate")
	runner.receive(dev, "home/thermostat", []byte(`{"temperature": 20}`), false)
	runner.saveDirty()

	retyped := dev.model
	retyped.Control = "number"
	restart(runner, retyped)
	status, _ := runner.Status(dev.model.ID)
	assert.Empty(t, status.State, "state from another kind is not restored")
	assert.False(t, status.Stale)
	_, err := models.FindMQTTDeviceState(dev.model.ID)
	assert.Error(t, err, "and it is removed")
}

// TestDeviceStopFlushesOrForgets covers the two ways a device stops: a
// rebuild writes what it has so the new instance restores it, and a deleted
// device writes nothing back.
func TestDeviceStopFlushesOrForgets(t *testing.T) {
	runner, dev := stateTestDevice(t, "climate")
	runner.receive(dev, "home/thermostat", []byte(`{"temperature": 19}`), false)
	runner.stop(dev.model.ID, true)
	stored, err := models.FindMQTTDeviceState(dev.model.ID)
	require.NoError(t, err, "a rebuilt device's state is written before it stops")
	assert.JSONEq(t, `{"temperature":19}`, stored.State)

	require.NoError(t, models.DeleteMQTTDevice(dev.model.ID))
	runner.running[dev.model.ID] = dev
	runner.receive(dev, "home/thermostat", []byte(`{"temperature": 18}`), false)
	runner.stop(dev.model.ID, false)
	_, err = models.FindMQTTDeviceState(dev.model.ID)
	assert.Error(t, err, "a deleted device leaves no state behind")
}
