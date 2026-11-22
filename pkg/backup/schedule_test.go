package backup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/instlock"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKey = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// TestRetentionKeepsTheNewest covers SC-006 and the guard around it: five
// archives kept to three removes the two oldest, and another installation's
// archives and files that are not archives are never touched.
func TestRetentionKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	var mine []string
	for i := 0; i < 5; i++ {
		name := ArchiveName("lab", base.AddDate(0, 0, i))
		mine = append(mine, name)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}
	other := ArchiveName("garage", base)
	require.NoError(t, os.WriteFile(filepath.Join(dir, other), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))

	removed, err := prune(dir, "lab", 3)
	require.NoError(t, err)
	assert.ElementsMatch(t, mine[:2], removed)

	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := append([]string{other, "notes.txt"}, mine[2:]...)
	sort.Strings(want)
	assert.Equal(t, want, left)
}

// TestMissedTime covers the catch-up decision after a stopped server.
func TestMissedTime(t *testing.T) {
	last := time.Date(2026, 11, 4, 3, 0, 0, 0, time.Local)
	tests := []struct {
		name   string
		now    time.Time
		missed bool
		at     time.Time
	}{
		{name: "before the next time", now: last.Add(23 * time.Hour), missed: false},
		{name: "one missed", now: last.Add(25 * time.Hour), missed: true, at: last.AddDate(0, 0, 1)},
		{name: "several missed, the latest counts", now: last.AddDate(0, 0, 3).Add(time.Hour), missed: true, at: last.AddDate(0, 0, 3)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at, ok := missedTime("0 3 * * *", last, tc.now)
			assert.Equal(t, tc.missed, ok)
			if tc.missed {
				assert.True(t, tc.at.Equal(at), "missed %s, want %s", at, tc.at)
			}
		})
	}
}

// TestSaveScheduleRefuses covers the refusals made when the schedule is saved
// rather than at night.
func TestSaveScheduleRefuses(t *testing.T) {
	populated(t)
	data := t.TempDir()
	pass := testPass
	short := "short"
	readOnly := filepath.Join(t.TempDir(), "ro")
	require.NoError(t, os.MkdirAll(readOnly, 0o500))

	tests := []struct {
		name string
		u    ScheduleUpdate
		want string
	}{
		{name: "bad cron", u: ScheduleUpdate{Cron: "every night"}, want: "invalid cron spec"},
		{name: "retain too high", u: ScheduleUpdate{Retain: 400}, want: "retain must be between 1 and 365"},
		{name: "relative directory", u: ScheduleUpdate{Directory: "backups"}, want: "absolute path"},
		{name: "short passphrase", u: ScheduleUpdate{Passphrase: &short}, want: "at least 12 characters"},
		{name: "enabled without a passphrase", u: ScheduleUpdate{Enabled: true}, want: "stored passphrase"},
	}
	if os.Geteuid() != 0 {
		tests = append(tests, struct {
			name string
			u    ScheduleUpdate
			want string
		}{name: "unwritable directory", u: ScheduleUpdate{Directory: filepath.Join(readOnly, "inner")}, want: "cannot be created"})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SaveSchedule(data, testKey, tc.u)
			assert.ErrorContains(t, err, tc.want)
		})
	}

	s, err := SaveSchedule(data, testKey, ScheduleUpdate{Enabled: true, Passphrase: &pass})
	require.NoError(t, err)
	assert.Equal(t, DefaultCron, s.Cron)
	assert.Equal(t, DefaultDirectory(data), s.Directory)
	assert.NotEqual(t, pass, s.Passphrase, "the passphrase is stored sealed")
	require.NotNil(t, s.LastScheduledAt, "enabling starts counting from now, so nothing is caught up at once")
	opened, err := StoredPassphrase(data, testKey)
	require.NoError(t, err)
	assert.Equal(t, pass, opened)

	s, err = SaveSchedule(data, testKey, ScheduleUpdate{Enabled: true, Retain: 3})
	require.NoError(t, err, "a nil passphrase keeps the stored one")
	assert.Equal(t, 3, s.Retain)
}

// TestRecoverMarksInterruptedRuns covers a server stopped mid-backup, and a
// server starting while the backup command is writing.
func TestRecoverMarksInterruptedRuns(t *testing.T) {
	populated(t)
	dir := t.TempDir()
	run := &models.BackupRun{StartedAt: time.Now(), Trigger: models.BackupTriggerSchedule, Directory: dir,
		Archive: ArchiveName("lab", time.Now()), Status: models.BackupRunning, Verified: models.BackupVerifyPending}
	require.NoError(t, models.CreateBackupRun(run))
	partial := partialPath(dir, run.Archive)
	require.NoError(t, os.WriteFile(partial, []byte("half"), 0o600))

	NewService(func() Source { return Source{} }, nil).Recover()
	runs, err := models.ListBackupRuns(1)
	require.NoError(t, err)
	assert.Equal(t, models.BackupFailed, runs[0].Status)
	assert.Equal(t, "server stopped during the backup", runs[0].Error)
	_, err = os.Stat(partial)
	assert.ErrorIs(t, err, os.ErrNotExist)

	// A backup another process is still writing keeps its file and its run.
	live := &models.BackupRun{StartedAt: time.Now(), Trigger: models.BackupTriggerCLI, Directory: dir,
		Archive: ArchiveName("cli", time.Now()), Status: models.BackupRunning, Verified: models.BackupVerifyPending}
	require.NoError(t, models.CreateBackupRun(live))
	livePartial := partialPath(dir, live.Archive)
	f, err := os.OpenFile(livePartial, os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, instlock.TryLock(f))
	NewService(func() Source { return Source{} }, nil).Recover()
	_, err = os.Stat(livePartial)
	assert.NoError(t, err, "a locked partial file is left to its writer")
	runs, err = models.ListBackupRuns(1)
	require.NoError(t, err)
	assert.Equal(t, models.BackupRunning, runs[0].Status)
}

// TestScheduledBackupAppliesRetention runs the scheduler's fire path: a
// scheduled backup lands, records its time, and prunes to the retention.
func TestScheduledBackupAppliesRetention(t *testing.T) {
	populated(t)
	data, src := installation(t)
	pass := testPass
	dir := t.TempDir()
	_, err := SaveSchedule(data, testKey, ScheduleUpdate{Enabled: true, Directory: dir, Retain: 2, Passphrase: &pass})
	require.NoError(t, err)
	src.Secrets.EncryptionKey = testKey
	svc := NewService(func() Source { return src }, nil)
	sch := NewScheduler(svc, data, testKey, nil)

	base := time.Now().Add(-72 * time.Hour)
	for i := 0; i < 3; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ArchiveName("lab", base.Add(time.Duration(i)*time.Hour))), []byte("x"), 0o600))
	}
	due := time.Now().Truncate(time.Minute)
	sch.fire(context.Background(), due)

	list, err := List(dir)
	require.NoError(t, err)
	require.Len(t, list, 2, "retention keeps two")
	assert.Equal(t, models.BackupVerifyOK, list[0].Verified, "the newest is the scheduled backup")
	s, err := LoadSchedule(data)
	require.NoError(t, err)
	assert.True(t, due.Equal(*s.LastScheduledAt))
}

// TestCatchUpSurvivesAReload covers a schedule saved, or anything else waking
// the scheduler, in the two minutes a missed backup waits after start. The
// backup the stopped server missed is still owed and still runs.
func TestCatchUpSurvivesAReload(t *testing.T) {
	populated(t)
	data, src := installation(t)
	pass := testPass
	dir := t.TempDir()
	s, err := SaveSchedule(data, testKey, ScheduleUpdate{Enabled: true, Directory: dir, Passphrase: &pass})
	require.NoError(t, err)
	// The default schedule is daily, so two days of downtime missed a backup.
	stopped := time.Now().Add(-48 * time.Hour)
	s.LastScheduledAt = &stopped
	require.NoError(t, storeSchedule(s))
	src.Secrets.EncryptionKey = testKey
	sch := NewScheduler(NewService(func() Source { return src }, nil), data, testKey, nil)

	delay := catchUpDelay
	catchUpDelay = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sch.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		catchUpDelay = delay
	})

	sch.Reload()
	require.Eventually(t, func() bool {
		list, err := List(dir)
		return err == nil && len(list) == 1
	}, 10*time.Second, 50*time.Millisecond, "a reload during the delay must not cancel the missed backup")
}

// TestOverdue locks the hour of grace a scheduled backup gets (045 FR-005).
// With an hourly schedule in UTC, a success just before the top of the hour
// is due at that hour, which is less than an hour ago, so it is not overdue.
// A success two hours earlier was due two hours ago, so it is.
func TestOverdue(t *testing.T) {
	populated(t)
	data, src := installation(t)
	pass := testPass
	_, err := SaveSchedule(data, testKey, ScheduleUpdate{Enabled: true, Cron: "CRON_TZ=UTC 0 * * * *", Directory: t.TempDir(), Retain: 2, Passphrase: &pass})
	require.NoError(t, err)
	src.Secrets.EncryptionKey = testKey
	svc := NewService(func() Source { return src }, nil)

	top := time.Now().UTC().Truncate(time.Hour)
	succeed := func(at time.Time) {
		t.Helper()
		require.NoError(t, models.CreateBackupRun(&models.BackupRun{StartedAt: at, Trigger: models.BackupTriggerSchedule, Status: models.BackupSucceeded, Verified: models.BackupVerifyOK}))
	}

	succeed(top.Add(-2*time.Hour - time.Minute))
	due, overdue, err := svc.Overdue(time.Now())
	require.NoError(t, err)
	assert.True(t, overdue, "a backup due two hours ago with no success since is overdue")
	assert.True(t, top.Add(-2*time.Hour).Equal(due), "it was due at the first hour after the success")

	succeed(top.Add(-time.Minute))
	_, overdue, err = svc.Overdue(time.Now())
	require.NoError(t, err)
	assert.False(t, overdue, "a backup due less than an hour ago is within its grace")
}
