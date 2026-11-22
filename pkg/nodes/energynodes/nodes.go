// Package energynodes contributes the nodes that read energy usage and
// prices and start runs when the price period changes.
package energynodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// energyPlugin contributes the energy nodes.
type energyPlugin struct{}

// Plugin returns the energy nodes as a built-in plugin.
func Plugin() plugin.Plugin { return energyPlugin{} }

func (energyPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "energy",
		Description: "Energy usage and price reads, and a trigger on price period changes.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (energyPlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})
	return nil
}

// RegisterAll registers the energy nodes.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(UsageNode{})
	reg.Register(PriceNode{})
	reg.Register(PricePeriodTrigger{})
}

// Spans.
const (
	spanToday     = "today"
	spanYesterday = "yesterday"
	spanThisWeek  = "this_week"
	spanThisMonth = "this_month"
	spanLastHours = "last_hours"
	spanRange     = "range"
	// usualWeeks is how many earlier matching weekdays make the usual level.
	usualWeeks = 4
)

// UsageNode reads a target's usage and cost over a span.
type UsageNode struct{}

func (UsageNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "energy-usage",
		Label:       "Energy Usage",
		Folder:      flow.FolderEnergy,
		Description: "Reads usage and cost of the house, a group, or a source over a span, optionally against the usual level",
		Icon:        "las la-bolt",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Usage", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "target", Label: "Target", Type: flow.FieldString, Default: "house", Placeholder: "house",
				Help: "house, untracked, solar, group:<id>, or source:<id>, as Settings, Energy lists them. Expressions allowed."},
			{ID: "span", Label: "Span", Type: flow.FieldSelect, Default: spanToday, Options: []engine.Option{
				{Value: spanToday, Label: "Today so far"}, {Value: spanYesterday, Label: "Yesterday"},
				{Value: spanThisWeek, Label: "This week"}, {Value: spanThisMonth, Label: "This month"},
				{Value: spanLastHours, Label: "Last hours"}, {Value: spanRange, Label: "Range"}}},
			{ID: "hours", Label: "Hours", Type: flow.FieldNumber, Default: 24, ShowIf: &flow.ShowIf{Field: "span", Equals: spanLastHours}},
			{ID: "from", Label: "From", Type: flow.FieldString, Placeholder: "now-7d/d", ShowIf: &flow.ShowIf{Field: "span", Equals: spanRange},
				Help: "Range start: now-7d/d, RFC 3339, or Unix milliseconds."},
			{ID: "to", Label: "To", Type: flow.FieldString, Placeholder: "now", ShowIf: &flow.ShowIf{Field: "span", Equals: spanRange}},
			{ID: "compare_usual", Label: "Compare with usual", Type: flow.FieldBoolean, Default: false,
				Help: "Add the average of the same span on the previous four matching weekdays, and the ratio to it."},
		},
	}
}

type usageConfig struct {
	Target       string  `json:"target"`
	Span         string  `json:"span"`
	Hours        float64 `json:"hours"`
	From         string  `json:"from"`
	To           string  `json:"to"`
	CompareUsual bool    `json:"compare_usual"`
}

// spanRange returns the span's start and end.
func spanBounds(cfg usageConfig, now time.Time, loc *time.Location) (time.Time, time.Time, error) {
	switch cfg.Span {
	case "", spanToday:
		return energy.DayStart(now, loc), now, nil
	case spanYesterday:
		today := energy.DayStart(now, loc)
		return energy.AddDays(today, -1, loc), today, nil
	case spanThisWeek:
		return energy.WeekStartOf(now, loc, energy.CurrentSettings().WeekStart), now, nil
	case spanThisMonth:
		return energy.MonthStartOf(now, loc), now, nil
	case spanLastHours:
		h := cfg.Hours
		if h <= 0 {
			h = 24
		}
		if h > 24*400 {
			return time.Time{}, time.Time{}, errors.New("hours must be at most 9600")
		}
		return now.Add(-time.Duration(h * float64(time.Hour))), now, nil
	case spanRange:
		from, to := cfg.From, cfg.To
		if to == "" {
			to = "now"
		}
		r, err := tsdb.ParseRangeWeek(from, to, now.In(loc), energy.CurrentSettings().WeekStart)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return r.From, r.To, nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("unknown span %q", cfg.Span)
}

// total sums a rollup's rows.
func total(r *energy.Rollup, measure string) float64 {
	sum := 0.0
	for _, row := range r.Rows {
		sum += row.Values[measure]
	}
	return sum
}

func (UsageNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg usageConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	target, err := ec.Interpolate(strings.TrimSpace(cfg.Target), msg)
	if err != nil {
		return nil, err
	}
	if target == "" {
		target = energy.TargetHouse
	}
	loc := energy.Location()
	now := ec.Clock().Now()
	from, to, err := spanBounds(cfg, now, loc)
	if err != nil {
		return nil, err
	}
	costMeasure := energy.MeasureCost
	if target == energy.TargetHouse {
		costMeasure = energy.MeasureTotalCost
	}
	roll, err := energy.Compute(target, []string{energy.MeasureUsage, costMeasure}, from, to, energy.PeriodHour)
	if err != nil {
		return nil, err
	}
	complete := true
	for _, row := range roll.Rows {
		if !row.Complete && row.End.Before(now) {
			complete = false
		}
	}
	kwh := total(roll, energy.MeasureUsage)
	payload := map[string]any{
		"target": target, "name": roll.Name, "kwh": kwh, "cost": total(roll, costMeasure), "currency": roll.Currency,
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "complete": complete,
	}
	if w, ok := currentPower(target); ok {
		payload["power_w"] = w
	}
	if cfg.CompareUsual {
		sum, n := 0.0, 0
		for i := 1; i <= usualWeeks; i++ {
			pf := shiftDays(from, -7*i, loc)
			pt := pf.Add(to.Sub(from))
			past, err := energy.Compute(target, []string{energy.MeasureUsage}, pf, pt, energy.PeriodHour)
			if err != nil {
				return nil, err
			}
			sum += total(past, energy.MeasureUsage)
			n++
		}
		usual := sum / float64(n)
		payload["usual_kwh"] = usual
		if usual > 0 {
			payload["ratio"] = kwh / usual
		}
	}
	return engine.One("out", &engine.Msg{Payload: payload, Topic: "energy:" + target, Group: msg.Group}), nil
}

// shiftDays moves a time by whole local days, keeping its wall clock time.
func shiftDays(t time.Time, days int, loc *time.Location) time.Time {
	lt := t.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day()+days, lt.Hour(), lt.Minute(), lt.Second(), lt.Nanosecond(), loc)
}

// currentPower is a power source's last reading in watts.
func currentPower(target string) (float64, bool) {
	if !strings.HasPrefix(target, "source:") {
		return 0, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(target, "source:"))
	if err != nil {
		return 0, false
	}
	s, err := models.FindEnergySource(id)
	if err != nil || s.Measurement != models.EnergyMeasurePower || s.LastValue == nil {
		return 0, false
	}
	f, err := energy.UnitFactor(s.Measurement, s.Unit)
	if err != nil {
		return 0, false
	}
	return *s.LastValue * f * 1000, true
}

// PriceNode reads the price now or at a time, or finds the cheapest window.
type PriceNode struct{}

func (PriceNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "energy-price",
		Label:       "Energy Price",
		Folder:      flow.FolderEnergy,
		Description: "Reads the electricity price now or at a time, or finds the cheapest window ahead",
		Icon:        "las la-tag",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Price", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "mode", Label: "Mode", Type: flow.FieldSelect, Default: "now", Options: []engine.Option{
				{Value: "now", Label: "Price now"}, {Value: "at", Label: "Price at a time"}, {Value: "cheapest", Label: "Cheapest window"}}},
			{ID: "at", Label: "At", Type: flow.FieldString, Placeholder: "{{ payload.time }}", ShowIf: &flow.ShowIf{Field: "mode", Equals: "at"},
				Help: "RFC 3339 time or a range expression such as now+2h."},
			{ID: "hours", Label: "Window hours", Type: flow.FieldNumber, Default: 3, ShowIf: &flow.ShowIf{Field: "mode", Equals: "cheapest"}},
			{ID: "from", Label: "Earliest start", Type: flow.FieldString, Placeholder: "now", ShowIf: &flow.ShowIf{Field: "mode", Equals: "cheapest"}},
			{ID: "to", Label: "Latest end", Type: flow.FieldString, Placeholder: "now+1d/d+7h", ShowIf: &flow.ShowIf{Field: "mode", Equals: "cheapest"},
				Help: "The window lies between these, such as now to tomorrow 07:00 for a load that must finish by then."},
		},
	}
}

type priceConfig struct {
	Mode  string  `json:"mode"`
	At    string  `json:"at"`
	Hours float64 `json:"hours"`
	From  string  `json:"from"`
	To    string  `json:"to"`
}

func (PriceNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg priceConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, err
	}
	ts := energy.SortTariffs(list)
	loc := energy.Location()
	now := ec.Clock().Now()
	switch cfg.Mode {
	case "cheapest":
		hours := cfg.Hours
		if hours <= 0 {
			hours = 3
		}
		from, to := cfg.From, cfg.To
		if from == "" {
			from = "now"
		}
		if to == "" {
			to = "now+24h"
		}
		if from, err = ec.Interpolate(from, msg); err != nil {
			return nil, err
		}
		if to, err = ec.Interpolate(to, msg); err != nil {
			return nil, err
		}
		r, err := tsdb.ParseRangeWeek(from, to, now.In(loc), energy.CurrentSettings().WeekStart)
		if err != nil {
			return nil, err
		}
		w, err := ts.Cheapest(time.Duration(hours*float64(time.Hour)), r.From, r.To, loc)
		if err != nil {
			return nil, err
		}
		return engine.One("out", &engine.Msg{Topic: "energy:cheapest", Group: msg.Group, Payload: map[string]any{
			"start": w.Start.Format(time.RFC3339), "end": w.End.Format(time.RFC3339),
			"average_rate": w.Average, "currency": w.Currency}}), nil
	case "at":
		raw, err := ec.Interpolate(cfg.At, msg)
		if err != nil {
			return nil, err
		}
		at, err := tsdb.ParseTime(raw, now.In(loc), false)
		if err != nil {
			return nil, err
		}
		return priceMsg(ts, at, now, loc, msg.Group)
	}
	return priceMsg(ts, now, now, loc, msg.Group)
}

func priceMsg(ts energy.Tariffs, at, now time.Time, loc *time.Location, group []engine.GroupFrame) (map[string][]*engine.Msg, error) {
	p, err := ts.PriceAt(at, loc, energy.ControlValue, at.After(now.Add(time.Minute)))
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"at": at.Format(time.RFC3339), "rate": p.Import, "currency": p.Currency, "period": p.Period}
	if p.Export != nil {
		payload["export_rate"] = *p.Export
	}
	if p.Tariff.Import.V.ControlID == "" {
		if next, _, err := ts.NextChange(at, loc); err == nil {
			payload["ends_at"] = next.Format(time.RFC3339)
		}
	}
	return engine.One("out", &engine.Msg{Topic: "energy:price", Payload: payload, Group: group}), nil
}
