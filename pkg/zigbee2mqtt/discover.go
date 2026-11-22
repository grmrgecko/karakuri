package zigbee2mqtt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// discoverTimeout bounds the wait for the retained device list. Zigbee2MQTT
// retains it, so it arrives at once whenever Zigbee2MQTT has ever run on the
// base topic.
const discoverTimeout = 5 * time.Second

// Subscriber is the part of an MQTT driver discovery needs.
type Subscriber interface {
	Subscribe(filter string, qos byte, handler func(topic string, payload []byte)) (func(), error)
}

// NormalizeBase trims a base topic and applies the default.
func NormalizeBase(base string) string {
	base = strings.Trim(strings.TrimSpace(base), "/")
	if base == "" {
		return DefaultBase
	}
	return base
}

// ReadDevices waits for the retained device list on <base>/bridge/devices.
func ReadDevices(ctx context.Context, sub Subscriber, base string) ([]Device, error) {
	topic := NormalizeBase(base) + "/bridge/devices"
	got := make(chan []byte, 1)
	drop, err := sub.Subscribe(topic, 0, func(_ string, payload []byte) {
		select {
		case got <- payload:
		default:
		}
	})
	if err != nil {
		return nil, fmt.Errorf("subscribing to %s: %w", topic, err)
	}
	defer drop()

	ctx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()
	select {
	case payload := <-got:
		return ParseDevices(payload)
	case <-ctx.Done():
		return nil, apperrors.ErrNotFound.WithMessage("Zigbee2MQTT not found on " + topic +
			"; check the base topic and that Zigbee2MQTT has started on this broker")
	}
}

// Discovered is one Zigbee2MQTT device as discovery lists it.
type Discovered struct {
	IEEEAddress  string   `json:"ieee_address" description:"IEEE address, which Zigbee2MQTT never changes."`
	FriendlyName string   `json:"friendly_name" description:"Name in Zigbee2MQTT, which its topics use."`
	Type         string   `json:"type" description:"Router or EndDevice."`
	PowerSource  string   `json:"power_source,omitempty" description:"Mains or battery."`
	Model        string   `json:"model,omitempty" description:"Model."`
	Vendor       string   `json:"vendor,omitempty" description:"Vendor."`
	Description  string   `json:"description,omitempty" description:"What Zigbee2MQTT says the device is."`
	Kind         string   `json:"kind,omitempty" description:"Control kind the device would be added as."`
	Commandable  bool     `json:"commandable" description:"Whether the added device would take commands."`
	Fields       []string `json:"fields,omitempty" description:"Other values the device reports, kept as fields."`
	CanAdd       bool     `json:"can_add" description:"Whether the device can be added."`
	Reason       string   `json:"reason,omitempty" description:"Why the device cannot be added."`
	DeviceID     string   `json:"device_id,omitempty" description:"karakuri device already added from it."`
}

// Discover lists a device list against the devices karakuri already has on
// the same connection and base topic. The coordinator is left out.
func Discover(devices []Device, existing []*models.MQTTDevice, connID uuid.UUID, base string) []Discovered {
	base = NormalizeBase(base)
	added := map[string]string{}
	for _, m := range existing {
		if m.ConnectionID == connID && m.Z2MBase == base && m.Z2MIEEE != "" {
			added[m.Z2MIEEE] = m.ID.String()
		}
	}
	var out []Discovered
	for _, d := range devices {
		if d.IsCoordinator() {
			continue
		}
		entry := Discovered{
			IEEEAddress:  d.IEEEAddress,
			FriendlyName: d.FriendlyName,
			Type:         d.Type,
			PowerSource:  d.PowerSource,
			DeviceID:     added[d.IEEEAddress],
		}
		entry.CanAdd, entry.Reason = d.CanAdd()
		if d.Definition != nil {
			entry.Model, entry.Vendor, entry.Description = d.Definition.Model, d.Definition.Vendor, d.Definition.Description
		}
		if entry.CanAdd {
			p := Propose(d)
			entry.Kind, entry.Commandable, entry.Fields = p.Kind, !p.ReadOnly, p.Fields
		}
		if entry.DeviceID != "" {
			entry.CanAdd, entry.Reason = false, "already added"
		}
		out = append(out, entry)
	}
	return out
}
