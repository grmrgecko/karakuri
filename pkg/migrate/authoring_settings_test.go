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

// TestFreezeAuthoringSettings preserves the live settings used by old deployed
// versions and ensures later startups cannot replace that baseline with edits.
func TestFreezeAuthoringSettings(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, AutoMigrate())
	owner := models.User{Username: "author", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(&owner).Error)
	f := models.Flow{Name: "Existing flow", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(&f).Error)
	v := models.FlowVersion{FlowID: f.ID, Version: 1, Graph: `{"nodes":[],"edges":[]}`}
	require.NoError(t, db.DB.Create(&v).Error)
	d := models.Dashboard{ID: uuid.New(), Name: "Existing dashboard", Slug: "existing", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(&d).Error)
	dv := models.DashboardVersion{DashboardID: d.ID, Version: 1, Snapshot: `{"grid_rows":6,"grid_cols":8,"widgets":[]}`}
	require.NoError(t, db.DB.Create(&dv).Error)
	require.NoError(t, AutoMigrate())
	require.NoError(t, db.DB.Model(&f).Update("name", "Draft title").Error)
	require.NoError(t, db.DB.Model(&d).Update("name", "Draft dashboard title").Error)
	require.NoError(t, AutoMigrate())
	stored, err := models.FindFlowVersionByID(v.ID)
	require.NoError(t, err)
	assert.Equal(t, "Existing flow", stored.Name)
	assert.True(t, stored.HasSettings)
	assert.Equal(t, v.Graph, stored.Graph)
	storedDashboard, err := models.FindDashboardVersion(d.ID, 1)
	require.NoError(t, err)
	snapshot, err := models.ParseDashboardSnapshot(storedDashboard.Snapshot)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Presentation)
	assert.Equal(t, "Existing dashboard", snapshot.Presentation.Name)
	assert.Equal(t, 8, snapshot.GridCols)
}
