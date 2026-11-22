package integrations

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// MinRemoveAfter is the shortest "Remove after" a connection accepts. The
// importer sweeps once a minute, and a device that reports hourly must not be
// removed between two reports.
const MinRemoveAfter = time.Hour

// Auto import config keys shared by every device-capable connection kind.
const (
	AutoImportKey      = "auto_import"
	RemoveAfterKey     = "remove_after"
	DiscoveryPrefixKey = "discovery_prefix"
	Z2MBaseKey         = "zigbee2mqtt_base"
	ImportAreasKey     = "import_areas"
	ImportDomainsKey   = "import_domains"
	ImportLocationsKey = "import_locations"
	ImportTypesKey     = "import_types"
)

// Default discovery topics for MQTT connections.
const (
	DefaultDiscoveryPrefix = "homeassistant"
	DefaultZ2MBase         = "zigbee2mqtt"
)

// AutoImportFields returns the auto import settings a device-capable kind
// appends to its ConfigSpec, followed by the kind's own filter fields, which
// show only while auto import is on.
func AutoImportFields(extra ...flow.FieldSpec) []flow.FieldSpec {
	on := &flow.ShowIf{Field: AutoImportKey, Equals: true}
	fields := []flow.FieldSpec{
		{
			ID: AutoImportKey, Label: "Auto import devices", Type: flow.FieldBoolean, Default: false,
			Help: "Add every device this connection reports as a device, including ones that appear later.",
		},
		{
			ID: RemoveAfterKey, Label: "Remove after", Type: flow.FieldDuration, Placeholder: "7d", ShowIf: on,
			Help: "Remove an auto-imported device its source has not reported for this long, such as 12h or 7d. Empty never removes. At least 1h.",
		},
	}
	for _, f := range extra {
		f.ShowIf = on
		fields = append(fields, f)
	}
	return fields
}

// AutoImportSettings are a connection's auto import settings.
type AutoImportSettings struct {
	Enabled     bool
	RemoveAfter time.Duration
	// DiscoveryPrefix and Z2MBase apply to MQTT connections; a base topic set
	// to off leaves Z2MBase empty, which turns Zigbee2MQTT import off.
	DiscoveryPrefix string
	Z2MBase         string
	// Places and Types narrow entity imports: Home Assistant areas and
	// domains, openHAB locations and item types. Lower case.
	Places []string
	Types  []string
}

// ReadAutoImport reads auto import settings from a connection config.
func ReadAutoImport(raw []byte) (AutoImportSettings, error) {
	var cfg map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return AutoImportSettings{}, fmt.Errorf("reading connection config: %w", err)
		}
	}
	return AutoImportFromMap(cfg)
}

// AutoImportFromMap reads auto import settings from a decoded config.
func AutoImportFromMap(cfg map[string]any) (AutoImportSettings, error) {
	s := AutoImportSettings{DiscoveryPrefix: DefaultDiscoveryPrefix, Z2MBase: DefaultZ2MBase}
	switch v := cfg[AutoImportKey].(type) {
	case bool:
		s.Enabled = v
	case string:
		s.Enabled = v == "true"
	}
	if raw, _ := cfg[RemoveAfterKey].(string); strings.TrimSpace(raw) != "" {
		d, err := ParseLongDuration(raw)
		if err != nil {
			return s, fmt.Errorf("remove_after: %w", err)
		}
		if d < MinRemoveAfter {
			return s, fmt.Errorf("remove_after must be at least 1h")
		}
		s.RemoveAfter = d
	}
	if v, ok := cfg[DiscoveryPrefixKey].(string); ok && strings.Trim(strings.TrimSpace(v), "/") != "" {
		s.DiscoveryPrefix = strings.Trim(strings.TrimSpace(v), "/")
	}
	if v, ok := cfg[Z2MBaseKey].(string); ok && strings.TrimSpace(v) != "" {
		s.Z2MBase = strings.Trim(strings.TrimSpace(v), "/")
		if strings.EqualFold(s.Z2MBase, "off") {
			s.Z2MBase = ""
		}
	}
	s.Places = append(splitList(cfg[ImportAreasKey]), splitList(cfg[ImportLocationsKey])...)
	s.Types = append(splitList(cfg[ImportDomainsKey]), splitList(cfg[ImportTypesKey])...)
	return s, nil
}

// ValidateAutoImport rejects auto import settings that cannot be applied.
func ValidateAutoImport(cfg map[string]any) error {
	_, err := AutoImportFromMap(cfg)
	return err
}

// ParseLongDuration reads a duration that may count days, such as 7d or
// 1d12h, as well as anything time.ParseDuration takes.
func ParseLongDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	var days time.Duration
	if i := strings.Index(raw, "d"); i > 0 {
		n, err := strconv.Atoi(raw[:i])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a duration such as 12h or 7d", raw)
		}
		days = time.Duration(n) * 24 * time.Hour
		raw = raw[i+1:]
		if raw == "" {
			return days, nil
		}
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a duration such as 12h or 7d", raw)
	}
	return days + d, nil
}

// FormatLongDuration renders a duration the way ParseLongDuration reads it,
// such as 7d, 1d12h, or 90m.
func FormatLongDuration(d time.Duration) string {
	var b strings.Builder
	if days := d / (24 * time.Hour); days > 0 {
		b.WriteString(strconv.Itoa(int(days)) + "d")
		d -= days * 24 * time.Hour
	}
	if hours := d / time.Hour; hours > 0 {
		b.WriteString(strconv.Itoa(int(hours)) + "h")
		d -= hours * time.Hour
	}
	if minutes := d / time.Minute; minutes > 0 {
		b.WriteString(strconv.Itoa(int(minutes)) + "m")
		d -= minutes * time.Minute
	}
	if d > 0 || b.Len() == 0 {
		b.WriteString(d.String())
	}
	return b.String()
}

// splitList reads a comma-separated filter into lower-case entries.
func splitList(v any) []string {
	s, _ := v.(string)
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}
