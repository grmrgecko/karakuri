package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/instlock"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// S is the server's backup service, set by serve.
var S *Service

// Request asks for one backup.
type Request struct {
	Trigger    string
	UserID     *uuid.UUID
	FlowID     *uuid.UUID
	Passphrase string
	Directory  string
}

// Service runs backups one at a time, records each as a run, and tells
// watchers when one finishes.
type Service struct {
	source func() Source
	log    *zap.Logger

	// run serializes backups: a second request waits for the first instead of
	// doubling the database read and disk writes.
	run sync.Mutex

	mu       sync.Mutex
	watchers map[int]func(models.BackupRun)
	nextID   int
	// afterSuccess runs after a verified scheduled backup; the scheduler sets
	// it to apply retention.
	afterSuccess func(run *models.BackupRun)
}

// NewService returns a service copying the installation source describes.
// source is called for every backup so configuration changes apply.
func NewService(source func() Source, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{source: source, log: log, watchers: map[int]func(models.BackupRun){}}
}

// Watch registers fn to receive every finished run. The returned function
// removes it.
func (s *Service) Watch(fn func(models.BackupRun)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.watchers[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.watchers, id)
	}
}

func (s *Service) notify(run models.BackupRun) {
	s.mu.Lock()
	fns := make([]func(models.BackupRun), 0, len(s.watchers))
	for _, fn := range s.watchers {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(run)
	}
}

// Start records a run and performs it in the background, answering the run
// as recorded. A backup already running delays it, not the caller. The
// backup works on its own copy, so the caller may read the answer freely.
func (s *Service) Start(req Request) (*models.BackupRun, error) {
	run, err := s.open(req)
	if err != nil {
		return nil, err
	}
	working := *run
	go s.perform(context.Background(), &working, req.Passphrase)
	return run, nil
}

// Create records a run and performs it, returning the finished run. The
// error is the backup's failure, also recorded on the run.
func (s *Service) Create(ctx context.Context, req Request) (*models.BackupRun, error) {
	run, err := s.open(req)
	if err != nil {
		return nil, err
	}
	if err := s.perform(ctx, run, req.Passphrase); err != nil {
		return run, err
	}
	return run, nil
}

// open validates a request and inserts its run row.
func (s *Service) open(req Request) (*models.BackupRun, error) {
	if err := CheckPassphrase(req.Passphrase); err != nil {
		return nil, err
	}
	if req.Directory == "" {
		return nil, errors.New("no backup directory is set")
	}
	dir, err := filepath.Abs(req.Directory)
	if err != nil {
		return nil, err
	}
	run := &models.BackupRun{
		StartedAt: time.Now().UTC(),
		Trigger:   req.Trigger,
		UserID:    req.UserID,
		FlowID:    req.FlowID,
		Directory: dir,
		Status:    models.BackupRunning,
		Verified:  models.BackupVerifyPending,
	}
	if err := models.CreateBackupRun(run); err != nil {
		return nil, fmt.Errorf("recording the backup: %w", err)
	}
	return run, nil
}

// perform writes, renames, and verifies one archive, recording the outcome on
// run. A failure removes the partial or unverifiable file, so the directory
// only ever holds complete, verified archives.
func (s *Service) perform(ctx context.Context, run *models.BackupRun, passphrase string) error {
	s.run.Lock()
	defer s.run.Unlock()

	src := s.source()
	start := time.Now()
	run.StartedAt = start.UTC()
	// Names are stamped to the second; a second backup within the same
	// second takes the next free one rather than replacing the first.
	stamp := start
	for {
		run.Archive = ArchiveName(src.Installation, stamp)
		if _, err := os.Stat(filepath.Join(run.Directory, run.Archive)); errors.Is(err, os.ErrNotExist) {
			break
		}
		stamp = stamp.Add(time.Second)
	}
	_ = models.SaveBackupRun(run)
	s.log.Info("backup started", zap.Uint("run", run.ID), zap.String("trigger", run.Trigger), zap.String("archive", run.Archive))

	err := s.write(ctx, run, src, passphrase)
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	if err != nil {
		run.Status = models.BackupFailed
		run.Error = err.Error()
		if run.Verified == models.BackupVerifyPending {
			run.Verified = models.BackupVerifyFailed
		}
		s.log.Error("backup failed", zap.Uint("run", run.ID), zap.String("trigger", run.Trigger), zap.Error(err))
	} else {
		run.Status = models.BackupSucceeded
		s.log.Info("backup finished", zap.Uint("run", run.ID), zap.String("archive", run.Archive),
			zap.Int64("bytes", run.Size), zap.Duration("took", time.Since(start)))
	}
	if serr := models.SaveBackupRun(run); serr != nil {
		s.log.Error("recording the backup result", zap.Uint("run", run.ID), zap.Error(serr))
	}
	if err == nil && run.Trigger == models.BackupTriggerSchedule && s.afterSuccess != nil {
		s.afterSuccess(run)
	}
	s.notify(*run)
	return err
}

func (s *Service) write(ctx context.Context, run *models.BackupRun, src Source, passphrase string) error {
	if err := os.MkdirAll(run.Directory, 0o750); err != nil {
		return fmt.Errorf("creating the backup directory: %w", err)
	}
	final := filepath.Join(run.Directory, run.Archive)
	partial := partialPath(run.Directory, run.Archive)
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("creating the archive: %w", err)
	}
	// The lock tells a server starting meanwhile that the partial file belongs
	// to a live writer, such as the backup command, and is not left over.
	if err := instlock.TryLock(f); err != nil {
		f.Close()
		os.Remove(partial)
		return fmt.Errorf("locking the archive: %w", err)
	}
	m, err := Write(ctx, f, src, passphrase, run.Directory)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(partial)
		return err
	}
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return fmt.Errorf("naming the archive: %w", err)
	}
	if info, err := os.Stat(final); err == nil {
		run.Size = info.Size()
	}
	run.Counts = m.Counts

	if _, err := Verify(final, passphrase); err != nil {
		run.Verified = models.BackupVerifyFailed
		os.Remove(final)
		return fmt.Errorf("the written archive did not verify, so it was removed: %w", err)
	}
	run.Verified = models.BackupVerifyOK
	return nil
}

// beingWritten reports whether a live writer holds a partial archive's lock.
func beingWritten(partial string) bool {
	f, err := os.Open(partial)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := instlock.TryLock(f); err != nil {
		return errors.Is(err, instlock.ErrHeld)
	}
	_ = instlock.Unlock(f)
	return false
}

// partialPath is where an archive is written before it is complete. The
// leading dot and suffix keep it out of listings and retention.
func partialPath(dir, archive string) string {
	return filepath.Join(dir, "."+archive+".partial")
}

// Recover marks runs a stopped server left running as failed and removes
// their partial files. A run whose partial file another process still holds
// locked, such as the backup command running beside the server, is left to
// finish.
func (s *Service) Recover() {
	runs, err := models.ListRunningBackupRuns()
	if err != nil {
		s.log.Error("reading interrupted backups", zap.Error(err))
		return
	}
	for _, run := range runs {
		if run.Archive != "" {
			partial := partialPath(run.Directory, run.Archive)
			if beingWritten(partial) {
				s.log.Info("backup still being written by another process", zap.Uint("run", run.ID))
				continue
			}
			os.Remove(partial)
		}
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.Status = models.BackupFailed
		run.Verified = models.BackupVerifyFailed
		run.Error = "server stopped during the backup"
		if err := models.SaveBackupRun(run); err != nil {
			s.log.Error("recording an interrupted backup", zap.Uint("run", run.ID), zap.Error(err))
		}
		s.log.Info("marked interrupted backup as failed", zap.Uint("run", run.ID))
	}
}

// ArchiveInfo is one archive in a backup directory.
type ArchiveInfo struct {
	Name         string           `json:"name" description:"Archive file name."`
	Installation string           `json:"installation" description:"Installation the archive is of."`
	Size         int64            `json:"size" description:"Size in bytes."`
	CreatedAt    time.Time        `json:"created_at" description:"When the backup was taken."`
	Verified     string           `json:"verified" description:"ok, failed, or unverified for an archive no run here wrote."`
	RunID        *uint            `json:"run_id,omitempty" description:"Run that wrote it."`
	Counts       map[string]int64 `json:"counts,omitempty" description:"Rows of each kind it holds."`
}

// List returns the archives in dir, newest first, with what the runs that
// wrote them recorded. An archive copied in from elsewhere lists as
// unverified.
func List(dir string) ([]ArchiveInfo, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []ArchiveInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []ArchiveInfo{}
	for _, e := range entries {
		inst, created, ok := ParseArchiveName(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		a := ArchiveInfo{Name: e.Name(), Installation: inst, Size: info.Size(), CreatedAt: created, Verified: "unverified"}
		if run, err := models.FindBackupRunByArchive(e.Name()); err == nil && run.Status == models.BackupSucceeded {
			a.Verified = run.Verified
			id := run.ID
			a.RunID = &id
			a.Counts = run.Counts
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ArchivePath resolves an archive name inside dir, refusing anything that is
// not an archive name, so callers taking a name from a request cannot reach
// another file.
func ArchivePath(dir, name string) (string, error) {
	if _, _, ok := ParseArchiveName(name); !ok {
		return "", os.ErrNotExist
	}
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return path, nil
}

// recordRefusal records a backup that could not start, so it shows among the
// runs and watchers hear of it like any other failure.
func (s *Service) recordRefusal(trigger, dir string, cause error) {
	now := time.Now().UTC()
	run := &models.BackupRun{
		StartedAt:  now,
		FinishedAt: &now,
		Trigger:    trigger,
		Directory:  dir,
		Status:     models.BackupFailed,
		Verified:   models.BackupVerifyFailed,
		Error:      cause.Error(),
	}
	if err := models.CreateBackupRun(run); err != nil {
		s.log.Error("recording a refused backup", zap.Error(err))
	}
	s.notify(*run)
}

// Stored fills a request's passphrase from the schedule's stored one and its
// directory from the schedule when the request leaves them empty.
func (s *Service) Stored(req Request) (Request, error) {
	src := s.source()
	if req.Directory == "" {
		sch, err := LoadSchedule(src.DataDir)
		if err != nil {
			return req, err
		}
		req.Directory = sch.Directory
	}
	if req.Passphrase == "" {
		pass, err := StoredPassphrase(src.DataDir, src.Secrets.EncryptionKey)
		if err != nil {
			return req, err
		}
		req.Passphrase = pass
	}
	return req, nil
}

// DataDir is the installation's data directory.
func (s *Service) DataDir() string { return s.source().DataDir }

// EncryptionKey is the key the schedule's passphrase is sealed with.
func (s *Service) EncryptionKey() string { return s.source().Secrets.EncryptionKey }

// LogDownload records that someone downloaded an archive, which hands over
// every stored credential to whoever holds its passphrase, in the server log
// and the security log.
func (s *Service) LogDownload(ctx context.Context, archive string) {
	actor := events.ActorFrom(ctx)
	s.log.Info("backup downloaded", zap.String("user", actor.Name), zap.String("archive", archive))
	events.Record(events.Entry{
		Category: models.EventSecurity,
		Subject:  events.Subject{Kind: "backup", ID: archive, Name: archive},
		Message:  "Downloaded backup " + archive,
		Actor:    actor,
	})
}
