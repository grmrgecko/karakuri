package zigbee2mqtt

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSyncFollowsRenamesAndRemovals covers the device list watcher: a rename
// moves the generated topics and nothing else, a hand-edited script stays,
// a device that leaves the list is marked missing, not deleted, and its
// return clears the mark.
func TestSyncFollowsRenamesAndRemovals(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())

	devices := loadFixture(t, "synthetic.json")
	conn := uuid.New()
	door := byName(t, devices, "Back door")
	def, origin := DefinitionFor(door, conn, "karakuri-test")
	def.Topics = append(def.Topics, "karakuri-test/Back door/extra")
	saved, err := mqttrelay.SaveDefinition(uuid.Nil, def, origin)
	require.NoError(t, err)

	edited := mqttrelay.DefinitionOf(saved)
	edited.StateScript = "return JSON.parse(payload); // hand edit"
	_, err = mqttrelay.SaveDefinition(saved.ID, edited, mqttrelay.Origin{})
	require.NoError(t, err)

	sync := NewSync(func(uuid.UUID) (Subscriber, bool) { return nil, false }, zap.NewNop())
	key := syncKey{conn: conn, base: "karakuri-test"}
	publish := func(list []Device) {
		raw, err := json.Marshal(list)
		require.NoError(t, err)
		sync.apply(key, raw)
	}
	reload := func() *models.MQTTDevice {
		m, err := models.FindMQTTDeviceByID(saved.ID)
		require.NoError(t, err)
		return m
	}

	renamed := append([]Device{}, devices...)
	for i := range renamed {
		if renamed[i].IEEEAddress == door.IEEEAddress {
			renamed[i].FriendlyName = "Garden door"
		}
	}
	publish(renamed)
	m := reload()
	assert.Equal(t, []string{"karakuri-test/Garden door", "karakuri-test/Back door/extra"}, m.TopicList(),
		"the generated topic follows; a topic added by hand stays as written")
	assert.Equal(t, "karakuri-test/Garden door/availability", m.AvailabilityTopic)
	assert.Equal(t, "Back door", m.Name, "the karakuri name is the author's")
	assert.Contains(t, m.StateScript, "hand edit", "a rename keeps hand edits")
	assert.False(t, m.Missing)

	var without []Device
	for _, d := range renamed {
		if d.IEEEAddress != door.IEEEAddress {
			without = append(without, d)
		}
	}
	publish(without)
	assert.True(t, reload().Missing, "gone from the list is missing, not deleted")

	publish(renamed)
	assert.False(t, reload().Missing, "back in the list clears the mark")
}

// TestSyncFollowsRenameWithoutAvailabilityTopic locks that a device whose
// availability topic was cleared still follows a rename. Zigbee2MQTT ships
// with availability off, and such a device is given an expected reporting
// interval in place of the topic. The old friendly name was read back out of
// the availability topic alone, so that device kept listening on the state
// topic of a name Zigbee2MQTT no longer published on and went silent.
func TestSyncFollowsRenameWithoutAvailabilityTopic(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())

	devices := loadFixture(t, "synthetic.json")
	conn := uuid.New()
	door := byName(t, devices, "Back door")
	def, origin := DefinitionFor(door, conn, "karakuri-test")
	def.AvailabilityTopic = ""
	def.ExpectEveryS = 3600
	saved, err := mqttrelay.SaveDefinition(uuid.Nil, def, origin)
	require.NoError(t, err)

	sync := NewSync(func(uuid.UUID) (Subscriber, bool) { return nil, false }, zap.NewNop())
	key := syncKey{conn: conn, base: "karakuri-test"}
	publish := func(list []Device) {
		raw, err := json.Marshal(list)
		require.NoError(t, err)
		sync.apply(key, raw)
	}

	// The list as it stood when the device was added, which Zigbee2MQTT
	// retains and the sync reads when it subscribes.
	publish(devices)
	renamed := append([]Device{}, devices...)
	for i := range renamed {
		if renamed[i].IEEEAddress == door.IEEEAddress {
			renamed[i].FriendlyName = "Garden door"
		}
	}
	publish(renamed)

	m, err := models.FindMQTTDeviceByID(saved.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"karakuri-test/Garden door"}, m.TopicList(), "the state topic follows the rename")
	assert.Empty(t, m.AvailabilityTopic, "a topic the author cleared is not put back")
	assert.Equal(t, 3600, m.ExpectEveryS)
}
