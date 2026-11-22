package system

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/distatus/battery"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// batteryNode reports the agent host's batteries. Like the other readings it is
// an output-only pure source: one pull is one look at the controller, and a
// host with no battery answers with present false rather than failing, because
// "is the laptop on mains" is a question a desktop can answer too.
type batteryNode struct{}

func (batteryNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeBattery,
		Folder:            folder,
		Label:             "Battery",
		Description:       "Reports charge, state, and remaining time for this agent host's batteries",
		Icon:              "las la-battery-three-quarters",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
	}
}

// Execute reads every battery once and reports the whole set plus the summary
// a flow usually acts on.
func (batteryNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	batteries, err := getAllBatteries()

	// A host with no battery is an answer, not a failure. Everything else that
	// stops the whole read is: an unreadable controller is worth knowing about
	// rather than reporting as a machine on mains.
	var fatal battery.ErrFatal
	if errors.As(err, &fatal) {
		if errors.Is(fatal.Err, battery.ErrNotFound) {
			return nodeapi.One("out", &nodeapi.Msg{
				Payload: map[string]any{"present": false, "count": 0, "batteries": []any{}},
				Meta:    map[string]any{"source": "battery"},
			}), nil
		}
		return nil, fmt.Errorf("reading the battery: %w", err)
	}

	// A partial read is kept: one controller that will not report its design
	// capacity should not cost a flow the charge percentage it asked for.
	partial := err != nil

	reports := make([]any, 0, len(batteries))
	var current, full, rate float64
	state := ""
	for i, b := range batteries {
		if b == nil {
			continue
		}
		reports = append(reports, batteryReport(i, b))
		current += b.Current
		full += b.Full
		rate += b.ChargeRate
		state = mergeBatteryState(state, b.State.Raw)
	}
	if len(reports) == 0 {
		return nodeapi.One("out", &nodeapi.Msg{
			Payload: map[string]any{"present": false, "count": 0, "batteries": []any{}},
			Meta:    map[string]any{"source": "battery", "partial": partial},
		}), nil
	}

	payload := map[string]any{
		"present":     true,
		"count":       len(reports),
		"state":       state,
		"charging":    state == strings.ToLower(battery.Charging.String()),
		"on_battery":  state == strings.ToLower(battery.Discharging.String()),
		"charge_rate": round2(rate),
		"batteries":   reports,
	}
	if full > 0 {
		payload["percent"] = round2(current / full * 100)
	}
	if minutes, ok := minutesRemaining(state, current, full, rate); ok {
		payload["minutes_remaining"] = minutes
	}

	return nodeapi.One("out", &nodeapi.Msg{
		Payload: payload,
		Meta:    map[string]any{"source": "battery", "partial": partial},
	}), nil
}

// batteryReport shapes one battery. Capacities are reported in mWh as the
// library normalizes them, alongside the two percentages a flow reads: charge
// against what the pack holds today, and health against what it held new.
func batteryReport(index int, b *battery.Battery) map[string]any {
	out := map[string]any{
		"index":          index,
		"state":          strings.ToLower(b.State.String()),
		"current_mwh":    round2(b.Current),
		"full_mwh":       round2(b.Full),
		"design_mwh":     round2(b.Design),
		"charge_rate_mw": round2(b.ChargeRate),
		"voltage":        round2(b.Voltage),
	}
	if b.Full > 0 {
		out["percent"] = round2(b.Current / b.Full * 100)
	}
	if b.Design > 0 {
		out["health_percent"] = round2(b.Full / b.Design * 100)
	}
	return out
}

// mergeBatteryState reduces several packs to the one state a flow acts on.
// Charging wins over discharging, which wins over anything at rest: a laptop
// with one pack filling and one idle is charging.
func mergeBatteryState(current string, state battery.AgnosticState) string {
	next := strings.ToLower(state.String())
	rank := func(s string) int {
		switch s {
		case strings.ToLower(battery.Charging.String()):
			return 3
		case strings.ToLower(battery.Discharging.String()):
			return 2
		case "":
			return 0
		default:
			return 1
		}
	}
	if rank(next) > rank(current) {
		return next
	}
	return current
}

// minutesRemaining estimates the time to empty or to full from the momentary
// charge rate. A rate of zero means the controller is not reporting one, or the
// pack is at rest, and neither has an answer worth inventing.
func minutesRemaining(state string, current, full, rate float64) (float64, bool) {
	if rate <= 0 {
		return 0, false
	}
	switch state {
	case strings.ToLower(battery.Discharging.String()):
		return round2(current / rate * 60), true
	case strings.ToLower(battery.Charging.String()):
		if full <= current {
			return 0, false
		}
		return round2((full - current) / rate * 60), true
	default:
		return 0, false
	}
}
