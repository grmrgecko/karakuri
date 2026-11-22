package models

import (
	"time"
)

// Event categories.
const (
	EventDevice     = "device"
	EventConnection = "connection"
	EventFlow       = "flow"
	EventAgent      = "agent"
	EventSecurity   = "security"
	EventSystem     = "system"
	EventCustom     = "custom"
)

// Event levels.
const (
	EventInfo    = "info"
	EventWarning = "warning"
	EventError   = "error"
)

// EventCategories lists every category in display order.
var EventCategories = []string{EventDevice, EventConnection, EventFlow, EventAgent, EventSecurity, EventSystem, EventCustom}

// Event is one entry in the event log: something that happened, to what, and
// who or what did it. Repeats within a minute fold into one row with a count.
type Event struct {
	ID          uint           `gorm:"primaryKey;index:idx_events_category,priority:2;index:idx_events_subject,priority:3;index:idx_events_level,priority:2;index:idx_events_actor,priority:3" json:"id" description:"Unique identifier; higher is newer."`
	At          time.Time      `gorm:"index;not null" json:"at" description:"First occurrence."`
	LastAt      time.Time      `gorm:"not null" json:"last_at" description:"Last occurrence when repeats were folded."`
	Count       int            `gorm:"not null;default:1" json:"count" description:"Occurrences folded into this entry."`
	Category    string         `gorm:"not null;index:idx_events_category,priority:1" json:"category" description:"device, connection, flow, agent, security, system, or custom."`
	Level       string         `gorm:"not null;index:idx_events_level,priority:1" json:"level" description:"info, warning, or error."`
	SubjectKind string         `gorm:"index:idx_events_subject,priority:1" json:"subject_kind,omitempty" description:"What the entry is about: control, device, connection, flow, and so on."`
	SubjectID   string         `gorm:"index:idx_events_subject,priority:2" json:"subject_id,omitempty" description:"ID of the subject."`
	SubjectName string         `json:"subject_name,omitempty" description:"Name of the subject when the entry was made."`
	Message     string         `gorm:"not null" json:"message" description:"What happened."`
	Details     map[string]any `gorm:"serializer:json;type:json" json:"details,omitempty" description:"Structured fields such as old and new value or the error."`
	ActorKind   string         `gorm:"not null;index:idx_events_actor,priority:1" json:"actor_kind" description:"user, apikey, flow, agent, system, or anonymous."`
	ActorID     string         `gorm:"index:idx_events_actor,priority:2" json:"actor_id,omitempty" description:"ID of the actor."`
	ActorName   string         `json:"actor_name,omitempty" description:"Name of the actor."`
	Via         string         `json:"via,omitempty" description:"dashboard, api, mcp, cli, or flow."`
	DashboardID string         `json:"dashboard_id,omitempty" description:"Dashboard a change was made from."`
	RunID       string         `json:"run_id,omitempty" description:"Run of a flow actor."`
	Address     string         `json:"address,omitempty" description:"Client address of a network action."`
}
