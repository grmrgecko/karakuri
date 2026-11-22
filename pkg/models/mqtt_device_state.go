package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm/clause"
)

// MQTTDeviceState is the last merged state of one MQTT device. It is
// restored at startup, marked stale until the device reports again, so a
// device that reports only on change is not blank after a restart.
type MQTTDeviceState struct {
	DeviceID uuid.UUID `gorm:"type:uuid;primaryKey" json:"device_id" description:"MQTT device ID."`
	// Control is the device's control kind when the state was stored. State
	// stored under another kind is not restored, since its values may not fit.
	Control    string    `json:"control" description:"Control kind the state was stored under."`
	State      string    `gorm:"type:text;not null" json:"state" description:"JSON-encoded merged state."`
	ReportedAt time.Time `json:"reported_at" description:"When the state script last merged a report."`
	// Availability is online, offline, or unknown when the state was stored.
	Availability       string    `json:"availability" description:"online, offline, or unknown."`
	AvailabilitySource string    `json:"availability_source" description:"What decided the availability: connection, topic, interval, or default."`
	AvailabilityAt     time.Time `json:"availability_at" description:"When the availability last changed."`
	UpdatedAt          time.Time `json:"updated_at" description:"Last write timestamp."`
}

// FindMQTTDeviceState returns a device's stored state.
func FindMQTTDeviceState(deviceID uuid.UUID) (*MQTTDeviceState, error) {
	var s MQTTDeviceState
	err := db.DB.Where("device_id = ?", deviceID).First(&s).Error
	return &s, err
}

// UpsertMQTTDeviceState writes a device's state.
func UpsertMQTTDeviceState(s *MQTTDeviceState) error {
	return db.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"control", "state", "reported_at", "availability", "availability_source", "availability_at", "updated_at"}),
	}).Create(s).Error
}

// DeleteMQTTDeviceState removes a device's stored state.
func DeleteMQTTDeviceState(deviceID uuid.UUID) error {
	return db.DB.Where("device_id = ?", deviceID).Delete(&MQTTDeviceState{}).Error
}
