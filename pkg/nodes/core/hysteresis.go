package core

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// HysteresisNode retains its active state between separate switch thresholds.
type HysteresisNode struct{}

func (HysteresisNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "hysteresis", Label: "Hysteresis", Folder: flow.FolderLogic, Icon: "las la-wave-square",
		Description:     "Switches active state at separate thresholds, suppressing readings inside the band",
		InvocationState: flow.InvocationStateStateful,
		Inputs:          []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}, {ID: "reset", Label: "Reset", Type: flow.PortAny, Optional: true}},
		Outputs:         []engine.PortSpec{{ID: "active", Label: "Active", Type: flow.PortAny, TypeFrom: "in"}, {ID: "inactive", Label: "Inactive", Type: flow.PortAny, TypeFrom: "in"}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "property", Label: "Reading", Type: flow.FieldString, Default: "payload", Help: "Numeric message property, such as payload.humidity."},
			{ID: "lower", Label: "Lower threshold", Type: flow.FieldNumber, Required: true},
			{ID: "upper", Label: "Upper threshold", Type: flow.FieldNumber, Required: true},
			{ID: "direction", Label: "Active when", Type: flow.FieldSelect, Default: "high", Options: []engine.Option{{Value: "high", Label: "At or above the upper threshold"}, {Value: "low", Label: "At or below the lower threshold"}}},
			stateKeyField(),
		},
	}
}

func (HysteresisNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		Property  string   `json:"property"`
		Lower     *float64 `json:"lower"`
		Upper     *float64 `json:"upper"`
		Direction string   `json:"direction"`
		Key       string   `json:"key"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Lower == nil || cfg.Upper == nil || math.IsNaN(*cfg.Lower) || math.IsNaN(*cfg.Upper) || math.IsInf(*cfg.Lower, 0) || math.IsInf(*cfg.Upper, 0) || *cfg.Lower >= *cfg.Upper {
		return nil, fmt.Errorf("hysteresis needs finite lower and upper thresholds with lower below upper")
	}
	if cfg.Direction != "" && cfg.Direction != "high" && cfg.Direction != "low" {
		return nil, fmt.Errorf("unknown hysteresis direction %q", cfg.Direction)
	}
	msg := inbound(ec, inputs)
	key, err := messageStateKey(msg, cfg.Key)
	if err != nil {
		return nil, err
	}
	if inputs["reset"] != nil && inputs["in"] == nil {
		ec.DurableStore().Update("hysteresis", func(cur any, _ bool) any { values, _ := cur.(map[string]any); delete(values, key); return values })
		return nil, nil
	}
	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := resolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("reading property %q is not present on the message", property)
	}
	number, ok := toNumber(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("reading property %q must be a finite number", property)
	}
	var changed, active bool
	ec.DurableStore().Update("hysteresis", func(cur any, _ bool) any {
		values, _ := cur.(map[string]any)
		if values == nil {
			values = map[string]any{}
		}
		previous, _ := values[key].(bool)
		active = previous
		if cfg.Direction == "low" {
			if number <= *cfg.Lower {
				active = true
			} else if number >= *cfg.Upper {
				active = false
			}
		} else {
			if number >= *cfg.Upper {
				active = true
			} else if number <= *cfg.Lower {
				active = false
			}
		}
		changed = previous != active
		values[key] = active
		return values
	})
	if !changed {
		return nil, nil
	}
	if active {
		return engine.One("active", msg), nil
	}
	return engine.One("inactive", msg), nil
}
