package models

import (
	"github.com/google/uuid"
)

// FlowDependency records one external reference a flow's draft graph makes,
// such as a connection or a shared subflow. Rebuilt on every graph save.
type FlowDependency struct {
	ID     uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	FlowID uuid.UUID `gorm:"type:uuid;not null;index" json:"flow_id" description:"Dependent flow ID."`
	Kind   string    `gorm:"not null" json:"kind" description:"Dependency kind: connection or subflow."`
	RefID  string    `gorm:"not null" json:"ref_id" description:"Referenced resource identifier."`
}
