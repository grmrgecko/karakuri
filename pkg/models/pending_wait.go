package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PendingWait is one node's held work for one key in one flow: a Trigger arm,
// a delayed message, a debounce window, or a partial fan-in group. Rows
// survive restarts and redeploys so the engine can resume the wait; they are
// removed when the wait fires, is cancelled, or is dropped, and with their
// flow.
type PendingWait struct {
	ID       uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	FlowID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_pending_wait_key" json:"flow_id" description:"Owning flow ID."`
	NodeID   string    `gorm:"not null;uniqueIndex:idx_pending_wait_key" json:"node_id" description:"Graph node ID holding the wait."`
	NodeType string    `gorm:"not null" json:"node_type" description:"Node type when the wait was armed."`
	Key      string    `gorm:"not null;uniqueIndex:idx_pending_wait_key" json:"key" description:"Node-defined wait key."`
	Token    string    `gorm:"not null" json:"token" description:"Token of the execution that owns the wait."`
	// Deadline is null for a state-only row, which has no waiter to resume.
	Deadline  *time.Time `json:"deadline,omitempty" description:"Absolute deadline, or null for state only."`
	RunID     uuid.UUID  `gorm:"type:uuid" json:"run_id" description:"Run that armed or last restarted the wait."`
	State     string     `gorm:"type:text;not null" json:"state" description:"JSON-encoded node state."`
	CreatedAt time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// ListPendingWaits returns every stored wait, for loading the registry at boot.
func ListPendingWaits() ([]*PendingWait, error) {
	var waits []*PendingWait
	err := db.DB.Find(&waits).Error
	return waits, err
}

// UpsertPendingWait writes a wait, replacing any row for the same flow, node,
// and key.
func UpsertPendingWait(w *PendingWait) error {
	return db.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "flow_id"}, {Name: "node_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"node_type", "token", "deadline", "run_id", "state", "updated_at",
		}),
	}).Create(w).Error
}

// DeletePendingWait removes one wait.
func DeletePendingWait(flowID uuid.UUID, nodeID, key string) error {
	return db.DB.Where(map[string]any{"flow_id": flowID, "node_id": nodeID, "key": key}).
		Delete(&PendingWait{}).Error
}

// DeleteFlowPendingWaits removes every wait belonging to a flow.
func DeleteFlowPendingWaits(tx *gorm.DB, flowID uuid.UUID) error {
	return tx.Where("flow_id = ?", flowID).Delete(&PendingWait{}).Error
}
