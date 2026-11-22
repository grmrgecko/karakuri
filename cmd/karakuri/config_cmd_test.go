package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/backup"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleConfig = `server:
  host: "127.0.0.1"

# These are shared placeholder secrets, identical in every generated config.
# Replace both before exposing this instance.
security:
  jwt_secret: "CHANGE-ME"
  bcrypt_cost: 12
  encryption_key: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

logging:
  level: "debug"
`

const replacementKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestStageConfigKeyRewritesOnlyTheKey covers a rotation editing the operator's
// own config file in place. The rewrite is textual rather than a YAML
// round-trip, precisely so the file comes back as the operator left it — a
// re-serialized config would drop every comment and reorder every key, which
// for a file people hand-edit is a worse outcome than not rotating at all.
//
// Staging and committing are separate because the rotation also re-encrypts
// every stored secret: the file must not change until that has succeeded, or a
// half-finished rotation leaves a key that opens nothing.
func TestStageConfigKeyRewritesOnlyTheKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(sampleConfig), 0o640),
		"the test needs a config file to rotate")

	pending, err := stageConfigKey(path, replacementKey)
	require.NoError(t, err, "staging a rotation against a config with a key must succeed")

	before, err := os.ReadFile(path)
	require.NoError(t, err, "the config must still be readable while staged")
	require.Equal(t, sampleConfig, string(before),
		"staging must not touch the file: a rotation that failed later would leave a key that opens nothing")

	require.NoError(t, pending.commit(), "committing a staged rotation must succeed")

	updated, err := os.ReadFile(path)
	require.NoError(t, err, "the rotated config must be readable back from disk")
	got := string(updated)

	assert.Contains(t, got, `encryption_key: "`+replacementKey+`"`, "the new key must be written")
	assert.NotContains(t, got, "deadbeef", "the old key must be gone, not merely accompanied by the new one")

	// Everything else, comments included, must survive untouched.
	for _, keep := range []string{
		`host: "127.0.0.1"`,
		"# These are shared placeholder secrets, identical in every generated config.",
		`jwt_secret: "CHANGE-ME"`,
		"bcrypt_cost: 12",
		`level: "debug"`,
	} {
		assert.Containsf(t, got, keep,
			"the rewrite is textual so the file comes back as the operator left it; %q was lost", keep)
	}

	info, err := os.Stat(path)
	require.NoError(t, err, "the rotated config must still exist")
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(),
		"the file holds secrets, so a rewrite must not widen its permissions")
}

// TestStageConfigKeyFollowsSymlink covers the layout the dev build actually
// produces: build/etc/config.yaml is a symlink to config.dev.yaml, and the
// Makefile repoints it when the mode changes. Writing through a temp file and
// renaming — the safe way to replace a file — would replace the link with a
// regular file and silently break that, so the rotation has to resolve the link
// first and rewrite the real file behind it.
func TestStageConfigKeyFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.dev.yaml")
	link := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(target, []byte(sampleConfig), 0o600), "the test needs a config to link to")
	require.NoError(t, os.Symlink(target, link), "the test needs the dev build's symlink layout")

	pending, err := stageConfigKey(link, replacementKey)
	require.NoError(t, err, "staging through a symlink must succeed")
	require.NoError(t, pending.commit(), "committing through a symlink must succeed")

	info, err := os.Lstat(link)
	require.NoError(t, err, "the link must still exist")
	assert.NotZero(t, info.Mode()&os.ModeSymlink,
		"the link must survive, or the Makefile's mode switch silently stops working")

	updated, err := os.ReadFile(target)
	require.NoError(t, err, "the link's target must be readable")
	assert.Contains(t, string(updated), replacementKey, "the real file behind the link is what must be rewritten")
}

// TestStageConfigKeyRejectsConfigWithoutKey pairs with the successful rotation
// above. A config with no encryption_key line has nothing to rewrite, and
// appending one would put it at an arbitrary place in a file the operator
// maintains — better to say so than to guess where it belongs.
func TestStageConfigKeyRejectsConfigWithoutKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  host: \"127.0.0.1\"\n"), 0o600),
		"the test needs a config with no key in it")

	_, err := stageConfigKey(path, replacementKey)
	assert.Error(t, err,
		"there is no key line to rewrite, and appending one would guess where it belongs in a hand-maintained file")
}

// TestStageConfigKeyCleanupLeavesNoTempFile covers the abandoned rotation. The
// staged file holds the new encryption key in plain text next to the real
// config, so leaving one behind after a failed re-encryption would put a live
// secret on disk in a file nothing will ever clean up.
func TestStageConfigKeyCleanupLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(sampleConfig), 0o600), "the test needs a config to stage against")

	pending, err := stageConfigKey(path, replacementKey)
	require.NoError(t, err, "staging must succeed before abandoning it means anything")

	pending.cleanup()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "the config directory must be readable")
	for _, e := range entries {
		assert.Falsef(t, strings.HasSuffix(e.Name(), ".rotating"),
			"%q was left behind: it holds the new encryption key in plain text beside the real config", e.Name())
	}
}

// TestStageRestoredSecrets covers the config side of a restore: both secrets
// land in the real file behind the dev symlink, a secret holding $ survives
// as written, and the replaced file is kept with its key.
func TestStageRestoredSecrets(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.dev.yaml")
	link := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(target, []byte(sampleConfig), 0o600))
	require.NoError(t, os.Symlink(target, link))

	commit, discard, keys, err := stageRestoredSecrets(link, backup.Secrets{EncryptionKey: replacementKey, JWTSecret: "j$1wt"})
	require.NoError(t, err)
	defer discard()
	assert.Equal(t, []string{"security.encryption_key", "security.jwt_secret"}, keys)
	saved, err := commit()
	require.NoError(t, err)

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the dev symlink survives")
	updated, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(updated), `encryption_key: "`+replacementKey+`"`)
	assert.Contains(t, string(updated), `jwt_secret: "j$1wt"`)
	old, err := os.ReadFile(saved)
	require.NoError(t, err)
	assert.Equal(t, sampleConfig, string(old), "the replaced config is kept whole")
}

// TestStageConfigValuesKeepsTheLineAfterAnEmptySetting locks the rewrite to
// one line per setting. A secret supplied through the environment leaves its
// config line empty, and a pattern that reads past the line end replaced the
// following setting with the new value, which dropped the encryption key from
// a restored config.
func TestStageConfigValuesKeepsTheLineAfterAnEmptySetting(t *testing.T) {
	const config = "security:\n  jwt_secret:\n  encryption_key: \"deadbeef\"\n  bcrypt_cost: 12\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600), "the test needs a config with an empty setting")

	pending, err := stageConfigValues(path, map[string]string{"encryption_key": replacementKey, "jwt_secret": "restored"}, ".restoring")
	require.NoError(t, err, "staging against a config holding both settings must succeed")
	require.NoError(t, pending.commit(), "committing the staged config must succeed")

	updated, err := os.ReadFile(path)
	require.NoError(t, err, "the rewritten config must be readable")
	assert.Equal(t,
		"security:\n  jwt_secret: \"restored\"\n  encryption_key: \""+replacementKey+"\"\n  bcrypt_cost: 12\n",
		string(updated),
		"an empty setting takes its value on its own line and the next setting stays in place")
}

// TestRotateKeyReSealsTheBackupPassphrase runs a rotation and locks that the
// backup schedule's stored passphrase moves to the new key. It is sealed with
// security.encryption_key like a connection secret, and a rotation that left
// it behind made every scheduled backup fail at its next run with a
// passphrase that no longer opened. The rest of the schedule is untouched.
func TestRotateKeyReSealsTheBackupPassphrase(t *testing.T) {
	previous := config.C
	t.Cleanup(func() { config.C = previous })
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
	passphrase := "correct horse battery"
	_, err := backup.SaveSchedule(dataDir, config.DefaultEncryptionKey, backup.ScheduleUpdate{
		Enabled: true, Cron: "0 4 * * *", Retain: 7, Passphrase: &passphrase,
	})
	require.NoError(t, err, "the schedule must be stored under the old key")

	rotate := &ConfigRotateKeyCmd{Key: replacementKey, PrintOnly: true}
	require.NoError(t, rotate.Run(), "a rotation over readable secrets must succeed")

	got, err := backup.StoredPassphrase(dataDir, replacementKey)
	require.NoError(t, err, "the passphrase must open with the new key after the rotation")
	assert.Equal(t, passphrase, got, "the passphrase itself must not change")
	schedule, err := backup.LoadSchedule(dataDir)
	require.NoError(t, err, "the rewritten schedule must load")
	assert.True(t, schedule.Enabled, "the schedule stays enabled")
	assert.Equal(t, "0 4 * * *", schedule.Cron, "the cron expression survives the rewrite")
	assert.Equal(t, 7, schedule.Retain, "the retention survives the rewrite")
	assert.NotNil(t, schedule.LastScheduledAt, "the catch-up marker survives the rewrite")

	// A passphrase the current key cannot open stops the rotation before
	// anything is written, as an unreadable connection does.
	config.C.Security.EncryptionKey = config.DefaultEncryptionKey
	again := &ConfigRotateKeyCmd{Key: strings.Repeat("ab", 32), PrintOnly: true}
	err = again.Run()
	require.Error(t, err, "a passphrase sealed under another key must stop the rotation")
	assert.Contains(t, err.Error(), "the stored backup passphrase", "the refusal names what could not be opened")
	_, err = backup.StoredPassphrase(dataDir, replacementKey)
	assert.NoError(t, err, "a refused rotation leaves the stored passphrase as it was")
}
