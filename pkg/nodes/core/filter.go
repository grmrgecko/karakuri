package core

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// FilterNode passes a message on only when the watched value has changed, and
// blocks it otherwise. Sensor topics republish constantly whether or not
// anything moved, so this is what keeps the rest of a flow from running on
// every redundant reading.
//
// The last seen value lives in the node's durable store, so it persists
// across runs, redeploys, and restarts: a threshold filter that forgot its
// baseline on every reboot would pass the next reading no matter how little
// it moved. The reset port is what clears it deliberately.
type FilterNode struct{}

func (FilterNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:            "filter",
		Label:           "Filter",
		Folder:          flow.FolderLogic,
		Description:     "Blocks messages unless the watched value changed",
		Icon:            "las la-filter",
		InvocationState: flow.InvocationStateStateful,
		// Watching a property inside the payload means the payload has to be
		// an object; topic, meta, and the payload itself leave it undeclared.
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny,
				TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}}},
			{ID: "reset", Label: "Reset", Type: flow.PortAny, Optional: true},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Mode",
				Type:    flow.FieldSelect,
				Default: "changed",
				Options: []engine.Option{
					{Value: "changed", Label: "Block unless value changed"},
					{Value: "changed_by", Label: "Block unless changed by more than"},
					{Value: "changed_by_percent", Label: "Block unless changed by more than (%)"},
					{Value: "greater", Label: "Block unless greater than last by"},
					{Value: "less", Label: "Block unless less than last by"},
				},
				Help: "The numeric modes only update the stored value when a message passes, so slow drift still eventually reports.",
			},
			{
				ID:          "property",
				Label:       "Property",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.temperature",
				Help:        "Dot path into the message: payload.*, topic, or meta.*. A bare path is read from the payload.",
			},
			{
				ID:          "delta",
				Label:       "Amount",
				Type:        flow.FieldNumber,
				Default:     1,
				Placeholder: "1",
				Help:        "Comparison amount for every mode except plain change detection.",
			},
			{
				ID:      "per_topic",
				Label:   "Track each topic separately",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "On keeps one last-value per message topic, so one filter node can serve many sensors. Off compares against a single value.",
			},
			{
				ID:      "pass_first",
				Label:   "Pass the first message",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "On emits the first message seen for a topic, since there is nothing to compare it against yet. Off waits for a change.",
			},
		},
	}
}

// filterConfig is the decoded filter node configuration.
type filterConfig struct {
	Mode      string  `json:"mode"`
	Property  string  `json:"property"`
	Delta     float64 `json:"delta"`
	PerTopic  *bool   `json:"per_topic"`
	PassFirst *bool   `json:"pass_first"`
}

// Execute compares the watched property against the last value that passed and
// emits only when the configured condition holds.
func (FilterNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg filterConfig
	cfg.Delta = 1
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	// Reject an unknown mode before touching any state, so a typo fails on the
	// first message rather than the second.
	if !filterModes[cfg.Mode] {
		return nil, fmt.Errorf("unknown filter mode %q", cfg.Mode)
	}

	// A message on the reset port clears the remembered values and emits
	// nothing, so the next real message is treated as the first again.
	if reset := inputs["reset"]; reset != nil && inputs["in"] == nil {
		ec.DurableStore().Delete(filterStoreKey)
		return nil, nil
	}

	msg := inbound(ec, inputs)
	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := resolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}

	// Bytes and text are remembered in a form the store returns unchanged, or
	// an unchanged binary value would never equal its stored copy.
	switch t := value.(type) {
	case []byte:
		value = storableText(string(t))
	case string:
		value = storableText(t)
	}

	key := ""
	if cfg.PerTopic == nil || *cfg.PerTopic {
		key = msg.Topic
	}
	passFirst := cfg.PassFirst == nil || *cfg.PassFirst

	// Decide and record in one step: concurrent runs of the same flow share the
	// store, so a read followed by a separate write could let a duplicate
	// through.
	var decision filterDecision
	ec.DurableStore().Update(filterStoreKey, func(cur any, _ bool) any {
		seen, _ := cur.(map[string]any)
		next := make(map[string]any, len(seen)+1)
		for k, v := range seen {
			next[k] = v
		}
		last, hadLast := next[key]
		decision = filterDecide(cfg.Mode, cfg.Delta, value, last, hadLast, passFirst)
		if decision.err != nil {
			return next
		}
		// The first value always becomes the baseline, even when it is not
		// emitted; otherwise a node configured to block the first message would
		// never have anything to compare against and would block forever. After
		// that, only a message that passes moves the baseline, so slow drift
		// still eventually clears a threshold.
		if decision.pass || !hadLast {
			next[key] = value
		}
		return next
	})
	if decision.err != nil {
		return nil, decision.err
	}
	if !decision.pass {
		return nil, nil
	}
	return engine.One("out", msg), nil
}

// filterStoreKey names the map of last-passed values in the node store.
const filterStoreKey = "last"

// filterModes lists the accepted comparison modes; the empty string is the
// default (plain change detection).
var filterModes = map[string]bool{
	"": true, "changed": true, "changed_by": true,
	"changed_by_percent": true, "greater": true, "less": true,
}

// filterDecision is the outcome of one comparison.
type filterDecision struct {
	pass bool
	err  error
}

// filterDecide applies the configured mode to the current and last values.
func filterDecide(mode string, delta float64, value, last any, hadLast, passFirst bool) filterDecision {
	if !hadLast {
		return filterDecision{pass: passFirst}
	}

	switch mode {
	case "", "changed":
		return filterDecision{pass: !valuesEqual(value, last)}

	case "changed_by", "changed_by_percent", "greater", "less":
		cur, ok := toNumber(value)
		if !ok {
			return filterDecision{err: fmt.Errorf("value %v is not numeric", value)}
		}
		prev, ok := toNumber(last)
		if !ok {
			// The stored value is not comparable (a mode change, or the sensor
			// started reporting numbers); take the new one and pass.
			return filterDecision{pass: true}
		}
		switch mode {
		case "changed_by":
			return filterDecision{pass: math.Abs(cur-prev) > delta}
		case "changed_by_percent":
			if prev == 0 {
				return filterDecision{pass: cur != 0}
			}
			return filterDecision{pass: math.Abs((cur-prev)/prev)*100 > delta}
		case "greater":
			return filterDecision{pass: cur-prev > delta}
		default:
			return filterDecision{pass: prev-cur > delta}
		}

	default:
		return filterDecision{err: fmt.Errorf("unknown filter mode %q", mode)}
	}
}

// valuesEqual compares two decoded values for change detection. Numbers compare
// numerically across types; everything else compares by its rendered form so
// objects and arrays are handled without reflection.
func valuesEqual(a, b any) bool {
	if an, ok := toNumber(a); ok {
		if bn, ok := toNumber(b); ok {
			return an == bn
		}
		return false
	}
	if ab, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ab == bb
	}
	return stringify(a) == stringify(b)
}
