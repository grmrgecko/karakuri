package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
)

// Backup run triggers.
const (
	BackupTriggerManual   = "manual"
	BackupTriggerSchedule = "schedule"
	BackupTriggerFlow     = "flow"
	BackupTriggerCLI      = "cli"
)

// Backup run states.
const (
	BackupRunning   = "running"
	BackupSucceeded = "succeeded"
	BackupFailed    = "failed"
)

// Backup verification results.
const (
	BackupVerifyPending = "pending"
	BackupVerifyOK      = "ok"
	BackupVerifyFailed  = "failed"
)

// BackupErrorMax is the longest failure reason a run keeps, in characters.
const BackupErrorMax = 2000

// BackupRun records one backup attempt, from whichever trigger started it.
type BackupRun struct {
	ID         uint             `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	StartedAt  time.Time        `gorm:"index;not null" json:"started_at" description:"When the backup started."`
	FinishedAt *time.Time       `json:"finished_at,omitempty" description:"When it finished; absent while running."`
	Trigger    string           `gorm:"not null" json:"trigger" description:"manual, schedule, flow, or cli."`
	UserID     *uuid.UUID       `gorm:"type:uuid" json:"user_id,omitempty" description:"User who asked for it."`
	FlowID     *uuid.UUID       `gorm:"type:uuid" json:"flow_id,omitempty" description:"Flow that started it."`
	Directory  string           `json:"directory" description:"Directory the archive was written to."`
	Archive    string           `gorm:"index" json:"archive,omitempty" description:"Archive file name."`
	Size       int64            `json:"size" description:"Archive size in bytes."`
	Status     string           `gorm:"not null" json:"status" description:"running, succeeded, or failed."`
	Error      string           `json:"error,omitempty" description:"Why the backup failed."`
	Verified   string           `gorm:"not null" json:"verified" description:"pending, ok, or failed."`
	Counts     map[string]int64 `gorm:"serializer:json;type:json" json:"counts,omitempty" description:"Rows of each kind the archive holds."`
}

// CreateBackupRun inserts a run.
func CreateBackupRun(run *BackupRun) error {
	run.Error = capBackupError(run.Error)
	return db.DB.Create(run).Error
}

// SaveBackupRun writes every field of an existing run.
func SaveBackupRun(run *BackupRun) error {
	run.Error = capBackupError(run.Error)
	return db.DB.Save(run).Error
}

// capBackupError cuts a failure reason to BackupErrorMax characters. Cutting
// by bytes could split a character and leave invalid UTF-8, which PostgreSQL
// refuses.
func capBackupError(reason string) string {
	n := 0
	for i := range reason {
		if n == BackupErrorMax {
			return reason[:i]
		}
		n++
	}
	return reason
}

// ListBackupRuns returns the most recent runs, newest first.
func ListBackupRuns(limit int) ([]*BackupRun, error) {
	var runs []*BackupRun
	err := db.DB.Order("started_at DESC, id DESC").Limit(limit).Find(&runs).Error
	return runs, err
}

// LastSucceededBackupRun returns the newest successful run, or nil when no
// backup has succeeded.
func LastSucceededBackupRun() (*BackupRun, error) {
	var runs []*BackupRun
	if err := db.DB.Where("status = ?", BackupSucceeded).Order("started_at DESC, id DESC").Limit(1).Find(&runs).Error; err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return runs[0], nil
}

// ListRunningBackupRuns returns runs still marked running.
func ListRunningBackupRuns() ([]*BackupRun, error) {
	var runs []*BackupRun
	err := db.DB.Where("status = ?", BackupRunning).Find(&runs).Error
	return runs, err
}

// FindBackupRunByArchive returns the latest run that wrote the named archive.
func FindBackupRunByArchive(name string) (*BackupRun, error) {
	var run BackupRun
	err := db.DB.Where("archive = ?", name).Order("id DESC").First(&run).Error
	return &run, err
}

// DeleteBackupRunsBefore removes runs started before the cutoff.
func DeleteBackupRunsBefore(cutoff time.Time) (int64, error) {
	res := db.DB.Where("started_at < ?", cutoff).Delete(&BackupRun{})
	return res.RowsAffected, res.Error
}
