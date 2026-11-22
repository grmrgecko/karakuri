package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// settingKey is where the schedule is stored.
const settingKey = "backup"

// Schedule defaults and limits.
const (
	DefaultCron   = "0 3 * * *"
	DefaultRetain = 14
	MaxRetain     = 365
	runRetention  = 400 * 24 * time.Hour
)

// catchUpDelay is how long after start a missed backup waits, so start-up
// work is not slowed. Tests shorten it.
var catchUpDelay = 2 * time.Minute

// Schedule is the stored backup schedule. The passphrase is kept sealed with
// the installation's encryption key, as connection secrets are, so a
// scheduled backup needs nobody present.
type Schedule struct {
	Enabled         bool       `json:"enabled"`
	Cron            string     `json:"cron"`
	Directory       string     `json:"directory"`
	Retain          int        `json:"retain"`
	Passphrase      string     `json:"passphrase,omitempty"`
	LastScheduledAt *time.Time `json:"last_scheduled_at,omitempty"`
}

// DefaultDirectory is where archives go when the schedule names none.
func DefaultDirectory(dataDir string) string {
	return filepath.Join(dataDir, "backups")
}

// LoadSchedule reads the stored schedule, filling defaults.
func LoadSchedule(dataDir string) (Schedule, error) {
	s := Schedule{Cron: DefaultCron, Retain: DefaultRetain, Directory: DefaultDirectory(dataDir)}
	setting, err := models.GetSetting(settingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(setting.Value), &s); err != nil {
		return s, fmt.Errorf("reading the backup schedule: %w", err)
	}
	if s.Cron == "" {
		s.Cron = DefaultCron
	}
	if s.Retain == 0 {
		s.Retain = DefaultRetain
	}
	if s.Directory == "" {
		s.Directory = DefaultDirectory(dataDir)
	}
	return s, nil
}

// storeSchedule writes the schedule as stored, passphrase already sealed.
func storeSchedule(s Schedule) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return models.SetSetting(settingKey, string(data))
}

// ScheduleUpdate is a change to the schedule. A nil Passphrase keeps the
// stored one; an empty one clears it.
type ScheduleUpdate struct {
	Enabled    bool
	Cron       string
	Directory  string
	Retain     int
	Passphrase *string
}

// SaveSchedule validates and stores a schedule change. The directory is
// created when missing and probed with a file, so an unwritable one is
// refused now rather than discovered at three in the morning.
func SaveSchedule(dataDir, encryptionKey string, u ScheduleUpdate) (Schedule, error) {
	current, err := LoadSchedule(dataDir)
	if err != nil {
		return current, err
	}
	next := current
	next.Enabled = u.Enabled
	next.Cron = strings.TrimSpace(u.Cron)
	if next.Cron == "" {
		next.Cron = DefaultCron
	}
	if err := cronspec.Validate(next.Cron, ""); err != nil {
		return current, err
	}
	next.Retain = u.Retain
	if next.Retain == 0 {
		next.Retain = DefaultRetain
	}
	if next.Retain < 1 || next.Retain > MaxRetain {
		return current, fmt.Errorf("retain must be between 1 and %d archives", MaxRetain)
	}
	next.Directory = strings.TrimSpace(u.Directory)
	if next.Directory == "" {
		next.Directory = DefaultDirectory(dataDir)
	}
	if !filepath.IsAbs(next.Directory) {
		return current, errors.New("the backup directory must be an absolute path")
	}
	if err := probeDirectory(next.Directory); err != nil {
		return current, err
	}
	if u.Passphrase != nil {
		if *u.Passphrase == "" {
			next.Passphrase = ""
		} else {
			if err := CheckPassphrase(*u.Passphrase); err != nil {
				return current, err
			}
			key, err := crypto.KeyFromHex(encryptionKey)
			if err != nil {
				return current, fmt.Errorf("sealing the passphrase: %w", err)
			}
			sealed, err := crypto.Encrypt(key, []byte(*u.Passphrase))
			if err != nil {
				return current, fmt.Errorf("sealing the passphrase: %w", err)
			}
			next.Passphrase = sealed
		}
	}
	if next.Enabled && next.Passphrase == "" {
		return current, errors.New("scheduled backups need a stored passphrase")
	}
	// A schedule turned on or changed starts counting from now; only times
	// missed while it was in force are caught up.
	if next.Enabled && (!current.Enabled || current.Cron != next.Cron || current.LastScheduledAt == nil) {
		now := time.Now().UTC()
		next.LastScheduledAt = &now
	}
	if err := storeSchedule(next); err != nil {
		return current, err
	}
	return next, nil
}

// probeDirectory creates dir when missing and proves a file can be written
// and removed there.
func probeDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("the backup directory cannot be created: %w", err)
	}
	f, err := os.CreateTemp(dir, ".karakuri-probe-")
	if err != nil {
		return fmt.Errorf("the backup directory is not writable: %w", err)
	}
	name := f.Name()
	f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("the backup directory does not allow removing files: %w", err)
	}
	return nil
}

// StoredPassphrase opens the schedule's sealed passphrase.
func StoredPassphrase(dataDir, encryptionKey string) (string, error) {
	s, err := LoadSchedule(dataDir)
	if err != nil {
		return "", err
	}
	if s.Passphrase == "" {
		return "", ErrNoPassphrase
	}
	key, err := crypto.KeyFromHex(encryptionKey)
	if err != nil {
		return "", err
	}
	plain, err := crypto.Decrypt(key, s.Passphrase)
	if err != nil {
		return "", fmt.Errorf("opening the stored passphrase: %w", err)
	}
	return string(plain), nil
}

// ErrNoPassphrase reports that no backup passphrase is stored.
var ErrNoPassphrase = errors.New("no backup passphrase is stored; set one in Settings, Backup")

// NextTime returns the schedule's next time after from.
func NextTime(expr string, from time.Time) (time.Time, error) {
	times, err := cronspec.Next(expr, "", from, 1)
	if err != nil {
		return time.Time{}, err
	}
	if len(times) == 0 {
		return time.Time{}, errors.New("the schedule never fires")
	}
	return times[0], nil
}

// missedTime reports the latest scheduled time after last and at or before
// now, when there is one: the backup a stopped server missed.
func missedTime(expr string, last, now time.Time) (time.Time, bool) {
	var missed time.Time
	at := last
	for i := 0; i < 100000; i++ {
		next, err := NextTime(expr, at)
		if err != nil || next.After(now) {
			break
		}
		missed = next
		at = next
	}
	return missed, !missed.IsZero()
}

// prune removes the oldest archives of installation in dir beyond retain.
// Archives of other installations sharing the directory are never touched.
func prune(dir, installation string, retain int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type archive struct {
		name string
		at   time.Time
	}
	var mine []archive
	for _, e := range entries {
		inst, at, ok := ParseArchiveName(e.Name())
		if ok && inst == installation && e.Type().IsRegular() {
			mine = append(mine, archive{e.Name(), at})
		}
	}
	if len(mine) <= retain {
		return nil, nil
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].at.After(mine[j].at) })
	var removed []string
	for _, a := range mine[retain:] {
		if err := os.Remove(filepath.Join(dir, a.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed = append(removed, a.name)
	}
	return removed, nil
}

// Sched is the server's scheduler, set by serve.
var Sched *Scheduler

// Scheduler runs the stored schedule on a service.
type Scheduler struct {
	svc           *Service
	dataDir       string
	encryptionKey string
	log           *zap.Logger
	now           func() time.Time
	wake          chan struct{}
}

// NewScheduler returns a scheduler for svc. It applies retention after each
// verified scheduled backup.
func NewScheduler(svc *Service, dataDir, encryptionKey string, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	sch := &Scheduler{svc: svc, dataDir: dataDir, encryptionKey: encryptionKey, log: log, now: time.Now, wake: make(chan struct{}, 1)}
	svc.afterSuccess = sch.retain
	return sch
}

// Reload makes a running scheduler reread the schedule.
func (sch *Scheduler) Reload() {
	select {
	case sch.wake <- struct{}{}:
	default:
	}
}

// Run schedules backups until ctx ends. On start it catches up one backup a
// stopped server missed, after a delay so start-up work is not slowed, and
// deletes run records past their retention once a day.
func (sch *Scheduler) Run(ctx context.Context) {
	sch.svc.Recover()
	sch.purgeRuns()
	purge := time.NewTicker(24 * time.Hour)
	defer purge.Stop()

	catchUp := true
	for {
		s, err := LoadSchedule(sch.dataDir)
		if err != nil {
			sch.log.Error("reading the backup schedule", zap.Error(err))
		}
		var wait <-chan time.Time
		var due time.Time
		catching := false
		if err == nil && s.Enabled {
			now := sch.now()
			last := now
			if s.LastScheduledAt != nil {
				last = *s.LastScheduledAt
			}
			if missed, ok := missedTime(s.Cron, last, now); ok && catchUp {
				due = missed
				wait = time.After(catchUpDelay)
				catching = true
				sch.log.Info("catching up a missed backup", zap.Time("scheduled", missed))
			} else if next, err := NextTime(s.Cron, maxTime(last, now)); err == nil {
				due = next
				wait = time.After(next.Sub(now))
			}
		}
		// A reload or the daily purge can wake the loop during the delay. The
		// missed backup is still owed then, so only a start with nothing to
		// catch up, or the catch-up itself firing, ends it.
		catchUp = catching

		select {
		case <-ctx.Done():
			return
		case <-sch.wake:
		case <-purge.C:
			sch.purgeRuns()
		case <-wait:
			sch.fire(ctx, due)
			catchUp = false
		}
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// fire runs the backup scheduled for due and records that it ran.
func (sch *Scheduler) fire(ctx context.Context, due time.Time) {
	s, err := LoadSchedule(sch.dataDir)
	if err != nil || !s.Enabled {
		return
	}
	at := due.UTC()
	s.LastScheduledAt = &at
	if err := storeSchedule(s); err != nil {
		sch.log.Error("recording the scheduled backup time", zap.Error(err))
	}
	pass, err := StoredPassphrase(sch.dataDir, sch.encryptionKey)
	if err != nil {
		sch.log.Error("scheduled backup has no usable passphrase", zap.Error(err))
		sch.svc.recordRefusal(models.BackupTriggerSchedule, s.Directory, err)
		return
	}
	_, _ = sch.svc.Create(ctx, Request{Trigger: models.BackupTriggerSchedule, Passphrase: pass, Directory: s.Directory})
}

// retain applies the schedule's retention after a verified scheduled backup.
func (sch *Scheduler) retain(run *models.BackupRun) {
	s, err := LoadSchedule(sch.dataDir)
	if err != nil {
		return
	}
	src := sch.svc.source()
	removed, err := prune(run.Directory, src.Installation, s.Retain)
	if err != nil {
		sch.log.Error("removing old backups", zap.String("directory", run.Directory), zap.Error(err))
	}
	for _, name := range removed {
		sch.log.Info("removed old backup", zap.String("archive", name))
	}
}

func (sch *Scheduler) purgeRuns() {
	n, err := models.DeleteBackupRunsBefore(time.Now().Add(-runRetention))
	if err != nil {
		sch.log.Error("removing old backup records", zap.Error(err))
		return
	}
	if n > 0 {
		sch.log.Debug("removed old backup records", zap.Int64("count", n))
	}
}
