package mqttrelay

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Definition is a device definition as clients write it: topics as a list and
// options as a map, next to the JSON columns they are stored in.
type Definition struct {
	Name          string         `json:"name" required:"true" minLength:"1" description:"Display name, unique across devices."`
	ConnectionID  uuid.UUID      `json:"connection_id" required:"true" description:"MQTT connection the device speaks on."`
	Topics        []string       `json:"topics" description:"Topic filters, wildcards allowed."`
	StateScript   string         `json:"state_script" description:"JavaScript reading one message into the values it carries."`
	Control       string         `json:"control,omitempty" description:"Control kind the device is drawn with."`
	Options       map[string]any `json:"options,omitempty" description:"Settings of that kind: bounds, choices, buttons, features."`
	PayloadScript string         `json:"payload_script,omitempty" description:"JavaScript turning a control change into {topic, payload}."`
	Description   string         `json:"description,omitempty" description:"What the device is."`
	// The availability settings. Empty online and offline payloads take the
	// defaults "online" and "offline".
	AvailabilityTopic   string `json:"availability_topic,omitempty" description:"Topic carrying online and offline payloads; empty for none."`
	AvailabilityOnline  string `json:"availability_online,omitempty" description:"Payload meaning online, default online. A JSON object whose state equals it also matches."`
	AvailabilityOffline string `json:"availability_offline,omitempty" description:"Payload meaning offline, default offline. A JSON object whose state equals it also matches."`
	ExpectEveryS        int    `json:"expect_every_s,omitempty" description:"Offline after this many seconds with no message; 0 is off, otherwise at least 60."`
	OfflineCommands     bool   `json:"offline_commands,omitempty" description:"Send commands while the device is offline."`
	// Delivery settings, for MQTT devices.
	StateQoS         int  `json:"state_qos,omitempty" minimum:"0" maximum:"2" description:"QoS of the state and availability subscriptions: 0 (default), 1, or 2."`
	CommandQoS       int  `json:"command_qos,omitempty" minimum:"0" maximum:"2" description:"QoS commands are published at: 0 (default), 1, or 2."`
	CommandRetain    bool `json:"command_retain,omitempty" description:"Publish commands retained."`
	StateTimeoutMS   int  `json:"state_timeout_ms,omitempty" description:"State script time limit in milliseconds, 50 to 5000; 0 is the default of 250."`
	PayloadTimeoutMS int  `json:"payload_timeout_ms,omitempty" description:"Payload script time limit in milliseconds, 100 to 10000; 0 is the default of 2000."`
	// Transport, EntityKey, and EntityType describe a device reached through
	// an ESPHome, Frigate, or Home Assistant connection rather than MQTT topics
	// and scripts.
	Transport  string `json:"transport,omitempty" enum:"mqtt,esphome,frigate,homeassistant,openhab" description:"How the device is reached: mqtt (default), esphome, frigate, homeassistant, or openhab."`
	EntityKey  string `json:"entity_key,omitempty" description:"Entity the device is, for entity devices: an ESPHome object ID, <camera>/<feature> for Frigate, a Home Assistant entity ID, or an openHAB item name."`
	EntityType string `json:"entity_type,omitempty" description:"Entity type, for entity devices: the ESPHome entity type, the Home Assistant domain, or the openHAB item type."`
}

// ValidateDefinition rejects a definition that could not run. A device with no
// topics would listen to nothing and a device with no decoder would have
// nothing to say about what it heard, so both are refused at the door rather
// than sitting in the list doing nothing.
func ValidateDefinition(def Definition) error {
	if strings.TrimSpace(def.Name) == "" {
		return apperrors.ErrValidation.WithMessage("name is required")
	}
	switch def.Transport {
	case "", TransportMQTT:
	case TransportESPHome, TransportFrigate, TransportHomeAssistant, TransportOpenHAB:
		// An entity device is its entity; the connection does the reading
		// and commanding that topics and scripts do for MQTT.
		if strings.TrimSpace(def.EntityKey) == "" {
			return apperrors.ErrValidation.WithMessage("a " + transportLabel(def.Transport) + " device needs the entity's object ID")
		}
		return nil
	default:
		return apperrors.ErrValidation.WithMessage("transport must be mqtt, esphome, frigate, homeassistant, or openhab")
	}
	if len(def.Topics) == 0 {
		return apperrors.ErrValidation.WithMessage("a device needs at least one topic")
	}
	if strings.TrimSpace(def.StateScript) == "" {
		return apperrors.ErrValidation.WithMessage("a device needs a state script")
	}
	if def.ExpectEveryS < 0 || (def.ExpectEveryS > 0 && def.ExpectEveryS < minExpectEveryS) {
		return apperrors.ErrValidation.WithMessage(fmt.Sprintf(
			"expect_every_s must be 0 or at least %d seconds", minExpectEveryS))
	}
	for name, qos := range map[string]int{"state_qos": def.StateQoS, "command_qos": def.CommandQoS} {
		if qos < 0 || qos > 2 {
			return apperrors.ErrValidation.WithMessage(name + " must be 0, 1, or 2")
		}
	}
	if err := checkLimit("state_timeout_ms", def.StateTimeoutMS, models.MinStateTimeoutMS, models.MaxStateTimeoutMS); err != nil {
		return err
	}
	return checkLimit("payload_timeout_ms", def.PayloadTimeoutMS, models.MinPayloadTimeoutMS, models.MaxPayloadTimeoutMS)
}

// CheckTestTimeout holds the time limit a script test runs under to the range
// a device of that mode may set, so a test cannot run longer than the device
// it stands in for.
func CheckTestTimeout(mode string, ms int) error {
	if mode == "payload" {
		return checkLimit("timeout_ms", ms, models.MinPayloadTimeoutMS, models.MaxPayloadTimeoutMS)
	}
	return checkLimit("timeout_ms", ms, models.MinStateTimeoutMS, models.MaxStateTimeoutMS)
}

// checkLimit holds a script time limit to its range; zero keeps the default.
// An out-of-range limit is refused rather than clamped, so a device never runs
// under a limit its author did not choose.
func checkLimit(name string, ms, min, max int) error {
	if ms == 0 || (ms >= min && ms <= max) {
		return nil
	}
	return apperrors.ErrValidation.WithMessage(fmt.Sprintf("%s must be 0 for the default, or %d to %d", name, min, max))
}

// NormalizeDefinitionOptions checks the kind settings a device stores against
// the catalog, filling declared defaults, so a slider gets its bounds even
// when the definition was written over the API. A device with no kind stores
// its options as given; nothing reads them.
func NormalizeDefinitionOptions(def Definition) (map[string]any, error) {
	kind := strings.TrimSpace(def.Control)
	if kind == "" {
		return def.Options, nil
	}
	// The kind comes from the same catalog a control picks from, so a device
	// naming one that does not exist would draw as nothing on every dashboard
	// it appears on.
	opts, err := controls.NormalizeOptions(kind, def.Options)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	return opts, nil
}

// ApplyDefinition writes a validated definition onto a device row. Options are
// expected to be normalized already.
func ApplyDefinition(d *models.MQTTDevice, def Definition, options map[string]any) {
	d.Name = strings.TrimSpace(def.Name)
	d.ConnectionID = def.ConnectionID
	d.StateScript = def.StateScript
	d.Control = def.Control
	d.PayloadScript = def.PayloadScript
	d.Description = def.Description
	d.SetTopicList(def.Topics)
	d.SetOptionMap(options)
	d.AvailabilityTopic = strings.TrimSpace(def.AvailabilityTopic)
	d.AvailabilityOnline = orDefault(def.AvailabilityOnline, "online")
	d.AvailabilityOffline = orDefault(def.AvailabilityOffline, "offline")
	d.ExpectEveryS = def.ExpectEveryS
	d.OfflineCommands = def.OfflineCommands
	d.StateQoS = def.StateQoS
	d.CommandQoS = def.CommandQoS
	d.CommandRetain = def.CommandRetain
	d.StateTimeoutMS = def.StateTimeoutMS
	d.PayloadTimeoutMS = def.PayloadTimeoutMS
	d.Transport = orDefault(def.Transport, TransportMQTT)
	d.EntityKey = strings.TrimSpace(def.EntityKey)
	d.EntityType = strings.TrimSpace(def.EntityType)
}

// DefinitionOf renders a stored device as the definition that would write it,
// for callers that change one part and save the rest unchanged.
func DefinitionOf(d *models.MQTTDevice) Definition {
	return Definition{
		Name:                d.Name,
		ConnectionID:        d.ConnectionID,
		Topics:              d.TopicList(),
		StateScript:         d.StateScript,
		Control:             d.Control,
		Options:             d.OptionMap(),
		PayloadScript:       d.PayloadScript,
		Description:         d.Description,
		AvailabilityTopic:   d.AvailabilityTopic,
		AvailabilityOnline:  d.AvailabilityOnline,
		AvailabilityOffline: d.AvailabilityOffline,
		ExpectEveryS:        d.ExpectEveryS,
		OfflineCommands:     d.OfflineCommands,
		StateQoS:            d.StateQoS,
		CommandQoS:          d.CommandQoS,
		CommandRetain:       d.CommandRetain,
		StateTimeoutMS:      d.StateTimeoutMS,
		PayloadTimeoutMS:    d.PayloadTimeoutMS,
		Transport:           d.Transport,
		EntityKey:           d.EntityKey,
		EntityType:          d.EntityType,
	}
}

// orDefault returns s trimmed, or def when that leaves nothing.
func orDefault(s, def string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return def
}

// View is one device as clients see it. Topics are a list here rather than
// the JSON column they are stored in.
type View struct {
	ID            uuid.UUID      `json:"id" description:"Unique identifier."`
	Name          string         `json:"name" description:"Display name."`
	ConnectionID  uuid.UUID      `json:"connection_id" description:"MQTT connection the device speaks on."`
	Topics        []string       `json:"topics" description:"Topic filters, wildcards allowed."`
	StateScript   string         `json:"state_script" description:"JavaScript reading one message into the values it carries."`
	Control       string         `json:"control" description:"Control kind the device is drawn with."`
	Options       map[string]any `json:"options" description:"Settings of that kind: bounds, choices, buttons, features."`
	PayloadScript string         `json:"payload_script" description:"JavaScript turning a control change into {topic, payload}."`
	Description   string         `json:"description" description:"What the device is."`
	// Commandable says whether the device takes commands at all: an MQTT
	// device with a payload script, or an entity that is writable. A row draws
	// a reading rather than a switch when it does not, so a viewer who cannot
	// see the script still gets a face that tells the truth.
	Commandable bool `json:"commandable" description:"Whether the device can be commanded: an MQTT device with a payload script, or a writable entity."`
	// State is what the decoder has accumulated so far, absent when the device
	// is not running.
	State map[string]any `json:"state,omitempty" description:"Values decoded so far."`
	// StateError is the decoder's standing failure. A broken decoder leaves a
	// stale value on every dashboard, and this is the only place that can say
	// why.
	StateError string `json:"state_error,omitempty" description:"Why the last message failed to decode, when it did."`
	// Running says whether the device is subscribed at all, which an empty
	// state cannot: a device that heard nothing yet and one whose connection
	// is down both hold no values.
	Running bool `json:"running" description:"Whether the device is subscribed and decoding."`
	// LastMessage is when anything last arrived on the device's topics, ignored
	// messages included, so the panel can say whether the topics are wired
	// right before the decoder has produced a value.
	LastMessage *time.Time `json:"last_message,omitempty" description:"When the last message arrived on the device's topics."`
	// Stale says the state is what was stored before the server started and
	// the device has not reported since.
	Stale bool `json:"stale,omitempty" description:"Whether the state was restored from before a restart and not yet confirmed by the device."`
	// ReportedAt is when the device's state script last merged a report.
	ReportedAt *time.Time `json:"reported_at,omitempty" description:"When the device last reported a value."`

	AvailabilityTopic   string `json:"availability_topic,omitempty" description:"Topic carrying online and offline payloads."`
	AvailabilityOnline  string `json:"availability_online,omitempty" description:"Payload meaning online."`
	AvailabilityOffline string `json:"availability_offline,omitempty" description:"Payload meaning offline."`
	ExpectEveryS        int    `json:"expect_every_s,omitempty" description:"Offline after this many seconds with no message; 0 is off."`
	OfflineCommands     bool   `json:"offline_commands,omitempty" description:"Whether commands are sent while the device is offline."`
	StateQoS            int    `json:"state_qos,omitempty" description:"QoS of the state and availability subscriptions."`
	// StateQoSGranted is the QoS the broker granted when it is lower than
	// asked, absent otherwise.
	StateQoSGranted  *int   `json:"state_qos_granted,omitempty" description:"QoS the broker granted on the state subscriptions, when lower than state_qos."`
	CommandQoS       int    `json:"command_qos,omitempty" description:"QoS commands are published at."`
	CommandRetain    bool   `json:"command_retain,omitempty" description:"Whether commands are published retained."`
	StateTimeoutMS   int    `json:"state_timeout_ms,omitempty" description:"State script time limit in milliseconds; 0 is the default."`
	PayloadTimeoutMS int    `json:"payload_timeout_ms,omitempty" description:"Payload script time limit in milliseconds; 0 is the default."`
	Z2MIEEE          string `json:"z2m_ieee,omitempty" description:"IEEE address of the Zigbee2MQTT device this was added from."`
	Z2MBase          string `json:"z2m_base,omitempty" description:"Zigbee2MQTT base topic this was added from."`
	Missing          bool   `json:"missing,omitempty" description:"Whether the device is absent from its source's device or entity list."`
	// AutoImported and LastReportedAt follow the connection's auto import.
	AutoImported   bool       `json:"auto_imported,omitempty" description:"Whether the connection's auto import created the device."`
	LastReportedAt *time.Time `json:"last_reported_at,omitempty" description:"When the device's source last reported it."`
	DiscoveryKey   string     `json:"discovery_key,omitempty" description:"Home Assistant MQTT discovery identity the device was added from."`
	Transport      string     `json:"transport" description:"How the device is reached: mqtt, esphome, or frigate."`
	EntityKey      string     `json:"entity_key,omitempty" description:"Entity object ID, for esphome and frigate devices: an ESPHome object ID or <camera>/<feature>."`
	EntityType     string     `json:"entity_type,omitempty" description:"Entity type, for esphome and frigate devices."`

	// Availability is online, offline, or unknown, with what decided it and
	// when it last changed. Absent when the device is not running.
	Availability       string     `json:"availability,omitempty" description:"online, offline, or unknown."`
	AvailabilitySource string     `json:"availability_source,omitempty" description:"What decided the availability: connection, topic, interval, or default."`
	AvailabilityAt     *time.Time `json:"availability_at,omitempty" description:"When the availability last changed."`
	// AvailabilityNote is the last payload on the availability topic that was
	// neither the online nor the offline payload.
	AvailabilityNote string `json:"availability_note,omitempty" description:"Last unrecognised availability payload."`
}

// ForViewer reduces a device to what a dashboard row draws. A viewer reached
// this through a dashboard, and a row renders the device's name, its control
// kind and that kind's settings, and its current values. The decoder and
// command scripts are the author's, and the connection and topic filters
// describe the broker rather than the thing on screen.
func (v View) ForViewer() View {
	return View{
		ID:          v.ID,
		Name:        v.Name,
		Description: v.Description,
		Control:     v.Control,
		Options:     v.Options,
		Commandable: v.Commandable,
		State:       v.State,
		StateError:  v.StateError,
		Running:     v.Running,
		LastMessage: v.LastMessage,
		Stale:       v.Stale,
		ReportedAt:  v.ReportedAt,
		// A missing device is unavailable to a viewer too, so the row says so
		// rather than drawing the last value as current.
		Availability:   v.Availability,
		AvailabilityAt: v.AvailabilityAt,
		Missing:        v.Missing,
	}
}

// BuildView renders a device, including its live state when the given runner
// holds it. A nil runner reports the device as not running.
func BuildView(d *models.MQTTDevice, devices *Devices) View {
	out := View{
		ID:            d.ID,
		Name:          d.Name,
		ConnectionID:  d.ConnectionID,
		Topics:        d.TopicList(),
		StateScript:   d.StateScript,
		Control:       d.Control,
		Options:       d.OptionMap(),
		PayloadScript: d.PayloadScript,
		Description:   d.Description,
		Commandable:   Commandable(d),

		AvailabilityTopic:   d.AvailabilityTopic,
		AvailabilityOnline:  d.AvailabilityOnline,
		AvailabilityOffline: d.AvailabilityOffline,
		ExpectEveryS:        d.ExpectEveryS,
		OfflineCommands:     d.OfflineCommands,
		StateQoS:            d.StateQoS,
		CommandQoS:          d.CommandQoS,
		CommandRetain:       d.CommandRetain,
		StateTimeoutMS:      d.StateTimeoutMS,
		PayloadTimeoutMS:    d.PayloadTimeoutMS,
		Z2MIEEE:             d.Z2MIEEE,
		Z2MBase:             d.Z2MBase,
		Missing:             d.Missing,
		AutoImported:        d.AutoImported,
		LastReportedAt:      d.LastReportedAt,
		DiscoveryKey:        d.DiscoveryKey,
		Transport:           orDefault(d.Transport, TransportMQTT),
		EntityKey:           d.EntityKey,
		EntityType:          d.EntityType,
	}
	if devices != nil {
		out.Commandable = devices.Commandable(d)
	}
	if devices != nil && !IsEntityTransport(d.Transport) && d.StateQoS > 0 {
		if granted, ok := devices.GrantedQoS(d); ok && granted < d.StateQoS {
			out.StateQoSGranted = &granted
		}
	}
	if devices != nil {
		if status, running := devices.Status(d.ID); running {
			out.State = status.State
			out.StateError = status.Err
			out.Running = true
			if !status.LastMessage.IsZero() {
				at := status.LastMessage
				out.LastMessage = &at
			}
			out.Stale = status.Stale
			if !status.ReportedAt.IsZero() {
				at := status.ReportedAt
				out.ReportedAt = &at
			}
			out.Availability = status.Availability
			out.AvailabilitySource = status.AvailabilitySource
			out.AvailabilityNote = status.AvailabilityNote
			if !status.AvailabilityAt.IsZero() {
				at := status.AvailabilityAt
				out.AvailabilityAt = &at
			}
		}
	}
	return out
}
