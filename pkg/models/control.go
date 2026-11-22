package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Control is a named, typed, persisted value the dashboard operates and flows
// read and react to. It is the same idea as a Home Assistant helper: state the
// system owns rather than state a device owns, so a dashboard row and an
// automation can agree on "house mode" without either one owning the other.
//
// Kind decides how the value is rendered, coerced, and validated; Options holds
// the kind's own settings (a number's bounds, a select's choices) as JSON, and
// State holds the current value as JSON so a boolean stays a boolean across a
// restart.
type Control struct {
	ID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name string    `gorm:"uniqueIndex;not null" json:"name" description:"Display name, unique across controls."`
	Kind string    `gorm:"not null" json:"kind" description:"Control kind, one of the catalog in pkg/controls: boolean, number, select, text, datetime, buttons, lock, light, cover, climate, or fan."`
	Icon string    `json:"icon" description:"Line-awesome icon class shown beside the control."`
	// Options is the kind's own configuration as a JSON object. It is stored
	// opaquely because the schema belongs to the kind, not to this table.
	Options string `gorm:"type:text" json:"-"`
	// State is the current value as JSON. A press-only kind stores the value
	// of the last press; every press is announced even when it repeats.
	State string `gorm:"type:text" json:"-"`
	// AllowPublic lets anonymous viewers of a public dashboard read and set the
	// control, the same choice a widget trigger makes about a press.
	AllowPublic bool      `gorm:"default:false" json:"allow_public" description:"Whether anonymous viewers of a public dashboard may read and set it."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (c *Control) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// ListControls returns every control ordered by name.
func ListControls() ([]*Control, error) {
	var controls []*Control
	err := db.DB.Order("name").Find(&controls).Error
	return controls, err
}

// FindControlByID loads one control.
func FindControlByID(id uuid.UUID) (*Control, error) {
	var c Control
	if err := db.DB.First(&c, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateControl inserts a control.
func CreateControl(c *Control) error {
	return db.DB.Create(c).Error
}

// SaveControl persists every field of a control.
func SaveControl(c *Control) error {
	return db.DB.Save(c).Error
}

// DeleteControl removes a control.
func DeleteControl(id uuid.UUID) error {
	return db.DB.Delete(&Control{}, "id = ?", id).Error
}

// UpdateControlState writes only the current value, which is the write every
// press and toggle makes. Saving the whole row here would let a stale copy of
// the definition overwrite an edit made while the dashboard was open.
func UpdateControlState(id uuid.UUID, state string) error {
	return db.DB.Model(&Control{}).Where("id = ?", id).
		Updates(map[string]any{"state": state, "updated_at": time.Now()}).Error
}
