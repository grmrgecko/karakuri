package flow

import (
	"encoding/json"
	"strings"
)

// paramPortPrefix namespaces parameter ports so they can never collide with a
// node's own input port IDs, which are chosen freely by each node type.
const paramPortPrefix = "param:"

// ParamPortID returns the input port ID that supplies a config field.
func ParamPortID(fieldID string) string { return paramPortPrefix + fieldID }

// ParamFieldID returns the config field a parameter port supplies, and whether
// the port is one at all.
func ParamFieldID(portID string) (string, bool) {
	if !strings.HasPrefix(portID, paramPortPrefix) {
		return "", false
	}
	return strings.TrimPrefix(portID, paramPortPrefix), true
}

// FieldPortType maps a config field's editor type to the port type that can
// supply it. A field with no entry cannot be driven by a wire: rule lists are
// structured editors with no single value, and a connection is a reference to
// server configuration rather than data flowing through the graph.
func FieldPortType(fieldType string) (string, bool) {
	switch fieldType {
	case FieldString, FieldText, FieldMarkdown, FieldExpression, FieldDuration,
		FieldCron, FieldTimezone, FieldEntity, FieldHATarget, FieldSelect, FieldScript:
		return PortString, true
	case FieldNumber:
		return PortNumber, true
	case FieldBoolean:
		return PortBool, true
	case FieldJSON:
		return PortObject, true
	default:
		return "", false
	}
}

// FieldBindable reports whether a config field can be exposed as a value port.
func FieldBindable(fieldType string) bool {
	_, ok := FieldPortType(fieldType)
	return ok
}

// ParamPorts derives the value input ports a placed node exposes: every port
// field of the node type first, then the config fields the node has opted in.
// Opted-in fields that no longer exist on the node type, or that were never
// bindable, are skipped rather than producing a port nothing can satisfy.
//
// A port field with a ShowIf produces its socket only while the condition
// holds against the node's config — the guardrail's model input exists only
// while a model-scored check is picked. Because the port is absent rather
// than hidden, a required one is demanded by deploy exactly when it is shown,
// and an edge left behind by a config change fails validation instead of
// feeding a field nothing reads. Opted-in fields are not gated the same way:
// their sockets came from an author's explicit choice, and yanking one over a
// mode switch would strand wiring that predates the mode.
func ParamPorts(spec []FieldSpec, params []string, config json.RawMessage) []PortSpec {
	var values map[string]any
	if len(config) > 0 {
		_ = json.Unmarshal(config, &values)
	}
	var ports []PortSpec
	byID := make(map[string]FieldSpec, len(spec))
	for _, f := range spec {
		byID[f.ID] = f
		if f.Port == "" || !FieldShown(spec, f, values) {
			continue
		}
		label := f.Label
		if label == "" {
			label = f.ID
		}
		ports = append(ports, PortSpec{
			ID:    ParamPortID(f.ID),
			Label: label,
			Type:  f.Port,
			Kind:  PortKindParam,
			Multi: f.Multi,
			// A parameter never gates readiness. It is pulled when the node
			// runs, so waiting for it on the message path would deadlock a node
			// whose only other input is its trigger.
			Optional: true,
		})
	}
	for _, fieldID := range params {
		field, ok := byID[fieldID]
		if !ok || field.Port != "" || field.NoParam {
			continue
		}
		portType, ok := FieldPortType(field.Type)
		if !ok {
			continue
		}
		label := field.Label
		if label == "" {
			label = fieldID
		}
		ports = append(ports, PortSpec{
			ID:       ParamPortID(fieldID),
			Label:    label,
			Type:     portType,
			Kind:     PortKindParam,
			Optional: true,
		})
	}
	return ports
}

// validateNodeParams reports parameter declarations that cannot work: a field
// the node type does not have, or one whose editor has no single value to wire.
func validateNodeParams(p *problems, key string, meta NodeMeta, n *Node) {
	if len(n.Params) == 0 {
		return
	}
	byID := make(map[string]FieldSpec, len(meta.ConfigSpec))
	for _, f := range meta.ConfigSpec {
		byID[f.ID] = f
	}
	seen := make(map[string]bool, len(n.Params))
	for _, fieldID := range n.Params {
		if seen[fieldID] {
			p.add(key, "config field %q is exposed as a parameter more than once", fieldID)
			continue
		}
		seen[fieldID] = true
		field, ok := byID[fieldID]
		if !ok {
			p.add(key, "node type %q has no config field %q to expose as a parameter", n.Type, fieldID)
			continue
		}
		if field.Port != "" {
			p.add(key, "config field %q is a permanent input port and cannot be toggled", fieldID)
			continue
		}
		if field.NoParam {
			p.add(key, "config field %q cannot be exposed as a parameter", fieldID)
			continue
		}
		if !FieldBindable(field.Type) {
			p.add(key, "config field %q cannot be supplied by a wire: %s fields have no single value",
				fieldID, field.Type)
		}
	}
}
