package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/version"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestStartRefusesOlderAndCopiesBeforeNewer locks the two things a version
// change at start-up does to an existing database. A release older than the
// one that last ran refuses to start unless allow_downgrade is set, since the
// newer release may have changed data the older one cannot read. A newer
// release copies the database before it migrates, once per upgrade, so a
// release restarted by its service manager or by a container's restart policy
// does not pile up copies. Without a backup passphrase a SQLite database is
// copied with VACUUM INTO.
func TestStartRefusesOlderAndCopiesBeforeNewer(t *testing.T) {
	previous, previousVersion, previousStart := config.C, version.Version, startUpgrade
	t.Cleanup(func() { config.C, version.Version, startUpgrade = previous, previousVersion, previousStart })
	config.C = &config.Config{
		Database: config.DatabaseConfig{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "karakuri.db"), MaxOpenConns: 1, MaxIdleConns: 1},
		Security: config.SecurityConfig{EncryptionKey: config.DefaultEncryptionKey},
	}
	require.NoError(t, initDatabaseQuiet(), "the test needs a migrated database")
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	dataDir := t.TempDir()
	version.Version = "1.2.0"

	t.Run("an older release refuses a newer release's database", func(t *testing.T) {
		startUpgrade = upgradeStart{}
		require.NoError(t, models.SetSetting(migrate.VersionSetting, "1.3.0"))
		err := prepareUpgrade(zap.NewNop(), dataDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "1.3.0", "the refusal names the release that last ran")

		config.C.Update.AllowDowngrade = true
		t.Cleanup(func() { config.C.Update.AllowDowngrade = false })
		require.NoError(t, prepareUpgrade(zap.NewNop(), dataDir), "allow_downgrade starts it anyway")
		assert.Equal(t, "1.3.0", startUpgrade.Downgrade, "the downgrade is reported once the server serves")
	})

	t.Run("a newer release copies the database once per upgrade", func(t *testing.T) {
		startUpgrade = upgradeStart{}
		require.NoError(t, models.SetSetting(migrate.VersionSetting, "1.1.0"))
		require.NoError(t, prepareUpgrade(zap.NewNop(), dataDir))
		assert.Equal(t, "1.1.0", startUpgrade.From)
		require.NoError(t, startUpgrade.BackupErr)
		require.FileExists(t, startUpgrade.Backup, "a SQLite database is copied without a passphrase")
		assert.Equal(t, filepath.Join(dataDir, "pre-update"), filepath.Dir(startUpgrade.Backup))

		// Read the copy back from disk: it is the database as 1.1.0 left it.
		copied, err := db.Open(db.Options{Type: "sqlite", DSN: startUpgrade.Backup, MaxOpenConns: 1, MaxIdleConns: 1})
		require.NoError(t, err)
		var stored models.Setting
		require.NoError(t, copied.Where("key = ?", migrate.VersionSetting).First(&stored).Error)
		assert.Equal(t, "1.1.0", stored.Value, "the copy holds the previous release's record")
		if sqlDB, err := copied.DB(); err == nil {
			_ = sqlDB.Close()
		}

		first := startUpgrade.Backup
		startUpgrade = upgradeStart{}
		require.NoError(t, prepareUpgrade(zap.NewNop(), dataDir), "a restart of the same upgrade starts")
		assert.Equal(t, first, startUpgrade.Backup, "a restart reuses the copy already taken")
		entries, err := os.ReadDir(filepath.Join(dataDir, "pre-update"))
		require.NoError(t, err)
		assert.Len(t, entries, 1, "a restart of the same upgrade takes no second copy")
	})
}
