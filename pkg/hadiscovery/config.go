// Package hadiscovery reads Home Assistant MQTT discovery configs and turns
// each one into a karakuri MQTT device definition with generated state and
// payload scripts, the way Zigbee2MQTT discovery does for its device list.
package hadiscovery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Config is one discovery config for one component, with abbreviations
// expanded and the base topic applied.
type Config struct {
	// Component is the platform: switch, light, sensor, and so on.
	Component string
	// Key identifies the config: <component>/<unique_id>, or the config topic
	// when the config has no unique ID.
	Key string
	// Topic is the topic the config arrived on.
	Topic string
	// Removed is set for an empty payload, which withdraws the config.
	Removed bool
	// Fields are the config's expanded keys.
	Fields map[string]any
	// Device is the expanded device block, empty when the config has none.
	Device map[string]any
}

// Parse reads one message from under the discovery prefix. It returns no
// configs for a topic that is not a config topic, one per component for a
// device-based config, and a single removed config for an empty payload.
func Parse(prefix, topic string, payload []byte) ([]Config, error) {
	rest, ok := strings.CutPrefix(topic, strings.Trim(prefix, "/")+"/")
	if !ok {
		return nil, nil
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || len(parts) > 4 || parts[len(parts)-1] != "config" {
		return nil, nil
	}
	component := parts[0]
	objectID := parts[len(parts)-2]

	text := strings.TrimSpace(string(payload))
	if text == "" {
		// An empty payload withdraws every config published on the topic.
		// Without the payload the unique IDs are unknown, so the topic is the
		// identity the caller matches on.
		return []Config{{Component: component, Key: topic, Topic: topic, Removed: true}}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("discovery config on %s is not a JSON object: %w", topic, err)
	}
	fields := expand(raw)

	if component == "device" {
		return parseDeviceConfig(topic, objectID, fields), nil
	}
	return []Config{newConfig(component, topic, fields)}, nil
}

// parseDeviceConfig splits a device-based config into one config per
// component. Shared keys (device, origin, availability, state topic, base
// topic, QoS) apply to every component unless the component sets its own.
func parseDeviceConfig(topic, objectID string, fields map[string]any) []Config {
	comps, _ := fields["components"].(map[string]any)
	var out []Config
	for id, rawComp := range comps {
		comp, ok := rawComp.(map[string]any)
		if !ok {
			continue
		}
		merged := map[string]any{}
		for k, v := range fields {
			if k != "components" {
				merged[k] = v
			}
		}
		for k, v := range expand(comp) {
			merged[k] = v
		}
		platform, _ := merged["platform"].(string)
		if platform == "" {
			continue
		}
		delete(merged, "platform")
		if _, ok := merged["object_id"]; !ok {
			merged["object_id"] = objectID + "_" + id
		}
		cfg := newConfig(platform, topic, merged)
		if _, has := merged["unique_id"]; !has {
			cfg.Key = topic + "#" + id
		}
		out = append(out, cfg)
	}
	return out
}

// newConfig builds a config from expanded fields, applying the base topic.
func newConfig(component, topic string, fields map[string]any) Config {
	applyBase(fields)
	cfg := Config{Component: component, Topic: topic, Fields: fields}
	if dev, ok := fields["device"].(map[string]any); ok {
		cfg.Device = dev
	}
	if uid := cfg.String("unique_id", ""); uid != "" {
		cfg.Key = component + "/" + uid
	} else {
		cfg.Key = topic
	}
	return cfg
}

// applyBase replaces a leading or trailing ~ in every topic value with the
// base topic, including inside availability entries.
func applyBase(fields map[string]any) {
	base, _ := fields["~"].(string)
	if base == "" {
		return
	}
	sub := func(v string) string {
		if strings.HasPrefix(v, "~") {
			return base + v[1:]
		}
		if strings.HasSuffix(v, "~") {
			return v[:len(v)-1] + base
		}
		return v
	}
	for k, v := range fields {
		if s, ok := v.(string); ok && (k == "topic" || strings.HasSuffix(k, "_topic")) {
			fields[k] = sub(s)
		}
	}
	if list, ok := fields["availability"].([]any); ok {
		for _, entry := range list {
			if m, ok := entry.(map[string]any); ok {
				if t, ok := m["topic"].(string); ok {
					m["topic"] = sub(t)
				}
			}
		}
	}
}

// expand replaces abbreviated keys with their full names, inside the device,
// origin, and availability blocks too.
func expand(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		full := k
		if long, ok := abbreviations[k]; ok {
			full = long
		}
		switch full {
		case "device":
			if m, ok := v.(map[string]any); ok {
				v = expandWith(m, deviceAbbreviations)
			}
		case "origin":
			if m, ok := v.(map[string]any); ok {
				v = expandWith(m, deviceAbbreviations)
			}
		case "availability":
			if list, ok := v.([]any); ok {
				expanded := make([]any, 0, len(list))
				for _, entry := range list {
					if m, ok := entry.(map[string]any); ok {
						expanded = append(expanded, expandWith(m, abbreviations))
					} else {
						expanded = append(expanded, entry)
					}
				}
				v = expanded
			}
		}
		out[full] = v
	}
	return out
}

// expandWith replaces keys from one abbreviation table.
func expandWith(raw map[string]any, table map[string]string) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if long, ok := table[k]; ok {
			k = long
		}
		out[k] = v
	}
	return out
}

// String reads a string field, or def when it is absent or not a string.
// Numbers and booleans are rendered, since payloads such as payload_on may be
// given as either.
func (c Config) String(key, def string) string {
	switch v := c.Fields[key].(type) {
	case string:
		return v
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", v), "0"), ".")
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return def
}

// Has reports whether a field is present and not null.
func (c Config) Has(key string) bool {
	v, ok := c.Fields[key]
	return ok && v != nil
}

// Number reads a numeric field, or def.
func (c Config) Number(key string, def float64) float64 {
	switch v := c.Fields[key].(type) {
	case float64:
		return v
	case string:
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return def
}

// Bool reads a boolean field, or def.
func (c Config) Bool(key string, def bool) bool {
	if v, ok := c.Fields[key].(bool); ok {
		return v
	}
	return def
}

// Strings reads a list of strings, or def.
func (c Config) Strings(key string, def []string) []string {
	list, ok := c.Fields[key].([]any)
	if !ok {
		return def
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		switch s := v.(type) {
		case string:
			out = append(out, s)
		case float64:
			out = append(out, fmt.Sprint(s))
		}
	}
	return out
}

// DeviceName is the device block's name, empty when there is none.
func (c Config) DeviceName() string {
	name, _ := c.Device["name"].(string)
	return strings.TrimSpace(name)
}

// DeviceIdentifiers lists the device block's identifiers.
func (c Config) DeviceIdentifiers() []string {
	var out []string
	switch v := c.Device["identifiers"].(type) {
	case string:
		out = append(out, v)
	case []any:
		for _, id := range v {
			if s, ok := id.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// FromZigbee2MQTT reports whether Zigbee2MQTT published the config for one of
// its devices, which its own bridge list already describes.
func (c Config) FromZigbee2MQTT() bool {
	for _, id := range c.DeviceIdentifiers() {
		if strings.HasPrefix(id, "zigbee2mqtt_") {
			return true
		}
	}
	return false
}

// Name is the device name karakuri gives the config: the device and entity
// names together, the device name alone when the entity has none, and the
// object ID when neither is set.
func (c Config) Name() string {
	device := c.DeviceName()
	entity := ""
	if v, ok := c.Fields["name"].(string); ok {
		entity = strings.TrimSpace(v)
	}
	switch {
	case device != "" && entity != "":
		if strings.HasPrefix(strings.ToLower(entity), strings.ToLower(device)) {
			return entity
		}
		return device + " " + entity
	case entity != "":
		return entity
	case device != "":
		return device
	}
	if id := c.String("object_id", ""); id != "" {
		return id
	}
	return c.Key
}

// Description says where the config came from.
func (c Config) Description() string {
	parts := []string{"MQTT discovery " + strings.ReplaceAll(c.Component, "_", " ")}
	maker, _ := c.Device["manufacturer"].(string)
	model, _ := c.Device["model"].(string)
	if what := strings.TrimSpace(maker + " " + model); what != "" {
		parts = append(parts, "from "+what)
	}
	return strings.Join(parts, " ")
}
