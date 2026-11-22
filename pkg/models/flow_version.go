package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
)

// FlowVersion is one stored graph for a flow. Version 0 is the editable
// draft; deploys freeze immutable copies with increasing version numbers.
type FlowVersion struct {
	HasSettings bool       `gorm:"not null;default:false" json:"-"`
	Name        string     `json:"-"`
	Description string     `json:"-"`
	ErrorFlowID *uuid.UUID `gorm:"type:uuid" json:"-"`
	ID          uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	FlowID      uuid.UUID  `gorm:"type:uuid;not null;index" json:"flow_id" description:"Parent flow ID."`
	Version     int        `gorm:"not null;default:0" json:"version" description:"Version number; 0 is the draft."`
	Graph       string     `gorm:"type:text" json:"-"`
	Comment     string     `json:"comment" description:"Deploy comment."`
	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty" description:"User who created this version."`
	CreatedBy   *User      `gorm:"foreignKey:CreatedByID" json:"created_by,omitempty" description:"User who created this version."`
	// RunAsID is the user runs of this version act as: whoever deployed it.
	// A faceplate apply freezes a new version but keeps the deployer's, so a
	// faceplate-level user never changes whose access a flow runs with.
	RunAsID   *uuid.UUID `gorm:"type:uuid" json:"run_as_id,omitempty" description:"User runs of this version act as."`
	CreatedAt time.Time  `json:"created_at" description:"Creation timestamp."`
}

// FindFlowVersionByID returns a flow version looked up by row ID.
func FindFlowVersionByID(id uint) (*FlowVersion, error) {
	var version FlowVersion
	err := db.DB.First(&version, id).Error
	return &version, err
}

// FindFlowVersion returns a flow's version looked up by version number.
func FindFlowVersion(flowID uuid.UUID, version int) (*FlowVersion, error) {
	var v FlowVersion
	err := db.DB.Where("flow_id = ? AND version = ?", flowID, version).First(&v).Error
	return &v, err
}

// ListFlowVersions returns a flow's deployed versions, newest first.
// The draft (version 0) is excluded.
func ListFlowVersions(flowID uuid.UUID) ([]*FlowVersion, error) {
	var versions []*FlowVersion
	err := db.DB.Preload("CreatedBy").
		Where("flow_id = ? AND version > 0", flowID).
		Order("version DESC").Find(&versions).Error
	return versions, err
}

// NextFlowVersion returns the next version number to assign on deploy.
func NextFlowVersion(flowID uuid.UUID) (int, error) {
	var maxVersion int
	err := db.DB.Model(&FlowVersion{}).
		Where("flow_id = ?", flowID).
		Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error
	return maxVersion + 1, err
}

// ApplySettings copies the frozen authoring settings into an in-memory flow.
func (v *FlowVersion) ApplySettings(f *Flow) {
	if !v.HasSettings {
		return
	}
	f.Name, f.Description, f.ErrorFlowID = v.Name, v.Description, v.ErrorFlowID
}
