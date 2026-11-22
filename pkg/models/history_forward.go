package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// JSONColumn stores a value as JSON text, so lists and maps that belong to
// one row need no table of their own.
type JSONColumn[T any] struct {
	V T
}

// Value encodes the column for the database.
func (c JSONColumn[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(c.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan decodes the column from the database. NULL leaves the zero value.
func (c *JSONColumn[T]) Scan(src any) error {
	var zero T
	c.V = zero
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return json.Unmarshal([]byte(v), &c.V)
	case []byte:
		if len(v) == 0 {
			return nil
		}
		return json.Unmarshal(v, &c.V)
	}
	return fmt.Errorf("cannot scan %T into a JSON column", src)
}

// MarshalJSON writes the wrapped value.
func (c JSONColumn[T]) MarshalJSON() ([]byte, error) { return json.Marshal(c.V) }

// UnmarshalJSON reads the wrapped value.
func (c *JSONColumn[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &c.V) }

// History forward source types.
const (
	ForwardSourceControl    = "control"
	ForwardSourceMQTTDevice = "mqtt_device"
	ForwardSourceHA         = "ha"
)

// HistoryForward records which control, MQTT device, or Home Assistant
// values are written to which time-series connections. Forwards are live
// settings like connections: saving one takes effect at once, with no draft.
type HistoryForward struct {
	ID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name    string    `gorm:"not null" json:"name" description:"Display name."`
	Enabled bool      `gorm:"default:true" json:"enabled" description:"Whether the forward records anything."`
	// SourceType is control, mqtt_device, or ha.
	SourceType string `gorm:"not null;index" json:"source_type" description:"control, mqtt_device, or ha."`
	// SourceIDs lists controls or devices; empty selects all of that type.
	SourceIDs JSONColumn[[]uuid.UUID] `gorm:"type:text" json:"source_ids" description:"Controls or MQTT devices recorded; empty records all of them."`
	// OwnerID is set when the forward belongs to one control or device editor;
	// SourceIDs then holds exactly that ID.
	OwnerID *uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"owner_id,omitempty" description:"Control or device whose editor owns this forward."`
	// ConnectionID is the Home Assistant connection of an ha forward.
	ConnectionID *uuid.UUID           `gorm:"type:uuid" json:"connection_id,omitempty" description:"Home Assistant connection for ha forwards."`
	Include      JSONColumn[[]string] `gorm:"type:text" json:"include" description:"Entity ID patterns recorded, with * for any run of characters."`
	Exclude      JSONColumn[[]string] `gorm:"type:text" json:"exclude" description:"Entity ID patterns left out."`
	// Attributes are Home Assistant attributes recorded besides the state.
	Attributes    JSONColumn[[]string]    `gorm:"type:text" json:"attributes" description:"Home Assistant attributes recorded besides the state."`
	AllAttributes bool                    `json:"all_attributes" description:"Record every numeric Home Assistant attribute."`
	Destinations  JSONColumn[[]uuid.UUID] `gorm:"type:text" json:"destinations" description:"Writable time-series connections the values go to."`
	// Metric overrides the default metric or measurement name.
	Metric        string                        `json:"metric,omitempty" description:"Metric or measurement name; empty uses karakuri_control, karakuri_mqtt_device, or karakuri_ha_entity."`
	Labels        JSONColumn[map[string]string] `gorm:"type:text" json:"labels" description:"Extra labels added to every point."`
	MinIntervalMS int64                         `json:"min_interval_ms" description:"Changes within this many milliseconds are coalesced to the latest."`
	CreatedAt     time.Time                     `json:"created_at" description:"Creation timestamp."`
	UpdatedAt     time.Time                     `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (f *HistoryForward) BeforeCreate(tx *gorm.DB) error {
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return nil
}

// ListHistoryForwards returns every forward ordered by name.
func ListHistoryForwards() ([]*HistoryForward, error) {
	var out []*HistoryForward
	err := db.DB.Order("name").Find(&out).Error
	return out, err
}

// FindHistoryForward loads one forward.
func FindHistoryForward(id uuid.UUID) (*HistoryForward, error) {
	var f HistoryForward
	if err := db.DB.First(&f, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// FindOwnedHistoryForward loads the forward a control or device editor owns.
func FindOwnedHistoryForward(owner uuid.UUID) (*HistoryForward, error) {
	var f HistoryForward
	if err := db.DB.First(&f, "owner_id = ?", owner).Error; err != nil {
		return nil, err
	}
	return &f, nil
}
