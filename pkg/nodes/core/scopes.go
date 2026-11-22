package core

import (
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// This file holds what nodes share when they read or write an author-facing
// variable. There are four scopes and the message is one of them, so the
// helpers take the message alongside the scope: the engine owns the run, flow,
// and global stores, but only the node holds its message.
//
// The default everywhere is msg + payload, since the payload is what most
// nodes set after processing.

// scopeField is the shared "which scope" config field.
func scopeField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:      "scope",
		Label:   "Scope",
		Type:    flow.FieldSelect,
		Default: engine.ScopeMsg,
		Options: []engine.Option{
			{Value: engine.ScopeMsg, Label: "Message"},
			{Value: engine.ScopeFlow, Label: "Flow"},
			{Value: engine.ScopeGlobal, Label: "Global"},
			{Value: engine.ScopeRun, Label: "Run"},
		},
		Help: "Message writes a property of the message passing through. Flow is shared by every run of this flow and Global by every flow, both surviving a restart. Run lasts for this run only.",
	}
}

// scopePropertyField is the shared "which name" config field. The label reads
// for both meanings because the same field is a dot path in the message scope
// and a variable name in the others.
func scopePropertyField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "property",
		Label:       "Property / variable",
		Type:        flow.FieldString,
		Default:     "payload",
		Placeholder: "payload",
		Help:        "In the message scope this is a dot path such as payload.temperature or topic. In the other scopes it is the variable name.",
	}
}

// normalizeScope defaults an empty scope to the message and rejects unknown
// names, so a typo in a config surfaces as an error rather than a silent write
// that lands nowhere.
func normalizeScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return engine.ScopeMsg, nil
	}
	if !engine.ValidScope(scope) {
		return "", fmt.Errorf("unknown scope %q", scope)
	}
	return scope, nil
}

// normalizeProperty defaults an empty property to the payload.
func normalizeProperty(property string) string {
	property = strings.TrimSpace(property)
	if property == "" {
		return "payload"
	}
	return property
}

// readScoped reads a value from the named scope.
func readScoped(ec engine.ExecContext, scope, property string, msg *engine.Msg) (any, bool, error) {
	scope, err := normalizeScope(scope)
	if err != nil {
		return nil, false, err
	}
	property = normalizeProperty(property)
	if scope == engine.ScopeMsg {
		value, found := resolveProperty(msg, property)
		return value, found, nil
	}
	value, found := ec.GetScoped(scope, property)
	return value, found, nil
}

// writeScoped stores value in the named scope and returns the message to send
// downstream. A message-scope write produces a copy with the property set —
// the input is never mutated, because the engine records it in run history.
// The other scopes leave the message alone and pass it through.
func writeScoped(ec engine.ExecContext, scope, property string, msg *engine.Msg, value any) (*engine.Msg, error) {
	scope, err := normalizeScope(scope)
	if err != nil {
		return nil, err
	}
	property = normalizeProperty(property)

	if scope != engine.ScopeMsg {
		if err := ec.SetScoped(scope, property, value); err != nil {
			return nil, err
		}
		return passThrough(msg), nil
	}

	out := passThrough(msg)
	if err := setProperty(out, property, value); err != nil {
		return nil, fmt.Errorf("cannot write to %q: %w", property, err)
	}
	return out, nil
}

// passThrough copies a message so a node can write to the copy without
// disturbing the one the engine recorded.
func passThrough(msg *engine.Msg) *engine.Msg {
	if msg == nil {
		return &engine.Msg{}
	}
	return &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
}
