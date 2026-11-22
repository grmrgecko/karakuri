package flow

import (
	"encoding/json"
	"strconv"
	"strings"
)

// A node's payload type usually depends on how it is configured: a JSON node
// decoding text emits an object, the same node encoding emits a string, and a
// file read as base64 emits binary. Expressing that as a rule on the port,
// rather than as code per node type, is what keeps the answer in one place —
// the editor derives ports from config the same way the server does, and a
// per-node switch would have to exist twice, in Go and in TypeScript, and stay
// in step forever.

// PortTypeCase is one branch of a port's type rule: the config the node must
// have for it to apply, and the payload type the port then carries.
type PortTypeCase struct {
	// Match maps a config field to the values that satisfy it. A field is
	// satisfied when its value equals any one of the alternatives, and a field
	// the config does not set reads as the empty string, so listing "" is how
	// a case covers an unconfigured node. The wildcard "*" is satisfied by any
	// non-empty value, for cases that turn on a field being set at all. Every
	// entry must match.
	Match map[string][]string `json:"match,omitempty" description:"Config field to the values that satisfy it; all entries must match. \"*\" matches any non-empty value."`
	Type  string              `json:"type" description:"Payload type carried when this case applies; empty retains the declared type and input propagation."`
	// PayloadPath overrides the rule's PayloadPath while this case applies. A
	// node whose modes read different fields scopes the path to the mode it
	// belongs to, so a stale path left behind by a mode switch cannot color a
	// port the current mode never touches. Empty defers to the rule, so a rule
	// pairing it with per-case paths leaves its own PayloadPath unset.
	PayloadPath []string `json:"payload_path,omitempty" description:"Overrides the rule's payload path fields while this case applies."`
}

// PortTypeRule derives one port's payload type from the placed node's config.
// Cases are tried in order, so a narrow case is written before the broad one
// it would otherwise be swallowed by.
type PortTypeRule struct {
	Cases []PortTypeCase `json:"cases,omitempty" description:"Ordered cases; the first whose config matches decides the type."`
	// Default is the type used when no case matches. Empty keeps the type the
	// port declared statically.
	Default string `json:"default,omitempty" description:"Type used when no case matches."`
	// PayloadPath names the config fields holding the dot path this port's
	// value is read from or written to, in precedence order: the first field
	// with a value wins, and a node that sets none of them is reading or
	// writing the payload itself.
	//
	// It exists because a port type describes the payload and nothing else. A
	// JSON node that writes its result to meta.parsed leaves the payload
	// exactly as it arrived, so the derived type would be a lie; one that
	// writes to payload.body says only that the payload is an object.
	PayloadPath []string `json:"payload_path,omitempty" description:"Config fields holding the dot path the value is read from or written to, in precedence order."`
}

// Where a dot path sits relative to the payload, which is all a port type can
// describe.
const (
	// pathPayload is the payload itself, so the derived type applies as-is.
	pathPayload = iota
	// pathInPayload is a property inside the payload, which says nothing about
	// the value at that path but does mean the payload is an object.
	pathInPayload
	// pathOffPayload is the topic or metadata, which leaves the payload
	// untouched and therefore undeclared.
	pathOffPayload
)

// ResolvePortType derives a port's payload type from a node's raw config.
// A port with no rule keeps the type it declared, which is every port whose
// type does not depend on how the node is set up.
func ResolvePortType(spec PortSpec, raw json.RawMessage) string {
	resolved, _ := resolvePortRule(spec, raw)
	return resolved
}

// resolvePortRule derives the type and reports whether a matched case asserted
// "any" outright. The distinction matters to TypeFrom: a rule falling through
// to its default is deferring, and propagation may still fill the type in from
// upstream, but a case that names "any" states the payload is config-defined
// and unknowable, which propagation must not paper over. A payload path that
// lands off the payload never asserts — the payload then passes through
// untouched, which is exactly what TypeFrom exists to say.
func resolvePortRule(spec PortSpec, raw json.RawMessage) (string, bool) {
	if spec.TypeRule == nil {
		return spec.Type, false
	}
	rule := spec.TypeRule
	cfg := decodeConfigFields(raw)

	// Pick the type from the first case whose config matches. The matched case
	// may scope the payload path to itself; no match leaves the rule's own.
	resolved := rule.Default
	if resolved == "" {
		resolved = spec.Type
	}
	asserted := false
	pathFields := rule.PayloadPath
	for _, c := range rule.Cases {
		if matchesConfig(c.Match, cfg) {
			resolved = c.Type
			if resolved == "" {
				resolved = spec.Type
			}
			asserted = c.Type == PortAny
			if len(c.PayloadPath) > 0 {
				pathFields = c.PayloadPath
			}
			break
		}
	}

	// Narrow it to what the port actually carries, which is the payload.
	switch payloadPathScope(pathFields, cfg) {
	case pathInPayload:
		return PortObject, false
	case pathOffPayload:
		return PortAny, false
	}
	return resolved, asserted && resolved == PortAny
}

// matchesConfig reports whether every entry of a case's match set is satisfied
// by the config. An empty match set matches anything, which is how a rule
// writes its catch-all case.
func matchesConfig(match map[string][]string, cfg map[string]any) bool {
	for field, allowed := range match {
		value := configString(cfg[field])
		found := false
		for _, want := range allowed {
			if value == want || (want == "*" && value != "") {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// payloadPathScope resolves the configured dot path and classifies it. Fields
// are consulted in order and the first with a value wins, so a parser whose
// target is empty falls back to the property it converted.
func payloadPathScope(fields []string, cfg map[string]any) int {
	if len(fields) == 0 {
		return pathPayload
	}
	path := ""
	for _, field := range fields {
		if v := strings.TrimSpace(configString(cfg[field])); v != "" {
			path = v
			break
		}
	}
	// The nodes that read or write a property all default to the payload, so
	// an unset path is the payload rather than an unknown.
	if path == "" || path == "payload" {
		return pathPayload
	}
	// Roots other than the payload leave it alone; everything else, including
	// a bare path, is a property inside it.
	root, _, _ := strings.Cut(path, ".")
	switch root {
	case "topic", "meta":
		return pathOffPayload
	}
	return pathInPayload
}

// decodeConfigFields reads a node's config as flat fields. Only top-level
// values are consulted by a rule, so a config that will not decode yields no
// fields and every rule falls back to its default.
func decodeConfigFields(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	return cfg
}

// configString renders a config value the way a rule compares it. Select
// fields are already strings; booleans and numbers are written the way they
// read in the editor so a rule can name them literally.
func configString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}

// resolvePortTypes applies each port's type rule against the node's config.
// The static slice belongs to the node type and is shared by every placed node
// of that type, so it is only copied once a rule actually changes something.
// A case asserting "any" also drops the port's TypeFrom, so type propagation
// cannot dress a config-defined payload in the upstream's type.
func resolvePortTypes(ports []PortSpec, raw json.RawMessage) []PortSpec {
	var out []PortSpec
	for i, p := range ports {
		resolved, asserted := resolvePortRule(p, raw)
		clearFrom := asserted && p.TypeFrom != ""
		if resolved == p.Type && !clearFrom {
			continue
		}
		if out == nil {
			out = append([]PortSpec{}, ports...)
		}
		out[i].Type = resolved
		if clearFrom {
			out[i].TypeFrom = ""
		}
	}
	if out == nil {
		return ports
	}
	return out
}
