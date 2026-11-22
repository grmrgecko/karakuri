package models

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// MQTTDevice is a device: one described by hand or generated from discovery
// that names the topics it speaks on and the script that reads them, or an
// entity of another connection. The relay keeps the values its script or
// entity reports.
//
// It exists because a topic and a dot path cannot describe a real device. One
// device reports brightness on a message that says nothing about colour and
// colour on a message that says nothing about brightness; another spreads its
// state over several topics entirely. A binding that reads one path out of one
// topic sees a fraction of that and cannot tell a missing field from a changed
// one. A device sees every message it owns, decides what each one means, and
// accumulates the answer.
type MQTTDevice struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name         string    `gorm:"uniqueIndex;not null" json:"name" description:"Display name, unique across devices."`
	ConnectionID uuid.UUID `gorm:"type:uuid;not null;index" json:"connection_id" description:"MQTT connection the device speaks on."`
	// Topics are MQTT topic filters, so a device may name several or use the
	// wildcards the protocol already has ('+' for one level, '#' for the rest).
	// Stored as a JSON array because a device's topics are a list, not a schema.
	Topics string `gorm:"type:text" json:"-"`
	// StateScript reads one message. It receives the topic it arrived on and
	// returns the values that message carries — only those, since a message that
	// mentions nothing about brightness must not clear it.
	StateScript string `gorm:"type:text" json:"state_script" description:"JavaScript reading one message into the values it carries."`
	// Control names the control kind the device is drawn with, from the same
	// catalog a karakuri control picks from. It lives here rather than on the
	// dashboard row for the same reason a control's kind does: the device knows
	// what it is, and a row that had to be told would be told differently on
	// every dashboard.
	Control string `json:"control" description:"Control kind the device is drawn with, from the controls catalog."`
	// Options are the kind's own settings: a slider's bounds, a dropdown's
	// choices, the buttons of a remote, which features a light has. Stored as
	// a JSON object against the kind's declared schema, so a new kind needs no
	// column and no frontend change.
	Options string `gorm:"type:text" json:"-"`
	// PayloadScript turns one control change into a message. It returns the
	// topic to send on and the payload to send, so a device that commands needs
	// no separate topic field — and a script that returns no topic has decided
	// this change means nothing, which is the command-side counterpart of a
	// state script returning nothing.
	PayloadScript string `gorm:"type:text" json:"payload_script" description:"JavaScript turning a control change into {topic, payload}."`
	Description   string `json:"description" description:"What the device is, for whoever reads the list later."`
	// AvailabilityTopic carries the device's online and offline payloads,
	// usually a last-will message. Empty means the device is online whenever
	// its connection is up, unless ExpectEveryS says otherwise.
	AvailabilityTopic   string `json:"availability_topic" description:"Topic carrying online and offline payloads; empty for none."`
	AvailabilityOnline  string `gorm:"not null;default:online" json:"availability_online" description:"Payload meaning online; a JSON object whose state equals it also matches."`
	AvailabilityOffline string `gorm:"not null;default:offline" json:"availability_offline" description:"Payload meaning offline; a JSON object whose state equals it also matches."`
	// ExpectEveryS marks the device offline after this many seconds with no
	// message, for firmware whose last-will never fires. Zero is off.
	ExpectEveryS    int  `gorm:"not null;default:0" json:"expect_every_s" description:"Offline after this many seconds with no message; 0 is off, otherwise at least 60."`
	OfflineCommands bool `gorm:"not null;default:false" json:"offline_commands" description:"Send commands while the device is offline, for sleeping devices that queue them."`
	// StateQoS is the QoS asked for on the state and availability
	// subscriptions; CommandQoS and CommandRetain are what commands are
	// published with unless a payload script says otherwise.
	StateQoS      int  `gorm:"not null;default:0" json:"state_qos" description:"QoS of the state and availability subscriptions: 0, 1, or 2."`
	CommandQoS    int  `gorm:"not null;default:0" json:"command_qos" description:"QoS commands are published at: 0, 1, or 2."`
	CommandRetain bool `gorm:"not null;default:false" json:"command_retain" description:"Whether commands are published retained."`
	// StateTimeoutMS and PayloadTimeoutMS bound the device's scripts. Zero
	// means the default, so devices saved before the limits existed keep them.
	StateTimeoutMS   int `gorm:"not null;default:0" json:"state_timeout_ms" description:"State script time limit in milliseconds; 0 is the default of 250."`
	PayloadTimeoutMS int `gorm:"not null;default:0" json:"payload_timeout_ms" description:"Payload script time limit in milliseconds; 0 is the default of 2000."`
	// Z2MIEEE and Z2MBase record the Zigbee2MQTT device this was added from,
	// which is how a rename in Zigbee2MQTT is followed.
	Z2MIEEE string `gorm:"column:z2m_ieee" json:"z2m_ieee,omitempty" description:"IEEE address of the Zigbee2MQTT device this was added from."`
	Z2MBase string `gorm:"column:z2m_base" json:"z2m_base,omitempty" description:"Zigbee2MQTT base topic this was added from."`
	// Transport is how the device is reached: mqtt, through topics and
	// scripts, or esphome, through an ESPHome connection's entity named by
	// EntityKey, the entity's object ID. EntityType is the ESPHome entity
	// type, such as light or sensor.
	Transport  string `gorm:"not null;default:mqtt" json:"transport" description:"How the device is reached: mqtt, esphome, or frigate."`
	EntityKey  string `json:"entity_key,omitempty" description:"ESPHome object ID of the entity, for esphome devices."`
	EntityType string `json:"entity_type,omitempty" description:"ESPHome entity type, for esphome devices."`
	// Missing is set while the device is absent from the list its source
	// keeps: Zigbee2MQTT's devices, a connection's entities, or a discovery
	// config that was cleared.
	Missing bool `gorm:"not null;default:false" json:"missing" description:"Whether the device is absent from its source's device or entity list."`
	// AutoImported marks a device its connection's auto import created; only
	// these are removed when their source stops reporting them.
	AutoImported bool `gorm:"not null;default:false" json:"auto_imported" description:"Whether the connection's auto import created the device."`
	// DiscoveryKey is the Home Assistant MQTT discovery identity the device
	// was added from: <component>/<unique_id>, or the config topic when the
	// config has no unique ID.
	DiscoveryKey string `gorm:"index" json:"discovery_key,omitempty" description:"Home Assistant MQTT discovery identity the device was added from."`
	// LastReportedAt is when the device's source last reported it: live
	// MQTT activity, or presence in an entity list while connected.
	LastReportedAt *time.Time `json:"last_reported_at,omitempty" description:"When the device's source last reported it."`
	CreatedAt      time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt      time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// AutoImportTombstone records an MQTT-sourced device that auto import removed
// for not reporting. A retained replay of its discovery does not import it
// again; live activity removes the tombstone and imports it.
type AutoImportTombstone struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	ConnectionID uuid.UUID `gorm:"type:uuid;not null;index:idx_auto_import_tombstone,unique,priority:1" json:"connection_id"`
	SourceKey    string    `gorm:"not null;index:idx_auto_import_tombstone,unique,priority:2" json:"source_key"`
	RemovedAt    time.Time `json:"removed_at"`
}

// BeforeCreate assigns a UUID when none is set.
func (t *AutoImportTombstone) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

// ListAutoImportTombstones returns a connection's tombstones by source key.
func ListAutoImportTombstones(connectionID uuid.UUID) (map[string]bool, error) {
	var rows []AutoImportTombstone
	if err := db.DB.Where("connection_id = ?", connectionID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.SourceKey] = true
	}
	return out, nil
}

// AddAutoImportTombstone records a removed device's source key.
func AddAutoImportTombstone(connectionID uuid.UUID, key string, at time.Time) error {
	return db.DB.Where(AutoImportTombstone{ConnectionID: connectionID, SourceKey: key}).
		Assign(AutoImportTombstone{RemovedAt: at}).
		FirstOrCreate(&AutoImportTombstone{}).Error
}

// DeleteAutoImportTombstone forgets a source key.
func DeleteAutoImportTombstone(connectionID uuid.UUID, key string) error {
	return db.DB.Where("connection_id = ? AND source_key = ?", connectionID, key).Delete(&AutoImportTombstone{}).Error
}

// SetMQTTDeviceReportedAt records when a device's source last reported it.
func SetMQTTDeviceReportedAt(id uuid.UUID, at time.Time) error {
	return db.DB.Model(&MQTTDevice{}).Where("id = ?", id).UpdateColumn("last_reported_at", at).Error
}

// BeforeCreate assigns a UUID when none is set.
func (d *MQTTDevice) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return nil
}

// TopicList decodes the stored topic filters. A malformed column reads as no
// topics rather than failing the device, so one bad row cannot stop the relay.
func (d *MQTTDevice) TopicList() []string {
	if strings.TrimSpace(d.Topics) == "" {
		return nil
	}
	var topics []string
	if err := json.Unmarshal([]byte(d.Topics), &topics); err != nil {
		return nil
	}
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// OptionMap decodes the stored kind settings. A malformed column reads as no
// settings rather than failing the device, so one bad row cannot stop the
// relay.
func (d *MQTTDevice) OptionMap() map[string]any {
	if strings.TrimSpace(d.Options) == "" {
		return map[string]any{}
	}
	var opts map[string]any
	if err := json.Unmarshal([]byte(d.Options), &opts); err != nil {
		return map[string]any{}
	}
	if opts == nil {
		return map[string]any{}
	}
	return opts
}

// SetOptionMap stores the kind settings.
func (d *MQTTDevice) SetOptionMap(opts map[string]any) {
	if opts == nil {
		opts = map[string]any{}
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		d.Options = "{}"
		return
	}
	d.Options = string(raw)
}

// SetTopicList stores the topic filters.
func (d *MQTTDevice) SetTopicList(topics []string) {
	clean := make([]string, 0, len(topics))
	for _, t := range topics {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		d.Topics = "[]"
		return
	}
	d.Topics = string(raw)
}

// ListMQTTDevices returns every device ordered by name.
func ListMQTTDevices() ([]*MQTTDevice, error) {
	var devices []*MQTTDevice
	err := db.DB.Order("name").Find(&devices).Error
	return devices, err
}

// MQTTDeviceIDsOnConnections returns the IDs of the devices that speak on
// any of the given connections.
func MQTTDeviceIDsOnConnections(connectionIDs []uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := db.DB.Model(&MQTTDevice{}).Where("connection_id IN ?", connectionIDs).Pluck("id", &ids).Error
	return ids, err
}

// FindMQTTDeviceByID loads one device.
func FindMQTTDeviceByID(id uuid.UUID) (*MQTTDevice, error) {
	var d MQTTDevice
	if err := db.DB.First(&d, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// CreateMQTTDevice inserts a device.
func CreateMQTTDevice(d *MQTTDevice) error {
	return db.DB.Create(d).Error
}

// SaveMQTTDevice persists every field of a device.
func SaveMQTTDevice(d *MQTTDevice) error {
	return db.DB.Save(d).Error
}

// DeleteMQTTDevice removes a device and its stored state. It returns
// gorm.ErrRecordNotFound when no device has that ID.
func DeleteMQTTDevice(id uuid.UUID) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("device_id = ?", id).Delete(&MQTTDeviceState{}).Error; err != nil {
			return err
		}
		res := tx.Delete(&MQTTDevice{}, "id = ?", id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// Script time limits: the defaults a device with no limit set runs under, and
// the ranges a limit may be set to.
const (
	DefaultStateTimeout   = 250 * time.Millisecond
	DefaultPayloadTimeout = 2 * time.Second
	MinStateTimeoutMS     = 50
	MaxStateTimeoutMS     = 5000
	MinPayloadTimeoutMS   = 100
	MaxPayloadTimeoutMS   = 10000
)

// StateTimeout is the time the device's state script may run.
func (d *MQTTDevice) StateTimeout() time.Duration {
	if d.StateTimeoutMS <= 0 {
		return DefaultStateTimeout
	}
	return time.Duration(d.StateTimeoutMS) * time.Millisecond
}

// PayloadTimeout is the time the device's payload script may run.
func (d *MQTTDevice) PayloadTimeout() time.Duration {
	if d.PayloadTimeoutMS <= 0 {
		return DefaultPayloadTimeout
	}
	return time.Duration(d.PayloadTimeoutMS) * time.Millisecond
}

// ListMQTTDevicesOnConnection returns the devices that speak on one
// connection.
func ListMQTTDevicesOnConnection(connectionID uuid.UUID) ([]*MQTTDevice, error) {
	var devices []*MQTTDevice
	err := db.DB.Where("connection_id = ?", connectionID).Order("name").Find(&devices).Error
	return devices, err
}
