package tsdbops

import (
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardCreatedDisabledStaysDisabled locks that a forward first saved
// with enabled false is stored that way. The enabled column defaults to true
// and an insert leaves a false to that default, so the forward came back
// enabled and started recording. The row is read again because the insert
// also back-fills the struct it was given.
func TestForwardCreatedDisabledStaysDisabled(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(models.All()...))
	conn := &models.Connection{Name: "Metrics", Kind: "influxdb"}
	require.NoError(t, db.DB.Create(conn).Error)

	saved, err := SaveForward(&models.HistoryForward{
		Name: "Meters", Enabled: false, SourceType: models.ForwardSourceControl,
		Destinations: models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{conn.ID}},
	})
	require.NoError(t, err, "a control forward with one writable destination is valid")
	assert.False(t, saved.Enabled, "the saved forward must report the enabled value it was given")

	stored, err := models.FindHistoryForward(saved.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled, "a forward created disabled must not be stored enabled")
}
