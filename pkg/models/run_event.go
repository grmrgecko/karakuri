package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
)

// RunEvent is one recorded event within a run: a node state transition or a
// debug emission. Events are deleted together with their run when history retention
// removes it.
type RunEvent struct {
	ID      uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	RunID   uuid.UUID `gorm:"type:uuid;not null;index" json:"run_id" description:"Run this event belongs to."`
	NodeID  string    `json:"node_id" description:"Graph node the event concerns."`
	Attempt int       `json:"attempt" description:"Execution attempt number, 1-based."`
	Event   string    `json:"event" description:"Event kind: node_state or debug."`
	State   string    `json:"state,omitempty" description:"Node state for node_state events."`
	Payload string    `gorm:"type:text" json:"payload,omitempty" description:"Event payload JSON."`
	At      time.Time `json:"at" description:"Event timestamp."`
}

// CreateRunEvent inserts one run event row.
func CreateRunEvent(ev *RunEvent) error {
	return db.DB.Create(ev).Error
}

// ListRunEvents returns a run's events in insertion order.
func ListRunEvents(runID uuid.UUID) ([]*RunEvent, error) {
	var events []*RunEvent
	err := db.DB.Where("run_id = ?", runID).Order("id ASC").Find(&events).Error
	return events, err
}
