package dashscope

import (
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestDeployedScopeFollowsWidgetRegistration locks that a deployed
// dashboard's cached scope is derived again when the set of loaded widget
// packages changes. A placement grants only while its plugin is loaded, but
// the scope was cached per deployed version with nothing to expire it:
// disabling a plugin left its placements' grants in force, and a dashboard
// first viewed while the plugin was off showed errors for those widgets after
// it was enabled again, until the next deploy or restart.
func TestDeployedScopeFollowsWidgetRegistration(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(models.All()...))
	specs := []widgets.Spec{{Name: "Reading", PackageName: "scope-test-reading", TagName: "scope-test-reading",
		Scope: widgets.WidgetScope{Bindings: "."}}}
	register := func() {
		require.NoError(t, widgets.Register(zap.NewNop(), "scope-test", t.TempDir(), specs), "the plugin's widget registers")
	}
	register()
	t.Cleanup(func() { widgets.Unregister("scope-test") })
	var def models.Widget
	require.NoError(t, db.DB.First(&def, "package_name = ?", "scope-test-reading").Error, "registering wrote the widget row")

	d := &models.Dashboard{ID: uuid.New(), DeployedVersion: 1}
	snapshot, err := (&models.DashboardSnapshot{Widgets: []models.DashboardSnapshotWidget{{
		ID: uuid.New(), WidgetID: def.ID,
		Config: `{"source":"ha","displayOnly":true,"connectionId":"c1","entityId":"sensor.porch"}`,
	}}}).JSON()
	require.NoError(t, err)
	require.NoError(t, db.DB.Create(&models.DashboardVersion{DashboardID: d.ID, Version: 1, Snapshot: snapshot}).Error)
	porch := Capability{Kind: CapHA, Owner: "c1", Ref: "sensor.porch"}
	allows := func() bool {
		scope, err := deployedScope(d)
		require.NoError(t, err, "the deployed version reads")
		return scope.Allows(porch)
	}

	assert.True(t, allows(), "a placement of a loaded widget grants its binding")
	widgets.Unregister("scope-test")
	assert.False(t, allows(), "an unloaded plugin's placement must stop granting at once")
	register()
	assert.True(t, allows(), "the grant returns when the plugin is loaded again")
}

// TestPlacementPatternsGrantOnlyTheirPlacement locks the {dashboard} and
// {widget} placeholders in a plugin's topic and route patterns to the
// placement that declared them. A route keyed by placement, such as a
// calendar's event data, must not open another dashboard's widget to a viewer
// who holds a grant on this one.
func TestPlacementPatternsGrantOnlyTheirPlacement(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(models.All()...))
	specs := []widgets.Spec{{Name: "Agenda", PackageName: "scope-test-agenda", TagName: "scope-test-agenda",
		Scope: widgets.WidgetScope{
			Topics: []string{"agenda:update:{dashboard}:{widget}"},
			API:    []string{"GET /{dashboard}/{widget}/data"},
		}}}
	require.NoError(t, widgets.Register(zap.NewNop(), "scope-test", t.TempDir(), specs), "the plugin's widget registers")
	t.Cleanup(func() { widgets.Unregister("scope-test") })

	placement := models.DashboardWidget{ID: uuid.New(), DashboardID: uuid.New(),
		Widget: models.Widget{PackageName: "scope-test-agenda"}}
	scope := deriveFrom([]models.DashboardWidget{placement}, nil)
	dash, own := placement.DashboardID.String(), placement.ID.String()
	other := uuid.NewString()

	tests := []struct {
		path string
		want bool
		why  string
	}{
		{"/" + dash + "/" + own + "/data", true, "the placement's own data route"},
		{"/" + dash + "/" + other + "/data", false, "another widget of the same dashboard"},
		{"/" + other + "/" + own + "/data", false, "the same widget ID under another dashboard"},
		{"/" + dash + "/" + own + "/refresh", false, "a route the pattern does not name"},
	}
	for _, tc := range tests {
		assert.Equalf(t, tc.want, scope.AllowsPluginAPI("scope-test", "GET", tc.path), "%s: %s", tc.path, tc.why)
	}

	assert.True(t, scope.AllowsTopic("agenda:update:"+dash+":"+own), "the placement's own topic")
	assert.False(t, scope.AllowsTopic("agenda:update:"+dash+":"+other), "another placement's topic")
}
