package eventsources

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) func() []models.Event {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "e.db"), MaxOpenConns: 2, MaxIdleConns: 2}))
	require.NoError(t, db.DB.AutoMigrate(&models.Event{}, &models.Setting{}))
	events.R = events.NewRecorder(nil, nil)
	events.R.Start()
	t.Cleanup(func() { events.R = nil })
	return func() []models.Event {
		events.R.Stop()
		var rows []models.Event
		require.NoError(t, db.DB.Order("id").Find(&rows).Error)
		return rows
	}
}

// TestAvailabilityEntries covers US1 scenario 4.
func TestAvailabilityEntries(t *testing.T) {
	read := setup(t)
	id := uuid.New()
	availabilityChanged(mqttrelay.AvailabilityChange{DeviceID: id, Name: "Garage sensor", Previous: "online", Current: "offline", At: time.Now()})
	availabilityChanged(mqttrelay.AvailabilityChange{DeviceID: id, Name: "Garage sensor", Previous: "offline", Current: "online", At: time.Now()})
	rows := read()
	require.Len(t, rows, 2)
	assert.Equal(t, "Garage sensor went offline", rows[0].Message)
	assert.Equal(t, models.EventWarning, rows[0].Level)
	assert.Equal(t, "Garage sensor is back online", rows[1].Message)
	assert.Equal(t, models.EventInfo, rows[1].Level)
}

// TestHAEntitiesAreOptIn covers FR-005: only listed entities are logged, and
// only when their state changes.
func TestHAEntitiesAreOptIn(t *testing.T) {
	read := setup(t)
	broker := mq.B
	s := events.Defaults()
	s.HAEntities = []events.HAEntity{{ConnectionID: "c1", EntityID: "binary_sensor.front_door"}}
	_, err := events.SaveSettings(s)
	require.NoError(t, err)
	src := Start(broker, nil)
	defer src.Stop()

	state := func(v string) map[string]any {
		return map[string]any{"state": v, "attributes": map[string]any{"friendly_name": "Front door"}}
	}
	broker.Publish("ha:c1:state:binary_sensor.front_door", "ha_state", map[string]any{"old_state": state("off"), "new_state": state("on")})
	broker.Publish("ha:c1:state:binary_sensor.front_door", "ha_state", map[string]any{"old_state": state("on"), "new_state": state("on")})
	broker.Publish("ha:c1:state:sensor.temperature", "ha_state", map[string]any{"old_state": state("20"), "new_state": state("21")})
	time.Sleep(50 * time.Millisecond)

	rows := read()
	require.Len(t, rows, 1, "an attribute-only update and an unlisted entity are not logged")
	assert.Equal(t, "Front door changed to on", rows[0].Message)
	assert.Equal(t, "c1/binary_sensor.front_door", rows[0].SubjectID)
}

// TestConnectionFlappingFolds covers SC-003: a connection failing a hundred
// times in a minute, with connecting between each attempt, leaves a handful of
// entries with counts, and its recovery is its own entry.
func TestConnectionFlappingFolds(t *testing.T) {
	read := setup(t)
	require.NoError(t, db.DB.AutoMigrate(&models.Connection{}, &models.Flow{}))
	conn := &models.Connection{ID: uuid.New(), Name: "Garage MQTT", Kind: "mqtt"}
	require.NoError(t, db.DB.Create(conn).Error)
	src := Start(mq.B, nil)
	defer src.Stop()

	topic := "connection:" + conn.ID.String() + ":status"
	mq.B.Publish(topic, "connection_status", map[string]any{"connection_id": conn.ID.String(), "state": "connected"})
	for i := 0; i < 100; i++ {
		mq.B.Publish(topic, "connection_status", map[string]any{"connection_id": conn.ID.String(), "state": "error", "error": "connection refused"})
		mq.B.Publish(topic, "connection_status", map[string]any{"connection_id": conn.ID.String(), "state": "connecting"})
	}
	mq.B.Publish(topic, "connection_status", map[string]any{"connection_id": conn.ID.String(), "state": "connected"})
	mq.B.Publish(topic, "connection_status", map[string]any{"connection_id": conn.ID.String(), "state": "connected"})
	time.Sleep(50 * time.Millisecond)

	rows := read()
	require.Len(t, rows, 3, "connected, the failures folded, reconnected")
	assert.Equal(t, "Garage MQTT connected", rows[0].Message)
	assert.Equal(t, "Garage MQTT failed: connection refused", rows[1].Message)
	assert.Equal(t, 100, rows[1].Count)
	assert.Equal(t, models.EventError, rows[1].Level)
	assert.Equal(t, "Garage MQTT reconnected", rows[2].Message)
}

// TestFailedRunsAreLinked covers US3 scenario 2.
func TestFailedRunsAreLinked(t *testing.T) {
	read := setup(t)
	require.NoError(t, db.DB.AutoMigrate(&models.User{}, &models.Flow{}))
	owner := &models.User{ID: uuid.New(), Username: "owner", Email: "owner@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)
	flowID := uuid.New()
	require.NoError(t, db.DB.Create(&models.Flow{ID: flowID, Name: "Motion lights", Slug: "motion-lights", OwnerID: owner.ID}).Error)
	src := Start(mq.B, nil)
	defer src.Stop()

	topic := "flow:" + flowID.String() + ":runs"
	mq.B.Publish(topic, "run_finished", map[string]any{"run": map[string]any{"id": "r1", "flow_id": flowID.String(), "status": "succeeded"}})
	mq.B.Publish(topic, "run_finished", map[string]any{"run": map[string]any{"id": "r2", "flow_id": flowID.String(), "status": "failed", "error": "Send notification: timeout"}})
	time.Sleep(50 * time.Millisecond)

	rows := read()
	require.Len(t, rows, 1, "only failures are events")
	assert.Equal(t, "Motion lights failed: Send notification: timeout", rows[0].Message)
	assert.Equal(t, "r2", rows[0].RunID)
}

// TestRepeatedPressesAreLogged pins that every press of a buttons control
// reaches the event log. A press is a change even when the same button is
// pressed again, so comparing old and new values dropped the second press of
// "Up" and every one after it. A settings edit announces the control with its
// value unmoved too, and stays out of the log, as does a state control whose
// value did not move.
func TestRepeatedPressesAreLogged(t *testing.T) {
	read := setup(t)
	blinds := controls.Snapshot{ID: uuid.New(), Name: "Blinds", Kind: controls.KindButtons, Value: "up"}
	away := controls.Snapshot{ID: uuid.New(), Name: "Away", Kind: controls.KindBoolean, Value: true}
	at := time.Now()

	controlChanged(controls.Change{Control: blinds, Old: "", Source: controls.SourceDashboard, At: at})
	controlChanged(controls.Change{Control: blinds, Old: "up", Source: controls.SourceDashboard, At: at.Add(time.Second)})
	controlChanged(controls.Change{Control: blinds, Old: "up", Source: controls.SourceAPI, Definition: true, At: at.Add(2 * time.Second)})
	controlChanged(controls.Change{Control: away, Old: true, Source: controls.SourceAPI, Definition: true, At: at.Add(3 * time.Second)})

	rows := read()
	presses := 0
	for _, row := range rows {
		assert.Equal(t, "Blinds set to up", row.Message, "Only the presses are events; the edits moved no value.")
		presses += row.Count
	}
	assert.Equal(t, 2, presses, "Both presses of the same button are in the log.")
}
