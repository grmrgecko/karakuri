package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPass = "correct horse battery"

// installation lays out a data directory with one file in every archived
// part, a temporary file of each writer, a user file named like one, and a
// flow files root inside and one outside the data directory.
func installation(t *testing.T) (string, Source) {
	t.Helper()
	data := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(data, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	write("agents/server.pem", "cert")
	write("agents/server-key.pem", "key")
	write("uploads/widgets/w/u/photo.png", "png")
	write("sql/.query-abc", "half a save")
	write("history-spool/x/00000000000000000001-2.seg", "{}\n{}\n")
	write("history-spool/x/00000000000000000002-1.seg.tmp", "half a segment")
	write("files/notes/today.txt", "hello")
	// Two dots inside a name are not a parent segment.
	write("files/notes/holiday..2024.jpg", "jpg")
	// Uploads and flow files are named by users, so a temporary-looking name
	// is still theirs.
	write("uploads/widgets/w/u/photo.tmp", "tmp")
	write("files/notes/report.tmp", "report")
	write("renders/big.png", "not archived")
	_, err := sqlstore.Store{DataDir: data}.Query(context.Background(), uuid.Nil, "global", "CREATE TABLE t (v INTEGER)", nil, 10)
	require.NoError(t, err)

	outside := t.TempDir()
	return data, Source{
		DB:           db.DB,
		DatabaseType: "sqlite",
		DataDir:      data,
		FileRoots:    []string{filepath.Join(data, "files"), outside},
		Secrets:      Secrets{EncryptionKey: "ab12", JWTSecret: "jwt"},
		Plugins:      []Plugin{{Name: "weather", Version: "1.2.0"}},
		Version:      "1.3.0",
		Installation: "lab",
	}
}

// TestCreateAndVerify covers US1: a running installation's backup holds every
// part, leaves out what is regenerated, and verifies.
func TestCreateAndVerify(t *testing.T) {
	populated(t)
	_, src := installation(t)
	dir := t.TempDir()
	svc := NewService(func() Source { return src }, nil)

	run, err := svc.Create(context.Background(), Request{Trigger: models.BackupTriggerCLI, Passphrase: testPass, Directory: dir})
	require.NoError(t, err)
	assert.Equal(t, models.BackupSucceeded, run.Status)
	assert.Equal(t, models.BackupVerifyOK, run.Verified)
	assert.Regexp(t, `^karakuri-lab-\d{8}T\d{6}Z\.tar\.zst\.age$`, run.Archive)
	assert.Equal(t, int64(1), run.Counts["flows"])

	m, err := Verify(filepath.Join(dir, run.Archive), testPass)
	require.NoError(t, err)
	var names []string
	for _, p := range m.Parts {
		names = append(names, p.Name)
	}
	for _, want := range []string{"db/flows.jsonl", "db/users.jsonl", "secrets.json", "agents/server.pem", "agents/server-key.pem",
		"uploads/widgets/w/u/photo.png", "sql/global.db", "history-spool/x/00000000000000000001-2.seg", "files/files/notes/today.txt",
		"files/files/notes/holiday..2024.jpg", "uploads/widgets/w/u/photo.tmp", "files/files/notes/report.tmp"} {
		assert.Contains(t, names, want)
	}
	for _, name := range names {
		assert.False(t, strings.HasPrefix(name, "renders/"), "renders are regenerated")
		if strings.HasPrefix(name, "sql/") || strings.HasPrefix(name, "history-spool/") {
			assert.False(t, temporaryName(filepath.Base(name)), "a half-written file is never archived: %s", name)
		}
	}
	assert.Equal(t, "1.3.0", m.Version)
	assert.Equal(t, []Plugin{{Name: "weather", Version: "1.2.0"}}, m.Plugins)
	assert.Contains(t, strings.Join(m.Skipped, "\n"), "outside the data directory")
	assert.Equal(t, int64(2), m.Tables["folders"])

	list, err := List(dir)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, models.BackupVerifyOK, list[0].Verified)
	assert.Equal(t, run.Archive, list[0].Name)
}

// TestDamagedArchivesRefuse covers SC-005: one changed byte, a cut-off file,
// and a wrong passphrase all refuse.
func TestDamagedArchivesRefuse(t *testing.T) {
	populated(t)
	_, src := installation(t)
	var buf bytes.Buffer
	_, err := Write(context.Background(), &buf, src, testPass, t.TempDir())
	require.NoError(t, err)
	good := buf.Bytes()

	check := func(data []byte, pass string) error {
		_, err := walkArchive(bytes.NewReader(data), pass, nil)
		return err
	}
	require.NoError(t, check(good, testPass))

	flipped := append([]byte{}, good...)
	flipped[len(flipped)/2] ^= 0x01
	assert.Error(t, check(flipped, testPass), "a changed byte refuses")
	assert.Error(t, check(good[:len(good)-100], testPass), "a cut-off archive refuses")
	assert.ErrorIs(t, check(good, "another passphrase entirely"), ErrPassphrase)
	assert.Error(t, check(good[:20], testPass), "a stub refuses")

	_, err = Write(context.Background(), io.Discard, src, "short", t.TempDir())
	assert.ErrorContains(t, err, "at least 12 characters")
}

// TestFailedBackupKeepsEarlierArchives covers a backup that fails partway: no
// partial file remains, the earlier archive is untouched, and the run says
// why.
func TestFailedBackupKeepsEarlierArchives(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	populated(t)
	data, src := installation(t)
	dir := t.TempDir()
	svc := NewService(func() Source { return src }, nil)
	first, err := svc.Create(context.Background(), Request{Trigger: models.BackupTriggerManual, Passphrase: testPass, Directory: dir})
	require.NoError(t, err)

	locked := filepath.Join(data, "uploads", "locked.bin")
	require.NoError(t, os.WriteFile(locked, []byte("x"), 0o000))
	run, err := svc.Create(context.Background(), Request{Trigger: models.BackupTriggerManual, Passphrase: testPass, Directory: dir})
	require.Error(t, err)
	assert.Equal(t, models.BackupFailed, run.Status)
	assert.Contains(t, run.Error, "locked.bin")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the earlier archive remains")
	assert.Equal(t, first.Archive, entries[0].Name())
}

// TestBackupsRunOneAtATime covers the queue: a second request waits for the
// first, and both get their own archive.
func TestBackupsRunOneAtATime(t *testing.T) {
	populated(t)
	_, src := installation(t)
	dir := t.TempDir()
	svc := NewService(func() Source { return src }, nil)
	done := make(chan models.BackupRun, 2)
	stop := svc.Watch(func(r models.BackupRun) { done <- r })
	defer stop()

	_, err := svc.Start(Request{Trigger: models.BackupTriggerManual, Passphrase: testPass, Directory: dir})
	require.NoError(t, err)
	_, err = svc.Start(Request{Trigger: models.BackupTriggerManual, Passphrase: testPass, Directory: dir})
	require.NoError(t, err)
	a, b := <-done, <-done
	assert.Equal(t, models.BackupSucceeded, a.Status)
	assert.Equal(t, models.BackupSucceeded, b.Status)
	assert.NotEqual(t, a.Archive, b.Archive, "a second backup in the same second takes its own name")
	assert.False(t, b.StartedAt.Before(*a.FinishedAt), "the second starts after the first finishes")
}

// TestStartAnswersARunTheBackupDoesNotWrite locks that the run Start answers
// is the caller's own. The API and MCP handlers encode it while the backup
// runs, so the backup writing the same struct was a data race.
func TestStartAnswersARunTheBackupDoesNotWrite(t *testing.T) {
	populated(t)
	_, src := installation(t)
	svc := NewService(func() Source { return src }, nil)
	done := make(chan models.BackupRun, 1)
	stop := svc.Watch(func(r models.BackupRun) { done <- r })
	defer stop()

	run, err := svc.Start(Request{Trigger: models.BackupTriggerManual, Passphrase: testPass, Directory: t.TempDir()})
	require.NoError(t, err)
	finished := <-done
	require.Equal(t, models.BackupSucceeded, finished.Status)
	assert.Equal(t, run.ID, finished.ID, "the answer and the backup are the same run")
	assert.Equal(t, models.BackupRunning, run.Status, "the answered run reads as it did when Start returned")
	assert.Empty(t, run.Archive, "the backup names its archive on its own copy")
}

// TestSavedDatabasesCopyComplete is T008: sqlstore replaces a database by
// renaming a finished file over it, so a copy taken during a stream of saves
// is always a whole database, without the store's lock.
func TestSavedDatabasesCopyComplete(t *testing.T) {
	data := t.TempDir()
	store := sqlstore.Store{DataDir: data}
	ctx := context.Background()
	_, err := store.Query(ctx, uuid.Nil, "global", "CREATE TABLE t (v INTEGER)", nil, 10)
	require.NoError(t, err)

	stop := make(chan struct{})
	saved := make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				saved <- nil
				return
			default:
			}
			if _, err := store.Query(ctx, uuid.Nil, "global", "INSERT INTO t VALUES (?)", []any{i}, 10); err != nil {
				saved <- err
				return
			}
		}
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	copies := 0
	for time.Now().Before(deadline) {
		var buf bytes.Buffer
		a, err := newArchiveWriter(&buf, testPass)
		require.NoError(t, err)
		require.NoError(t, writeTree(a, filepath.Join(data, "sql"), "sql", true))
		require.NoError(t, a.finish(&Manifest{CreatedAt: time.Now()}))
		_, err = walkArchive(&buf, testPass, func(e Entry) error {
			body, err := io.ReadAll(e.R)
			if err != nil {
				return err
			}
			path := filepath.Join(t.TempDir(), "copy.db")
			require.NoError(t, os.WriteFile(path, body, 0o600))
			gdb, err := db.Open(db.Options{Type: "sqlite", DSN: path, MaxOpenConns: 1, MaxIdleConns: 1})
			require.NoError(t, err)
			defer func() { s, _ := gdb.DB(); s.Close() }()
			var result string
			require.NoError(t, gdb.Raw("PRAGMA integrity_check").Scan(&result).Error)
			assert.Equal(t, "ok", result, "every copy is a whole database")
			copies++
			return nil
		})
		require.NoError(t, err)
	}
	close(stop)
	require.NoError(t, <-saved)
	assert.Greater(t, copies, 1)
}

// TestSplitFileRootsDropsNestedRoots locks that each file is archived once. A
// root listed twice, or one inside another, put the same member name in the
// archive twice. The backup verified, since reading accepts repeated names,
// and then could not be restored: unpacking creates files exclusively and
// stopped at the second copy with "file exists".
func TestSplitFileRootsDropsNestedRoots(t *testing.T) {
	data := t.TempDir()
	files := filepath.Join(data, "files")
	keep, skipped := splitFileRoots(data, []string{
		filepath.Join(files, "shared"), files, files, filepath.Join(data, "exports"),
	})

	var prefixes []string
	for _, r := range keep {
		prefixes = append(prefixes, r.prefix)
	}
	assert.ElementsMatch(t, []string{"files/files", "files/exports"}, prefixes,
		"the outer root already carries everything under the nested and repeated ones")
	assert.Len(t, skipped, 2, "the nested root and the repeat are each reported as left out")
}

// TestSymlinkedDataDirectoryIsArchived locks that a data directory kept on
// another disk through a symbolic link is still backed up. The walk does not
// follow links, and that included the directory it was asked to copy: with
// uploads linked to a larger disk the archive verified, held no upload, and a
// restore put nothing in the place the link was moved from. Links inside the
// directory are still left out.
func TestSymlinkedDataDirectoryIsArchived(t *testing.T) {
	data, disk := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(disk, "widgets"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(disk, "widgets", "photo.png"), []byte("png"), 0o600))
	if err := os.Symlink(disk, filepath.Join(data, "uploads")); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
	require.NoError(t, os.Symlink(filepath.Join(disk, "widgets", "photo.png"), filepath.Join(disk, "widgets", "alias.png")))

	a, err := newArchiveWriter(io.Discard, testPass)
	require.NoError(t, err)
	defer a.abort()
	require.NoError(t, writeTree(a, filepath.Join(data, "uploads"), "uploads", false))

	var names []string
	for _, p := range a.parts {
		names = append(names, p.Name)
	}
	assert.Equal(t, []string{"uploads/widgets/photo.png"}, names,
		"the linked directory's files are archived under the part's own name, and links inside it are not")
}
