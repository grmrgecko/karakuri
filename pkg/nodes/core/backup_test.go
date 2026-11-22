package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/backup"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const backupTestKey = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// backupFixture runs a backup service on a fresh database and data directory,
// optionally with a stored passphrase.
func backupFixture(t *testing.T, storePassphrase bool) string {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	data := t.TempDir()
	backup.S = backup.NewService(func() backup.Source {
		return backup.Source{DB: db.DB, DatabaseType: "sqlite", DataDir: data, Installation: "test", Version: "1.0.0",
			Secrets: backup.Secrets{EncryptionKey: backupTestKey, JWTSecret: "jwt"}}
	}, nil)
	t.Cleanup(func() { backup.S = nil })
	if storePassphrase {
		pass := "correct horse battery"
		_, err := backup.SaveSchedule(data, backupTestKey, backup.ScheduleUpdate{Passphrase: &pass})
		require.NoError(t, err)
	}
	return data
}

// TestBackupCreateNode covers FR-009's action: the node writes and verifies
// an archive with the stored passphrase, and refuses without retry when none
// is stored.
func TestBackupCreateNode(t *testing.T) {
	backupFixture(t, false)
	_, err := (BackupCreateNode{}).Execute(t.Context(), newTestCtx(`{}`, nil), nil)
	var failure *engine.ExecutionError
	require.ErrorAs(t, err, &failure, "a missing passphrase is not worth retrying")
	assert.False(t, failure.Retryable)
	assert.Contains(t, failure.Message, "no backup passphrase is stored")

	backupFixture(t, true)
	dir := t.TempDir()
	out, err := (BackupCreateNode{}).Execute(t.Context(), newTestCtx(`{"directory":"`+dir+`"}`, nil), nil)
	require.NoError(t, err)
	payload := out["out"][0].Payload.(map[string]any)
	assert.Equal(t, "ok", payload["verified"])
	assert.Equal(t, dir+"/"+payload["archive"].(string), payload["path"])
	list, err := backup.List(dir)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// TestBackupResultNode covers FR-009's trigger: it fires on the outcomes it
// was set to and not on the others.
func TestBackupResultNode(t *testing.T) {
	backupFixture(t, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	emitted := make(chan *engine.Msg, 4)
	done := make(chan error, 1)
	go func() {
		done <- (BackupResultNode{}).Start(ctx, newTestCtx(`{"on":"failed"}`, nil), func(m *engine.Msg) { emitted <- m })
	}()
	// The trigger registers its watcher as it starts.
	time.Sleep(50 * time.Millisecond)

	good, err := backup.S.Stored(backup.Request{Trigger: "manual", Directory: t.TempDir()})
	require.NoError(t, err)
	_, err = backup.S.Create(t.Context(), good)
	require.NoError(t, err)
	bad := good
	bad.Directory = "/proc/karakuri-cannot-write-here"
	_, err = backup.S.Create(t.Context(), bad)
	require.Error(t, err)

	select {
	case m := <-emitted:
		payload := m.Payload.(map[string]any)
		assert.Equal(t, "failed", payload["status"])
		assert.NotEmpty(t, payload["error"])
	case <-time.After(5 * time.Second):
		t.Fatal("the failed backup did not fire the trigger")
	}
	select {
	case m := <-emitted:
		t.Fatalf("only the failure fires, got %v", m.Payload)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	require.NoError(t, <-done)

	_, err = (BackupResultNode{}).Execute(t.Context(), newTestCtx(`{"on":"sometimes"}`, nil), nil)
	assert.ErrorContains(t, err, `unknown backup outcome "sometimes"`)
}
