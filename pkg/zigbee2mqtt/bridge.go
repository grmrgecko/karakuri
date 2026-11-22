// Package zigbee2mqtt reads the device list Zigbee2MQTT publishes and turns
// its devices into ordinary MQTT devices: a control kind, its options, and
// state and payload scripts generated from what each device exposes.
package zigbee2mqtt

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultBase is Zigbee2MQTT's default base topic.
const DefaultBase = "zigbee2mqtt"

// Device is one entry of <base>/bridge/devices. The coordinator has no
// definition, and neither has a device that is unsupported or still being
// interviewed.
type Device struct {
	IEEEAddress        string      `json:"ieee_address"`
	FriendlyName       string      `json:"friendly_name"`
	Type               string      `json:"type"`
	PowerSource        string      `json:"power_source"`
	Supported          bool        `json:"supported"`
	Disabled           bool        `json:"disabled"`
	InterviewCompleted bool        `json:"interview_completed"`
	Interviewing       bool        `json:"interviewing"`
	Definition         *Definition `json:"definition"`
}

// Definition is what Zigbee2MQTT knows about a device's model.
type Definition struct {
	Model       string   `json:"model"`
	Vendor      string   `json:"vendor"`
	Description string   `json:"description"`
	Exposes     []Expose `json:"exposes"`
}

// Expose is one capability of a device. Specific types (light, switch,
// cover, lock, fan, climate) group their parts under Features; generic types
// (binary, numeric, enum, text, composite) name one property.
type Expose struct {
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Label       string     `json:"label"`
	Property    string     `json:"property"`
	Endpoint    string     `json:"endpoint"`
	Category    string     `json:"category"`
	Access      int        `json:"access"`
	Unit        string     `json:"unit"`
	Description string     `json:"description"`
	Values      enumValues `json:"values"`
	ValueMin    *float64   `json:"value_min"`
	ValueMax    *float64   `json:"value_max"`
	ValueStep   *float64   `json:"value_step"`
	// ValueOn and ValueOff are what a binary reports: "ON" for a switch,
	// false for a contact sensor that is open.
	ValueOn  any      `json:"value_on"`
	ValueOff any      `json:"value_off"`
	Features []Expose `json:"features"`
}

// enumValues are an enum expose's values as text. Zigbee2MQTT lists strings
// for most enums and numbers for some, and a kind's choices are text either
// way.
type enumValues []string

// UnmarshalJSON reads a list of strings and numbers, keeping each number as
// its JSON text. Values of any other type are skipped.
func (v *enumValues) UnmarshalJSON(raw []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	out := make(enumValues, 0, len(items))
	for _, item := range items {
		var s string
		if json.Unmarshal(item, &s) == nil {
			out = append(out, s)
			continue
		}
		var n json.Number
		if json.Unmarshal(item, &n) == nil && n != "" {
			out = append(out, n.String())
		}
	}
	*v = out
	return nil
}

// Access bits Zigbee2MQTT sets on an expose.
const (
	accessState = 1 // published in the device's state messages
	accessSet   = 2 // settable through <base>/<device>/set
)

// ParseDevices reads a <base>/bridge/devices payload.
func ParseDevices(payload []byte) ([]Device, error) {
	var devices []Device
	if err := json.Unmarshal(payload, &devices); err != nil {
		return nil, fmt.Errorf("reading the Zigbee2MQTT device list: %w", err)
	}
	return devices, nil
}

// IsCoordinator reports whether d is the Zigbee coordinator, which is the
// bridge itself rather than a device.
func (d Device) IsCoordinator() bool {
	return strings.EqualFold(d.Type, "Coordinator")
}

// CanAdd reports whether a karakuri device can be made from d, and why not.
func (d Device) CanAdd() (bool, string) {
	switch {
	case d.IsCoordinator():
		return false, "the coordinator is the bridge itself"
	case d.Disabled:
		return false, "disabled in Zigbee2MQTT"
	case d.Interviewing || !d.InterviewCompleted:
		return false, "still being interviewed"
	case !d.Supported || d.Definition == nil:
		return false, "not supported by Zigbee2MQTT"
	}
	return true, ""
}
