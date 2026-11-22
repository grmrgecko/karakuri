package backup

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// ErrNoArchive reports an archive name that is not an archive in the backup
// directory.
var ErrNoArchive = errors.New("no such archive")

// Listing is the archives in the backup directory and the backup running
// now, if any.
type Listing struct {
	Directory string            `json:"directory" description:"Backup directory."`
	Archives  []ArchiveInfo     `json:"archives" description:"Archives, newest first."`
	Running   *models.BackupRun `json:"running,omitempty" description:"The backup running now."`
}

// ScheduleView is the schedule as clients see it. The passphrase is never
// returned.
type ScheduleView struct {
	Enabled       bool              `json:"enabled" description:"Whether scheduled backups run."`
	Cron          string            `json:"cron" description:"Cron schedule in the server's time zone."`
	Description   string            `json:"description" description:"The schedule in words."`
	Directory     string            `json:"directory" description:"Absolute directory archives are written to."`
	Retain        int               `json:"retain" description:"Archives kept by the schedule."`
	PassphraseSet bool              `json:"passphrase_set" description:"Whether a passphrase is stored."`
	NextAt        *time.Time        `json:"next_at,omitempty" description:"Next scheduled backup."`
	Last          *models.BackupRun `json:"last,omitempty" description:"Most recent backup of any trigger."`
}

// Directory is the schedule's backup directory.
func (s *Service) Directory() (string, error) {
	sch, err := LoadSchedule(s.DataDir())
	if err != nil {
		return "", err
	}
	return sch.Directory, nil
}

// ListArchives lists the backup directory.
func (s *Service) ListArchives() (Listing, error) {
	dir, err := s.Directory()
	if err != nil {
		return Listing{}, err
	}
	list, err := List(dir)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Directory: dir, Archives: list}
	if running, err := models.ListRunningBackupRuns(); err == nil && len(running) > 0 {
		out.Running = running[0]
	}
	return out, nil
}

// Path resolves an archive name in the backup directory, answering
// ErrNoArchive for anything else.
func (s *Service) Path(name string) (string, error) {
	dir, err := s.Directory()
	if err != nil {
		return "", err
	}
	path, err := ArchivePath(dir, name)
	if err != nil {
		return "", ErrNoArchive
	}
	return path, nil
}

// VerifyArchive checks a named archive with the passphrase, or the stored
// one when it is empty.
func (s *Service) VerifyArchive(name, passphrase string) (*Manifest, error) {
	path, err := s.Path(name)
	if err != nil {
		return nil, err
	}
	if passphrase == "" {
		if passphrase, err = StoredPassphrase(s.DataDir(), s.EncryptionKey()); err != nil {
			return nil, err
		}
	}
	m, err := Verify(path, passphrase)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be restored: %w", name, err)
	}
	return m, nil
}

// DeleteArchive removes a named archive. Its run record stays.
func (s *Service) DeleteArchive(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.log.Info("backup deleted", zap.String("archive", name))
	return nil
}

// View describes the schedule.
func (s *Service) View() (ScheduleView, error) {
	sch, err := LoadSchedule(s.DataDir())
	if err != nil {
		return ScheduleView{}, err
	}
	v := ScheduleView{Enabled: sch.Enabled, Cron: sch.Cron, Description: cronspec.Describe(sch.Cron),
		Directory: sch.Directory, Retain: sch.Retain, PassphraseSet: sch.Passphrase != ""}
	if sch.Enabled {
		if next, err := NextTime(sch.Cron, time.Now()); err == nil {
			v.NextAt = &next
		}
	}
	if runs, err := models.ListBackupRuns(1); err == nil && len(runs) > 0 {
		v.Last = runs[0]
	}
	return v, nil
}

// overdueGrace is how long a scheduled backup may lag before it is overdue,
// covering a long-running archive and a server restarted near the time.
const overdueGrace = time.Hour

// Overdue reports the scheduled time a backup was due by and has missed: the
// schedule is enabled, and its first time after the newest successful backup
// passed more than an hour ago. With no successful backup it counts from the
// last time the scheduler fired, and with neither there is nothing to miss.
func (s *Service) Overdue(now time.Time) (time.Time, bool, error) {
	sch, err := LoadSchedule(s.DataDir())
	if err != nil {
		return time.Time{}, false, err
	}
	if !sch.Enabled {
		return time.Time{}, false, nil
	}
	var base time.Time
	if run, err := models.LastSucceededBackupRun(); err != nil {
		return time.Time{}, false, err
	} else if run != nil {
		base = run.StartedAt
	} else if sch.LastScheduledAt != nil {
		base = *sch.LastScheduledAt
	}
	if base.IsZero() {
		return time.Time{}, false, nil
	}
	due, err := NextTime(sch.Cron, base)
	if err != nil {
		return time.Time{}, false, err
	}
	return due, now.Sub(due) > overdueGrace, nil
}

// SaveView stores a schedule change, wakes the scheduler, and describes the
// result.
func (s *Service) SaveView(u ScheduleUpdate) (ScheduleView, error) {
	if _, err := SaveSchedule(s.DataDir(), s.EncryptionKey(), u); err != nil {
		return ScheduleView{}, err
	}
	if Sched != nil {
		Sched.Reload()
	}
	return s.View()
}
