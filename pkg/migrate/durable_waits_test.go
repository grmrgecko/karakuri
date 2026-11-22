package migrate

import (
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDurableWaitTablesAreAdditive covers SC-006: a database from before
// durable waits gains the new tables and run column on startup, and the
// durable node state, variables, and control values already in it read back
// unchanged.
func TestDurableWaitTablesAreAdditive(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, AutoMigrate())

	// Shape the database the way an older server left it.
	m := db.DB.Migrator()
	require.NoError(t, m.DropTable(&models.PendingWait{}, &models.MQTTDeviceState{}))
	require.NoError(t, m.DropColumn(&models.Run{}, "resumed_from_run_id"))

	flowID := uuid.New()
	entry := models.NodeStoreEntry{FlowID: flowID, NodeID: "dedupe", Key: "seen", Value: `["a","b"]`}
	require.NoError(t, db.DB.Create(&entry).Error)
	variable := models.Variable{Scope: "flow", FlowID: flowID, Key: "mode", Value: `"away"`}
	require.NoError(t, db.DB.Create(&variable).Error)
	control := models.Control{ID: uuid.New(), Name: "House mode", Kind: "select", State: `"home"`}
	require.NoError(t, db.DB.Create(&control).Error)

	require.NoError(t, AutoMigrate(), "an older database upgrades without manual steps")

	assert.True(t, m.HasTable(&models.PendingWait{}), "pending waits get their table")
	assert.True(t, m.HasTable(&models.MQTTDeviceState{}), "device state gets its table")
	assert.True(t, m.HasColumn(&models.Run{}, "resumed_from_run_id"), "runs gain the resumed-from link")

	var gotEntry models.NodeStoreEntry
	require.NoError(t, db.DB.First(&gotEntry, entry.ID).Error)
	assert.Equal(t, entry.Value, gotEntry.Value, "durable node state is untouched")
	var gotVar models.Variable
	require.NoError(t, db.DB.First(&gotVar, variable.ID).Error)
	assert.Equal(t, variable.Value, gotVar.Value, "flow variables are untouched")
	var gotControl models.Control
	require.NoError(t, db.DB.First(&gotControl, "id = ?", control.ID).Error)
	assert.Equal(t, control.State, gotControl.State, "control values are untouched")
}
