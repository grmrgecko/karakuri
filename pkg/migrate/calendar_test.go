package migrate

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/grmrgecko/karakuri/internal/config"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// TestMigrateCalendarFeeds covers US4: inline feeds in drafts, deployed
// versions, and widget sources move into shared, encrypted connections; the
// trigger's lead time becomes a start offset; widget sources keep their
// labels; drafts gain the connection dependency; and a second run changes
// nothing.
func TestMigrateCalendarFeeds(t *testing.T) {
	require.NoError(t, config.Load(), "connections are encrypted with the configured key")
	if config.C.Security.EncryptionKey == "" {
		old := config.C.Security.EncryptionKey
		config.C.Security.EncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
		t.Cleanup(func() { config.C.Security.EncryptionKey = old })
	}
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, AutoMigrate())

	owner := models.User{Username: "author", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(&owner).Error)
	f := models.Flow{Name: "Mode overrides", OwnerID: owner.ID}
	require.NoError(t, db.DB.Create(&f).Error)
	familyFeed := "https://feeds.example.com/family.ics"
	graph := func(nodeType string, cfg map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"nodes": []any{map[string]any{"id": "cal", "type": nodeType, "config": cfg}},
			"edges": []any{},
		})
		return string(raw)
	}
	deployed := models.FlowVersion{FlowID: f.ID, Version: 1,
		Graph: graph("calendar-events", map[string]any{"provider": "ics", "url": familyFeed, "window_days": 3})}
	require.NoError(t, db.DB.Create(&deployed).Error)
	draft := models.FlowVersion{FlowID: f.ID, Version: 0,
		Graph: graph("calendar-event", map[string]any{"provider": "ics", "url": familyFeed, "lead_time_m": 15, "match": "Vacation"})}
	require.NoError(t, db.DB.Create(&draft).Error)
	require.NoError(t, db.DB.Model(&f).Updates(map[string]any{"draft_version_id": draft.ID, "deployed_version_id": deployed.ID}).Error)
	broken := models.FlowVersion{FlowID: f.ID, Version: 2, Graph: `{"nodes": [calendar-event`}
	require.NoError(t, db.DB.Create(&broken).Error)

	sources, _ := json.Marshal([]map[string]any{
		{"provider": "ics", "url": familyFeed, "name": "Family", "color": "#89b4fa", "tag": "family", "api_key": ""},
		{"provider": "google", "url": "en.usa#holiday@group.v.calendar.google.com", "name": "Holidays", "color": "#a6e3a1", "api_key": "AIzaSecret"},
	})
	widgetKey := "plugins.calendar." + uuid.NewString() + "." + uuid.NewString() + ".sources"
	require.NoError(t, db.DB.Create(&models.Setting{Key: widgetKey, Value: string(sources)}).Error)

	MigrateCalendarFeeds(zap.NewNop())

	var conns []models.Connection
	require.NoError(t, db.DB.Order("name").Find(&conns).Error)
	require.Len(t, conns, 2, "the family feed is shared by the nodes and the widget")
	byKind := map[string]models.Connection{}
	for _, c := range conns {
		byKind[c.Kind] = c
	}
	ics, google := byKind["calendar-ics"], byKind["calendar-google"]
	assert.Equal(t, "Calendar: Family", ics.Name, "the widget's label names the shared connection")
	assert.Equal(t, "Calendar: Holidays", google.Name)
	cfg, err := google.ConfigJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{"calendars":"en.usa#holiday@group.v.calendar.google.com","auth":"api_key","api_key":"AIzaSecret"}`, string(cfg),
		"the API key moved into the encrypted connection")

	var gotDraft, gotDeployed models.FlowVersion
	require.NoError(t, db.DB.First(&gotDraft, draft.ID).Error)
	require.NoError(t, db.DB.First(&gotDeployed, deployed.ID).Error)
	assert.JSONEq(t, graph("calendar-event", map[string]any{"connection": ics.ID.String(), "edges": "start", "start_offset": "-15m", "match": "Vacation"}), gotDraft.Graph,
		"the lead time became a negative start offset and the inline feed is gone")
	assert.JSONEq(t, graph("calendar-events", map[string]any{"connection": ics.ID.String(), "window_days": 3}), gotDeployed.Graph,
		"deployed versions are rewritten too, so no credential is left in one")

	var gotBroken models.FlowVersion
	require.NoError(t, db.DB.First(&gotBroken, broken.ID).Error)
	assert.Equal(t, broken.Graph, gotBroken.Graph, "a version that cannot be read is left as it was")

	var deps []models.FlowDependency
	require.NoError(t, db.DB.Where("flow_id = ?", f.ID).Find(&deps).Error)
	require.Len(t, deps, 1, "the draft depends on its new connection, so the connection cannot be deleted from under it")
	assert.Equal(t, ics.ID.String(), deps[0].RefID)

	var setting models.Setting
	require.NoError(t, db.DB.Where("key = ?", widgetKey).First(&setting).Error)
	assert.JSONEq(t, `[
		{"connection_id": "`+ics.ID.String()+`", "name": "Family", "color": "#89b4fa", "tag": "family"},
		{"connection_id": "`+google.ID.String()+`", "name": "Holidays", "color": "#a6e3a1"}
	]`, setting.Value, "widget sources keep their names, colours, and tags, and lose their credentials")

	MigrateCalendarFeeds(zap.NewNop())
	var count int64
	require.NoError(t, db.DB.Model(&models.Connection{}).Count(&count).Error)
	assert.EqualValues(t, 2, count, "a second run creates nothing")
	require.NoError(t, db.DB.First(&gotDraft, draft.ID).Error)
	assert.JSONEq(t, graph("calendar-event", map[string]any{"connection": ics.ID.String(), "edges": "start", "start_offset": "-15m", "match": "Vacation"}), gotDraft.Graph)
}

// TestMigrateCalendarFeedsQuotesKeyOnMySQL locks that the migration names the
// settings key column through the dialect. MySQL reserves the word, so the
// bare column was a syntax error there and the migration stopped before
// moving any feed, flow nodes included.
func TestMigrateCalendarFeedsQuotesKeyOnMySQL(t *testing.T) {
	dry, err := gorm.Open(mysql.New(mysql.Config{DSN: "karakuri@tcp(127.0.0.1:1)/karakuri", SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err, "a dry-run session builds statements without a server")
	var statements []string
	require.NoError(t, dry.Callback().Query().After("gorm:query").Register("test:capture", func(tx *gorm.DB) {
		statements = append(statements, tx.Statement.SQL.String())
	}))
	previous := db.DB
	db.DB = dry
	t.Cleanup(func() { db.DB = previous })

	MigrateCalendarFeeds(zap.NewNop())

	require.Len(t, statements, 2, "the migration reads flow versions, then widget sources")
	assert.Contains(t, statements[1], "`key` LIKE ?", "MySQL refuses the reserved word key as a bare column name")
}
