package autoimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testFollower builds a follower over a fake connection status and clock.
func testFollower(t *testing.T, kind string, removeAfter time.Duration, now *time.Time, up *bool) *follower {
	t.Helper()
	m := New(zap.NewNop())
	m.clock = func() time.Time { return *now }
	f := &follower{m: m, id: uuid.New(), name: "test", kind: kind,
		settings: integrations.AutoImportSettings{Enabled: true, RemoveAfter: removeAfter},
		status: func() integrations.Status {
			if *up {
				return integrations.Status{State: integrations.StateConnected}
			}
			return integrations.Status{State: "disconnected"}
		},
		live: map[string]time.Time{}, skipped: map[string]Skip{}}
	return f
}

// importDevice saves an auto-imported discovery device last reported at.
func importDevice(t *testing.T, f *follower, key string, at time.Time) *models.MQTTDevice {
	t.Helper()
	d, err := mqttrelay.SaveDefinition(uuid.Nil, mqttrelay.Definition{
		Name: key, ConnectionID: f.id, Topics: []string{"t/" + key}, StateScript: "return null;", Control: "text",
	}, mqttrelay.Origin{DiscoveryKey: key, Auto: true})
	require.NoError(t, err)
	require.NoError(t, models.SetMQTTDeviceReportedAt(d.ID, at))
	return d
}

// exists reports whether a device is still stored.
func exists(id uuid.UUID) bool {
	_, err := models.FindMQTTDeviceByID(id)
	return err == nil
}

// TestRemovalClock pins when "Remove after" removes a device. With a 2h
// setting: a broker connection that came up at 10:00 restarts the clock, so
// a device last heard at 09:00 survives until 12:00, not 11:00; a sweep while
// the broker is down removes nothing; an ESPHome node's own downtime counts.
// A removed MQTT device leaves a tombstone that a retained replay respects
// and live activity clears.
func TestRemovalClock(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())

	base := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	now, up := base, true
	f := testFollower(t, mqtt.Kind, 2*time.Hour, &now, &up)
	d := importDevice(t, f, "switch/plug", base.Add(-time.Hour))

	f.sweep()
	assert.True(t, exists(d.ID), "the first sweep after connecting starts the clock at 10:00")

	now = base.Add(time.Hour + 59*time.Minute)
	f.sweep()
	assert.True(t, exists(d.ID), "11:59 is 1h59m after the connection came up, inside 2h")

	up = false
	now = base.Add(5 * time.Hour)
	f.sweep()
	assert.True(t, exists(d.ID), "time while the broker is down never counts")

	up = true
	f.sweep()
	now = base.Add(5*time.Hour + 2*time.Hour + time.Minute)
	f.sweep()
	assert.False(t, exists(d.ID), "2h01m after the broker came back at 15:00, the silent device is removed")

	stones, err := models.ListAutoImportTombstones(f.id)
	require.NoError(t, err)
	assert.True(t, stones[discoveryKey("switch/plug")], "a removed MQTT device leaves a tombstone")
	mf := &mqttFollower{f: f}
	assert.False(t, mf.allowed(discoveryKey("switch/plug"), false), "a retained replay does not bring it back")
	assert.True(t, mf.allowed(discoveryKey("switch/plug"), true), "live activity does, and clears the tombstone")

	node := testFollower(t, esphome.Kind, 2*time.Hour, &now, &up)
	up = false
	e := importDevice(t, node, "sensor/temp", now.Add(-3*time.Hour))
	node.sweep()
	assert.False(t, exists(e.ID), "an ESPHome connection is the node itself, so its downtime counts")
}
