package mqttrelay

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"gorm.io/gorm"
)

// minExpectEveryS is the shortest expected reporting interval. The sweep runs
// every 15 seconds, so anything shorter would flap on ordinary jitter.
const minExpectEveryS = 60

// Origin is where a definition was added from: a Zigbee2MQTT device, a Home
// Assistant MQTT discovery config, and whether auto import added it. A zero
// Origin on an update keeps whatever the device already records.
type Origin struct {
	IEEE         string
	Base         string
	DiscoveryKey string
	Auto         bool
}

var (
	savedMu sync.Mutex
	onSaved []func()
)

// OnDevicesChanged registers a function called after any device is saved or
// deleted, after the running set has been reloaded. The Zigbee2MQTT sync uses
// it to follow the devices it watches.
func OnDevicesChanged(fn func()) {
	savedMu.Lock()
	onSaved = append(onSaved, fn)
	savedMu.Unlock()
}

// devicesChanged reloads the running set and tells every listener.
func devicesChanged() {
	if D != nil {
		D.Reload()
	}
	savedMu.Lock()
	fns := append([]func(){}, onSaved...)
	savedMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// SaveDefinition validates and stores a definition, creating a device when id
// is uuid.Nil, then reloads the running devices. REST, MCP, and Zigbee2MQTT
// discovery all write devices through it so they apply the same rules.
func SaveDefinition(id uuid.UUID, def Definition, origin Origin) (*models.MQTTDevice, error) {
	if err := ValidateDefinition(def); err != nil {
		return nil, err
	}
	options, err := NormalizeDefinitionOptions(def)
	if err != nil {
		return nil, err
	}

	d := &models.MQTTDevice{}
	if id != uuid.Nil {
		if d, err = models.FindMQTTDeviceByID(id); err != nil {
			return nil, apperrors.ErrNotFound.WithMessage("device not found")
		}
	}
	ApplyDefinition(d, def, options)
	if origin.IEEE != "" {
		d.Z2MIEEE = origin.IEEE
		d.Z2MBase = origin.Base
	}
	if origin.DiscoveryKey != "" {
		d.DiscoveryKey = origin.DiscoveryKey
	}
	if origin.Auto && id == uuid.Nil {
		d.AutoImported = true
		now := time.Now()
		d.LastReportedAt = &now
	}

	// One karakuri device per discovery config, for the same reason as below.
	if d.DiscoveryKey != "" {
		var count int64
		q := db.DB.Model(&models.MQTTDevice{}).
			Where("connection_id = ? AND discovery_key = ?", d.ConnectionID, d.DiscoveryKey)
		if d.ID != uuid.Nil {
			q = q.Where("id <> ?", d.ID)
		}
		if err := q.Count(&count).Error; err != nil {
			return nil, apperrors.ErrInternal.WithMessage(err.Error())
		}
		if count > 0 {
			return nil, apperrors.ErrValidation.WithMessage("discovery config " + d.DiscoveryKey + " is already added")
		}
	}

	// One karakuri device per Zigbee2MQTT device, so discovery's added mark
	// and the rename sync have a single device to follow.
	if d.Z2MIEEE != "" {
		var count int64
		q := db.DB.Model(&models.MQTTDevice{}).
			Where("connection_id = ? AND z2m_base = ? AND z2m_ieee = ?", d.ConnectionID, d.Z2MBase, d.Z2MIEEE)
		if d.ID != uuid.Nil {
			q = q.Where("id <> ?", d.ID)
		}
		if err := q.Count(&count).Error; err != nil {
			return nil, apperrors.ErrInternal.WithMessage(err.Error())
		}
		if count > 0 {
			return nil, apperrors.ErrValidation.WithMessage("Zigbee2MQTT device " + d.Z2MIEEE + " is already added")
		}
	}

	// One karakuri device per entity, for the same reason.
	if IsEntityTransport(d.Transport) {
		var count int64
		q := db.DB.Model(&models.MQTTDevice{}).
			Where("connection_id = ? AND transport = ? AND entity_key = ?", d.ConnectionID, d.Transport, d.EntityKey)
		if d.ID != uuid.Nil {
			q = q.Where("id <> ?", d.ID)
		}
		if err := q.Count(&count).Error; err != nil {
			return nil, apperrors.ErrInternal.WithMessage(err.Error())
		}
		if count > 0 {
			return nil, apperrors.ErrValidation.WithMessage(transportLabel(d.Transport) + " entity " + d.EntityKey + " is already added")
		}
	}

	if id == uuid.Nil {
		err = models.CreateMQTTDevice(d)
	} else {
		err = models.SaveMQTTDevice(d)
	}
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	devicesChanged()
	return d, nil
}

// DeleteDevice removes a device and its stored state, then reloads. It
// returns a not found error when no device has that ID.
func DeleteDevice(id uuid.UUID) error {
	if err := models.DeleteMQTTDevice(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.ErrNotFound.WithMessage("device not found")
		}
		return apperrors.ErrInternal.WithMessage(err.Error())
	}
	devicesChanged()
	return nil
}

// SetEntityKey points an entity device at its entity's new ID after the
// entity was renamed where it lives, clearing any missing mark.
func SetEntityKey(id uuid.UUID, key string) error {
	if err := db.DB.Model(&models.MQTTDevice{}).Where("id = ?", id).
		Updates(map[string]any{"entity_key": key, "missing": false}).Error; err != nil {
		return apperrors.ErrInternal.WithMessage(err.Error())
	}
	if D != nil {
		D.Reload()
	}
	return nil
}

// SetMissing records whether a device added from Zigbee2MQTT, a discovery
// config, or an entity connection is absent from its source, and tells
// dashboards showing it.
func SetMissing(id uuid.UUID, missing bool) error {
	if err := db.DB.Model(&models.MQTTDevice{}).Where("id = ?", id).Update("missing", missing).Error; err != nil {
		return apperrors.ErrInternal.WithMessage(err.Error())
	}
	if D != nil {
		D.Reload()
	}
	mq.B.Publish(DeviceTopic(id), "mqtt_device_missing", map[string]any{
		"device_id": id.String(),
		"missing":   missing,
	})
	return nil
}
