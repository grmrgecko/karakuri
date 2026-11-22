package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/instlock"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// target is an installation a restore lands in: its own data directory,
// SQLite file, and config file.
type target struct {
	data   string
	dbPath string
	config string
	// failConfig makes the config commit fail, the last step of a restore.
	failConfig bool
}

func newTarget(t *testing.T) *target {
	t.Helper()
	root := t.TempDir()
	tg := &target{data: filepath.Join(root, "var"), config: filepath.Join(root, "config.yaml")}
	tg.dbPath = filepath.Join(tg.data, "karakuri.db")
	require.NoError(t, os.MkdirAll(tg.data, 0o750))
	require.NoError(t, os.WriteFile(tg.config, []byte("security:\n  jwt_secret: \"old-jwt\"\n  encryption_key: \"old-key\"\n"), 0o600))
	return tg
}

func (tg *target) options(archive string, replace bool) RestoreOptions {
	return RestoreOptions{
		Archive:    archive,
		Passphrase: testPass,
		Replace:    replace,
		DataDir:    tg.data,
		Database:   db.Options{Type: "sqlite", DSN: tg.dbPath, MaxOpenConns: 2, MaxIdleConns: 2},
		Version:    "1.3.0",
		StageSecrets: func(s Secrets) (func() (string, error), func(), []string, error) {
			commit := func() (string, error) {
				if tg.failConfig {
					return "", errors.New("disk full")
				}
				old, _ := os.ReadFile(tg.config)
				saved := tg.config + ".pre-restore"
				if err := os.WriteFile(saved, old, 0o600); err != nil {
					return "", err
				}
				body := "security:\n  jwt_secret: \"" + s.JWTSecret + "\"\n  encryption_key: \"" + s.EncryptionKey + "\"\n"
				return saved, os.WriteFile(tg.config, []byte(body), 0o600)
			}
			return commit, func() {}, []string{"security.encryption_key", "security.jwt_secret"}, nil
		},
		Current: func(gdb *gorm.DB) Source {
			return Source{DB: gdb, DatabaseType: "sqlite", DataDir: tg.data, Installation: "target", Version: "1.3.0"}
		},
	}
}

func (tg *target) open(t *testing.T) *gorm.DB {
	t.Helper()
	return openFile(t, tg.dbPath)
}

// archiveOf writes a verified archive of the populated installation.
func archiveOf(t *testing.T, src Source) string {
	t.Helper()
	dir := t.TempDir()
	svc := NewService(func() Source { return src }, nil)
	run, err := svc.Create(context.Background(), Request{Trigger: models.BackupTriggerCLI, Passphrase: testPass, Directory: dir})
	require.NoError(t, err)
	return filepath.Join(dir, run.Archive)
}

// TestRestoreIntoEmptyInstallation covers US2 scenario 1 and SC-001: every
// row, file, and secret comes back on a machine that never ran.
func TestRestoreIntoEmptyInstallation(t *testing.T) {
	populated(t)
	_, src := installation(t)
	archive := archiveOf(t, src)
	tg := newTarget(t)

	report, err := Restore(context.Background(), tg.options(archive, false))
	require.NoError(t, err)
	assert.Equal(t, report.Manifest.Tables, report.Loaded, "every row loads")
	assert.Empty(t, report.SafetyArchive, "nothing to replace, so nothing to back up")
	assert.Equal(t, []Plugin{{Name: "weather", Version: "1.2.0"}}, report.MissingPlugins)

	gdb := tg.open(t)
	var conn models.Connection
	require.NoError(t, gdb.Where("name = ?", "Off").First(&conn).Error)
	assert.False(t, conn.Enabled)
	assert.Equal(t, "sealed-config", conn.Config, "the sealed credentials arrive unchanged")
	for _, rel := range []string{"agents/server.pem", "uploads/widgets/w/u/photo.png", "sql/global.db",
		"history-spool/x/00000000000000000001-2.seg", "files/notes/today.txt"} {
		_, err := os.Stat(filepath.Join(tg.data, filepath.FromSlash(rel)))
		assert.NoError(t, err, "%s is restored", rel)
	}
	cfg, err := os.ReadFile(tg.config)
	require.NoError(t, err)
	assert.Contains(t, string(cfg), `encryption_key: "ab12"`, "the archive's key opens its credentials")
	assert.Contains(t, string(cfg), `jwt_secret: "jwt"`)
	old, err := os.ReadFile(report.SavedConfig)
	require.NoError(t, err)
	assert.Contains(t, string(old), "old-key", "the replaced key is kept")

	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(tg.data), ".var.restore-*"))
	assert.Empty(t, leftovers, "the staging directory is removed")
}

// TestRestoreRefusesAPopulatedInstallation covers US2 scenario 2: without
// --replace nothing changes; with it, the current state is backed up and kept
// aside first.
func TestRestoreRefusesAPopulatedInstallation(t *testing.T) {
	populated(t)
	_, src := installation(t)
	archive := archiveOf(t, src)
	tg := newTarget(t)
	_, err := Restore(context.Background(), tg.options(archive, false))
	require.NoError(t, err)

	// Mark the restored installation so a second restore is visibly a
	// replacement.
	gdb := tg.open(t)
	require.NoError(t, gdb.Create(&models.Setting{Key: "only-in-target", Value: "1"}).Error)
	closeDB(gdb)

	_, err = Restore(context.Background(), tg.options(archive, false))
	require.ErrorIs(t, err, ErrPopulated)
	assert.Contains(t, err.Error(), "pass --replace")

	report, err := Restore(context.Background(), tg.options(archive, true))
	require.NoError(t, err)
	require.NotEmpty(t, report.SafetyArchive)
	safety, err := Verify(report.SafetyArchive, testPass)
	require.NoError(t, err, "the safety backup is a good archive")
	// The replaced installation had the test's own setting and the marker the
	// first restore left for the event log.
	assert.Equal(t, report.Manifest.Tables["settings"]+2, safety.Tables["settings"], "it holds what was replaced")
	keptDB := false
	for _, kept := range report.KeptAside {
		if strings.HasPrefix(kept, tg.dbPath+".pre-restore-") {
			keptDB = true
			_, err := os.Stat(kept)
			assert.NoError(t, err)
		}
	}
	assert.True(t, keptDB, "the replaced database is kept: %v", report.KeptAside)

	var n int64
	gdb = tg.open(t)
	gdb.Model(&models.Setting{}).Where("key = ?", "only-in-target").Count(&n)
	assert.Zero(t, n, "the replacement is the archive's state")
	var marker models.Setting
	require.NoError(t, gdb.Where("key = ?", RestoredSetting).First(&marker).Error, "the restore leaves a marker for the next start to log")
	assert.Contains(t, marker.Value, `"replaced":true`)
}

// TestRestoreRefusesARunningServer covers US2 scenario 3.
func TestRestoreRefusesARunningServer(t *testing.T) {
	populated(t)
	_, src := installation(t)
	archive := archiveOf(t, src)
	tg := newTarget(t)
	release, err := instlock.Acquire(tg.data)
	require.NoError(t, err)
	defer release()

	_, err = Restore(context.Background(), tg.options(archive, false))
	assert.ErrorContains(t, err, "the server is running")
	_, statErr := os.Stat(tg.dbPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "nothing was written")
}

// TestFailedRestoreLeavesInstallationAsItWas covers FR-005: a failure at the
// last step reverses the database and directory swaps.
func TestFailedRestoreLeavesInstallationAsItWas(t *testing.T) {
	populated(t)
	_, src := installation(t)
	archive := archiveOf(t, src)
	tg := newTarget(t)
	_, err := Restore(context.Background(), tg.options(archive, false))
	require.NoError(t, err)
	gdb := tg.open(t)
	require.NoError(t, gdb.Create(&models.Setting{Key: "marker", Value: "before"}).Error)
	closeDB(gdb)
	require.NoError(t, os.WriteFile(filepath.Join(tg.data, "uploads", "marker"), []byte("before"), 0o600))
	cfgBefore, _ := os.ReadFile(tg.config)

	tg.failConfig = true
	_, err = Restore(context.Background(), tg.options(archive, true))
	require.ErrorContains(t, err, "left as it was")

	gdb = tg.open(t)
	var s models.Setting
	require.NoError(t, gdb.Where("key = ?", "marker").First(&s).Error, "the database is the one from before")
	marker, err := os.ReadFile(filepath.Join(tg.data, "uploads", "marker"))
	require.NoError(t, err, "the uploads directory is the one from before")
	assert.Equal(t, "before", string(marker))
	cfgAfter, _ := os.ReadFile(tg.config)
	assert.Equal(t, cfgBefore, cfgAfter)
	asides, _ := filepath.Glob(filepath.Join(tg.data, "*.pre-restore-*"))
	assert.Empty(t, asides, "nothing is left moved aside")
}

// TestRestoreRefusesANewerArchive covers US2 scenario 4.
func TestRestoreRefusesANewerArchive(t *testing.T) {
	populated(t)
	_, src := installation(t)
	src.Version = "9.0.0"
	archive := archiveOf(t, src)
	tg := newTarget(t)
	_, err := Restore(context.Background(), tg.options(archive, false))
	assert.ErrorContains(t, err, "the archive is from karakuri 9.0.0; this is 1.3.0; upgrade before restoring")

	src.Version = "dev"
	report, err := Restore(context.Background(), tg.options(archiveOf(t, src), false))
	require.NoError(t, err, "a development archive restores")
	assert.NotEmpty(t, report.Warnings)
}

// TestCheckVersion pins the version order.
func TestCheckVersion(t *testing.T) {
	tests := []struct {
		archive, binary string
		refuse, warn    bool
	}{
		{"1.2.0", "1.3.0", false, false},
		{"1.3.0", "1.3.0", false, false},
		{"1.3.1", "1.3.0", true, false},
		{"2.0.0", "1.9.9", true, false},
		{"v1.3.0-rc1", "1.3.0", false, false},
		{"dev", "1.3.0", false, true},
		{"1.3.0", "dev", false, true},
	}
	for _, tc := range tests {
		warning, err := checkVersion(tc.archive, tc.binary)
		assert.Equal(t, tc.refuse, err != nil, "%s onto %s", tc.archive, tc.binary)
		assert.Equal(t, tc.warn, warning != "", "%s onto %s", tc.archive, tc.binary)
	}
}

// TestCheckFileRoots covers the manifest's flow file roots, which name live
// directories a restore moves aside: one outside the data directory or over
// one of karakuri's own parts is refused.
func TestCheckFileRoots(t *testing.T) {
	assert.NoError(t, checkFileRoots([]string{"files", "notes/today"}))
	for _, root := range []string{"..", "../../home/u/.ssh", "/etc", "a/../..", "agents", "uploads/widgets", ""} {
		assert.Error(t, checkFileRoots([]string{root}), root)
	}
}
