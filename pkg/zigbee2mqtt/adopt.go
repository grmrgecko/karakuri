package zigbee2mqtt

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
)

// AddResult is what adding a set of Zigbee2MQTT devices did.
type AddResult struct {
	Added  []mqttrelay.View `json:"added" description:"Devices created."`
	Failed []AddFailure     `json:"failed" description:"Devices that could not be added, with why."`
}

// AddFailure is one device that could not be added.
type AddFailure struct {
	IEEEAddress string `json:"ieee_address" description:"IEEE address."`
	Error       string `json:"error" description:"Why it was not added."`
}

// DefinitionFor builds the karakuri device a Zigbee2MQTT device becomes. Its
// state topic and availability topic use the friendly name, as Zigbee2MQTT
// publishes them; its payload script uses the IEEE address.
func DefinitionFor(d Device, connID uuid.UUID, base string) (mqttrelay.Definition, mqttrelay.Origin) {
	base = NormalizeBase(base)
	p := Propose(d)
	description := ""
	if d.Definition != nil {
		description = strings.TrimSpace(d.Definition.Vendor + " " + d.Definition.Model)
		if d.Definition.Description != "" {
			description += ": " + d.Definition.Description
		}
	}
	def := mqttrelay.Definition{
		Name:                d.FriendlyName,
		ConnectionID:        connID,
		Topics:              []string{base + "/" + d.FriendlyName},
		StateScript:         StateScript(p),
		Control:             p.Kind,
		Options:             p.Options,
		PayloadScript:       PayloadScript(p, base, d.IEEEAddress),
		Description:         description,
		AvailabilityTopic:   base + "/" + d.FriendlyName + "/availability",
		AvailabilityOnline:  "online",
		AvailabilityOffline: "offline",
	}
	return def, mqttrelay.Origin{IEEE: d.IEEEAddress, Base: base}
}

// Add creates karakuri devices for the listed IEEE addresses from a fresh
// read of the device list. Each device succeeds or fails on its own.
func Add(ctx context.Context, sub Subscriber, connID uuid.UUID, base string, ieees []string) (AddResult, error) {
	base = NormalizeBase(base)
	devices, err := ReadDevices(ctx, sub, base)
	if err != nil {
		return AddResult{}, err
	}
	return AddFrom(devices, connID, base, ieees, false)
}

// AddFrom creates karakuri devices for the listed IEEE addresses from a
// device list already read, marked auto imported when auto is set. Each
// device succeeds or fails on its own.
func AddFrom(devices []Device, connID uuid.UUID, base string, ieees []string, auto bool) (AddResult, error) {
	base = NormalizeBase(base)
	byIEEE := map[string]Device{}
	for _, d := range devices {
		byIEEE[d.IEEEAddress] = d
	}
	existing, err := models.ListMQTTDevices()
	if err != nil {
		return AddResult{}, apperrors.ErrInternal.WithMessage(err.Error())
	}
	names := map[string]bool{}
	for _, m := range existing {
		names[strings.ToLower(m.Name)] = true
	}

	result := AddResult{Added: []mqttrelay.View{}, Failed: []AddFailure{}}
	for _, ieee := range ieees {
		d, ok := byIEEE[ieee]
		if !ok {
			result.Failed = append(result.Failed, AddFailure{IEEEAddress: ieee, Error: "not in Zigbee2MQTT's device list"})
			continue
		}
		if can, why := d.CanAdd(); !can {
			result.Failed = append(result.Failed, AddFailure{IEEEAddress: ieee, Error: why})
			continue
		}
		def, origin := DefinitionFor(d, connID, base)
		origin.Auto = auto
		def.Name = uniqueName(def.Name, names)
		saved, err := mqttrelay.SaveDefinition(uuid.Nil, def, origin)
		if err != nil {
			result.Failed = append(result.Failed, AddFailure{IEEEAddress: ieee, Error: err.Error()})
			continue
		}
		names[strings.ToLower(saved.Name)] = true
		result.Added = append(result.Added, mqttrelay.BuildView(saved, mqttrelay.D))
	}
	return result, nil
}

// Regenerate rebuilds a device's kind, options, scripts, and topics from the
// current device list, keeping its name, description, and availability
// settings other than the topic.
func Regenerate(ctx context.Context, sub Subscriber, id uuid.UUID) (*models.MQTTDevice, error) {
	m, err := models.FindMQTTDeviceByID(id)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("device not found")
	}
	if m.Z2MIEEE == "" {
		return nil, apperrors.ErrValidation.WithMessage("device \"" + m.Name + "\" was not added from Zigbee2MQTT")
	}
	devices, err := ReadDevices(ctx, sub, m.Z2MBase)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if d.IEEEAddress != m.Z2MIEEE {
			continue
		}
		if can, why := d.CanAdd(); !can {
			return nil, apperrors.ErrValidation.WithMessage(fmt.Sprintf("device %q cannot be regenerated: %s", m.Name, why))
		}
		fresh, _ := DefinitionFor(d, m.ConnectionID, m.Z2MBase)
		def := mqttrelay.DefinitionOf(m)
		def.Topics, def.AvailabilityTopic = fresh.Topics, fresh.AvailabilityTopic
		def.Control, def.Options = fresh.Control, fresh.Options
		def.StateScript, def.PayloadScript = fresh.StateScript, fresh.PayloadScript
		return mqttrelay.SaveDefinition(m.ID, def, mqttrelay.Origin{})
	}
	return nil, apperrors.ErrNotFound.WithMessage("device \"" + m.Name + "\" is not in Zigbee2MQTT's device list")
}

// uniqueName returns name, or name with the lowest free numeric suffix.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[strings.ToLower(name)] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", name, i)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}
