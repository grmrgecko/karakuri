package models

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Run is the persisted record of one flow execution. Each flow retains its
// latest 100 completed runs and every active run.
type Run struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	FlowID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"flow_id" description:"Flow this run executed."`
	FlowVersionID  uint       `json:"flow_version_id" description:"Graph version that was executed."`
	TriggerNodeID  string     `json:"trigger_node_id" description:"Node that started the run."`
	TriggerPayload string     `gorm:"type:text" json:"trigger_payload,omitempty" description:"Initial message payload JSON."`
	Status         string     `gorm:"not null" json:"status" description:"Run status: running, succeeded, failed, or canceled."`
	StartedAt      time.Time  `json:"started_at" description:"Start timestamp."`
	EndedAt        *time.Time `json:"ended_at,omitempty" description:"End timestamp."`
	Error          string     `json:"error,omitempty" description:"Terminal error message."`
	HadFailures    bool       `gorm:"default:false" json:"had_failures" description:"Whether any node failed, even if handled by an error branch."`
	// ResumedFromRunID is the run that armed a restored wait. It stays set
	// after that run is pruned by retention.
	ResumedFromRunID *uuid.UUID `gorm:"type:uuid" json:"resumed_from_run_id,omitempty" description:"Run that armed the restored wait this run continues."`
}

// ReplaceLastRun records a new run and prunes completed history beyond 100 rows.
// Active runs retain their events even when another trigger fires.
func ReplaceLastRun(run *Run) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := pruneRunHistory(tx, run.FlowID); err != nil {
			return err
		}
		return tx.Create(run).Error
	})
}

// FinishRun records a run's terminal state.
func FinishRun(id uuid.UUID, status string, endedAt time.Time, errMsg string, hadFailures bool) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		var run Run
		if err := tx.First(&run, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.Model(&run).Updates(map[string]any{
			"status": status, "ended_at": endedAt, "error": errMsg, "had_failures": hadFailures,
		}).Error; err != nil {
			return err
		}
		return pruneRunHistory(tx, run.FlowID)
	})
}

// FindRunByID returns a run looked up by ID.
func FindRunByID(id uuid.UUID) (*Run, error) {
	var run Run
	err := db.DB.Where("id = ?", id).First(&run).Error
	return &run, err
}

// FindLastRunByFlow returns the flow's persisted run, newest first.
func FindLastRunByFlow(flowID uuid.UUID) (*Run, error) {
	var run Run
	err := db.DB.Where("flow_id = ?", flowID).Order("started_at DESC").First(&run).Error
	return &run, err
}

func pruneRunHistory(tx *gorm.DB, flowID uuid.UUID) error {
	var oldIDs []uuid.UUID
	// The limit bounds nothing; MySQL accepts OFFSET only after a LIMIT.
	if err := tx.Model(&Run{}).Where("flow_id = ? AND status != ?", flowID, "running").
		Order("started_at DESC, id DESC").Limit(math.MaxInt32).Offset(100).Pluck("id", &oldIDs).Error; err != nil {
		return err
	}
	if len(oldIDs) > 0 {
		if err := tx.Where("run_id IN ?", oldIDs).Delete(&RunEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", oldIDs).Delete(&Run{}).Error; err != nil {
			return err
		}
	}
	return nil
}
