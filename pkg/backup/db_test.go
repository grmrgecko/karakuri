package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	// age's default scrypt cost takes about a second per archive; the tests
	// open dozens.
	scryptWorkFactor = 10
	os.Exit(m.Run())
}

// openFile opens a WAL SQLite database file the way the server does.
func openFile(t *testing.T, path string) *gorm.DB {
	t.Helper()
	gdb, err := db.Open(db.Options{Type: "sqlite", DSN: path, MaxOpenConns: 4, MaxIdleConns: 4})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return gdb
}

// populated makes db.DB a seeded installation database with the rows whose
// values a careless copy would change: a disabled connection and agent (both
// default to enabled), a soft-deleted folder, a password hash the API never
// shows, a time with nanoseconds, a nullable foreign key, and an empty string
// in a json column, which SQLite keeps and PostgreSQL and MySQL refuse.
func populated(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: path, MaxOpenConns: 4, MaxIdleConns: 4}))
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
	require.NoError(t, migrate.AutoMigrate())
	require.NoError(t, migrate.SeedDefaults())

	stamp := time.Date(2026, 11, 5, 3, 0, 0, 123456789, time.UTC)
	user := &models.User{ID: uuid.New(), Username: "ada", Email: "ada@example.test", PasswordHash: "$2a$10$hash", CreatedAt: stamp, UpdatedAt: stamp}
	require.NoError(t, db.DB.Create(user).Error)
	conn := &models.Connection{ID: uuid.New(), Name: "Off", Kind: "mqtt", Config: "sealed-config", CreatedAt: stamp, UpdatedAt: stamp}
	require.NoError(t, db.DB.Create(conn).Error)
	require.NoError(t, db.DB.Model(conn).Update("enabled", false).Error)
	agent := &models.Agent{ID: uuid.New(), Name: "host"}
	require.NoError(t, db.DB.Create(agent).Error)
	require.NoError(t, db.DB.Model(agent).Update("enabled", false).Error)

	parent := &models.Folder{Name: "Parent"}
	require.NoError(t, db.DB.Create(parent).Error)
	child := &models.Folder{Name: "Child", ParentID: &parent.ID}
	require.NoError(t, db.DB.Create(child).Error)
	require.NoError(t, db.DB.Delete(child).Error)
	require.NoError(t, db.DB.Create(&models.Theme{Name: "Plain", PackageName: "plain", Version: "1.0.0", Variables: ""}).Error)
	flow := &models.Flow{ID: uuid.New(), Name: "Lights", Slug: "lights", FolderID: &parent.ID, OwnerID: user.ID}
	require.NoError(t, db.DB.Create(flow).Error)
	return db.DB
}

// dumpTo dumps gdb into dir and returns each table file's contents.
func dumpTo(t *testing.T, gdb *gorm.DB, dir string) (map[string]string, map[string]int64, map[string]int64) {
	t.Helper()
	files := map[string]string{}
	tables, counts, err := dumpDatabase(context.Background(), gdb, t.TempDir(), func(name string, f *os.File) error {
		data, err := os.ReadFile(f.Name())
		if err != nil {
			return err
		}
		files[name] = string(data)
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	require.NoError(t, err)
	return files, tables, counts
}

// TestDatabaseRoundTrip is the contract every restore rests on: a database
// dumped and loaded into an empty one dumps again byte for byte the same, so
// no column, value, or row changed on the way.
func TestDatabaseRoundTrip(t *testing.T) {
	source := populated(t)
	staged := t.TempDir()
	first, tables, counts := dumpTo(t, source, staged)
	assert.Equal(t, int64(2), tables["folders"], "a soft-deleted row is carried")
	assert.Equal(t, int64(1), counts["flows"])
	assert.Equal(t, int64(1), counts["connections"])

	target := openFile(t, filepath.Join(t.TempDir(), "target.db"))
	loaded, err := loadDatabase(context.Background(), target, staged)
	require.NoError(t, err)
	assert.Equal(t, tables, loaded, "every row loads")

	second, _, _ := dumpTo(t, target, t.TempDir())
	require.Equal(t, len(first), len(second))
	for name, data := range first {
		assert.Equal(t, data, second[name], "%s survives the round trip unchanged", name)
	}

	var conn models.Connection
	require.NoError(t, target.Where("name = ?", "Off").First(&conn).Error)
	assert.False(t, conn.Enabled, "a disabled connection stays disabled despite its default")
	assert.Equal(t, "sealed-config", conn.Config)
	var user models.User
	require.NoError(t, target.Where("username = ?", "ada").First(&user).Error)
	assert.Equal(t, "$2a$10$hash", user.PasswordHash, "fields the API hides are kept")
	assert.Equal(t, 123456789, user.CreatedAt.Nanosecond())

	var variables *string
	require.NoError(t, target.Raw("SELECT variables FROM themes WHERE package_name = ?", "plain").Scan(&variables).Error)
	assert.Nil(t, variables, "an empty json column loads as NULL, which every engine accepts")
}

// TestDumpIsOneSnapshot covers FR-003's consistency: a write that commits
// while the dump runs is not in it, and the writer is not blocked.
func TestDumpIsOneSnapshot(t *testing.T) {
	source := populated(t)
	writer := openFile(t, filepath.Join(filepath.Dir(dbPath(t, source)), "source.db"))

	written := make(chan error, 1)
	var once bool
	_, tables, _ := func() (map[string]string, map[string]int64, map[string]int64) {
		files := map[string]string{}
		tables, counts, err := dumpDatabase(context.Background(), source, t.TempDir(), func(name string, f *os.File) error {
			if !once {
				once = true
				written <- writer.Create(&models.Setting{Key: "late", Value: "1"}).Error
			}
			return nil
		})
		require.NoError(t, err)
		return files, tables, counts
	}()
	require.NoError(t, <-written, "a writer is not blocked by the dump")

	var now int64
	require.NoError(t, writer.Model(&models.Setting{}).Count(&now).Error)
	assert.Equal(t, now-1, tables["settings"], "the late write is after the snapshot")
}

// TestLoadToleratesOtherVersions covers archives from another build: a column
// the current model lacks is ignored and a missing one gets what a new row would.
func TestLoadToleratesOtherVersions(t *testing.T) {
	staged := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(staged, "db"), 0o700))
	row := `{"id":7,"key":"site","value":"x","retired_column":"gone"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(staged, "db", "settings.jsonl"), []byte(row), 0o600))

	target := openFile(t, filepath.Join(t.TempDir(), "target.db"))
	loaded, err := loadDatabase(context.Background(), target, staged)
	require.NoError(t, err)
	assert.Equal(t, int64(1), loaded["settings"])
	assert.Equal(t, int64(0), loaded["flows"], "a table the archive lacks loads empty")

	var s models.Setting
	require.NoError(t, target.First(&s, 7).Error)
	assert.Equal(t, "x", s.Value)
	assert.False(t, s.CreatedAt.IsZero(), "a column the archive lacks gets what a new row would")
}

// TestDumpReadsAnOlderSchema covers the backup a newer release takes before
// it migrates: the database still has the previous release's schema, which
// lacks tables and columns the current models name. The dump must copy what
// is there and leave the rest out, and the archive must load like one from
// the older release.
func TestDumpReadsAnOlderSchema(t *testing.T) {
	source := populated(t)
	// Stand in for an older release: one table it never had, and one column
	// added to a table it did have.
	require.NoError(t, source.Migrator().DropTable(&models.BackupRun{}))
	require.NoError(t, source.Migrator().DropColumn(&models.Flow{}, "has_faceplates"))

	staged := t.TempDir()
	files, tables, counts := dumpTo(t, source, staged)
	assert.NotContains(t, files, "db/backup_runs.jsonl", "a table the database lacks is left out")
	assert.NotContains(t, files["db/flows.jsonl"], "has_faceplates", "a column the database lacks is left out")
	assert.Contains(t, files["db/flows.jsonl"], `"name":"Lights"`, "the columns it has are copied")
	assert.Equal(t, int64(1), counts["flows"])

	target := openFile(t, filepath.Join(t.TempDir(), "target.db"))
	loaded, err := loadDatabase(context.Background(), target, staged)
	require.NoError(t, err)
	assert.Equal(t, tables["flows"], loaded["flows"], "the older archive loads into the current schema")
	assert.Equal(t, int64(0), loaded["backup_runs"], "the table it lacked loads empty")
}

// TestLoadOrderPutsParentsFirst covers the foreign keys GORM creates on
// PostgreSQL and MySQL: a dashboard references its theme, which registers
// later.
func TestLoadOrderPutsParentsFirst(t *testing.T) {
	order, err := loadOrder(openFile(t, filepath.Join(t.TempDir(), "x.db")))
	require.NoError(t, err)
	pos := map[string]int{}
	for i, m := range order {
		pos[m.schema.Table] = i
	}
	assert.Less(t, pos["themes"], pos["dashboards"])
	assert.Less(t, pos["users"], pos["dashboards"])
	assert.Less(t, pos["dashboards"], pos["dashboard_widgets"])
	assert.Len(t, order, len(models.All()))
}

// dbPath returns the file behind a SQLite handle.
func dbPath(t *testing.T, gdb *gorm.DB) string {
	t.Helper()
	var rows []struct {
		Name string
		File string
	}
	require.NoError(t, gdb.Raw("PRAGMA database_list").Scan(&rows).Error)
	for _, r := range rows {
		if r.Name == "main" {
			return r.File
		}
	}
	t.Fatal("no main database")
	return ""
}
