package energy

import (
	"context"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// Querier answers the energy query language for charts and the time-series
// query tools.
type Querier struct{}

// Languages lists the one language the energy connection accepts.
func (Querier) Languages() []string { return []string{tsdb.LangEnergy} }

// autoPeriod picks the period for a chart's interval.
func autoPeriod(interval time.Duration) string {
	switch {
	case interval <= time.Hour:
		return PeriodHour
	case interval <= 24*time.Hour:
		return PeriodDay
	case interval <= 7*24*time.Hour:
		return PeriodWeek
	}
	return PeriodMonth
}

// unitOf is the chart unit of a measure.
func unitOf(measure, currency string) string {
	switch measure {
	case MeasureCost, MeasureExportCredit, MeasureDailyCharge, MeasureTotalCost:
		if currency == "" {
			return ""
		}
		return "currency" + currency
	case MeasureSolarShare:
		return "percent"
	}
	return "kwatth"
}

// Query answers one energy query: a series per target, or with table
// format one row per target holding its total over the range.
func (Querier) Query(ctx context.Context, req tsdb.Request) (*tsdb.Result, error) {
	sel := tsdb.EnergyQuery{}
	if req.Energy != nil {
		sel = *req.Energy
	}
	targets := sel.Targets
	if len(targets) == 0 {
		targets = []string{TargetHouse}
	}
	measure := sel.Measure
	if measure == "" {
		measure = MeasureUsage
	}
	period := sel.Period
	if period == "" || period == "auto" {
		period = autoPeriod(req.Interval)
	}
	if !ValidPeriod(period) {
		return nil, &tsdb.Error{Class: tsdb.ClassRejected, Message: "period must be hour, day, week, month, or auto"}
	}
	res := &tsdb.Result{}
	var names []any
	var totals []any
	currency := ""
	for _, target := range targets {
		target = strings.TrimSpace(target)
		roll, err := Compute(target, []string{measure}, req.Range.From, req.Range.To, period)
		if err != nil {
			return nil, &tsdb.Error{Class: tsdb.ClassRejected, Message: err.Error()}
		}
		if roll.Currency != "" {
			currency = roll.Currency
		}
		if req.Format == tsdb.FormatTable {
			sum := 0.0
			for _, row := range roll.Rows {
				sum += row.Values[measure]
			}
			if measure == MeasureSolarShare {
				sum = shareOver(req.Range.From, req.Range.To, period)
			}
			names = append(names, roll.Name)
			totals = append(totals, sum)
			continue
		}
		times := make([]int64, 0, len(roll.Rows))
		values := make([]any, 0, len(roll.Rows))
		for _, row := range roll.Rows {
			times = append(times, row.Start.UnixMilli())
			values = append(values, row.Values[measure])
		}
		s := tsdb.TimeSeries(roll.Name, map[string]string{"target": target, "measure": measure}, times, values)
		s.Unit = unitOf(measure, roll.Currency)
		s.Fields[1].Unit = s.Unit
		s.Fields[1].Name = roll.Name
		res.Series = append(res.Series, s)
	}
	if req.Format == tsdb.FormatTable {
		unit := unitOf(measure, currency)
		res.Series = []tsdb.Series{{Name: measure, Unit: unit, Fields: []tsdb.Field{
			{Name: "name", Type: tsdb.FieldString, Values: names},
			{Name: measure, Type: tsdb.FieldNumber, Unit: unit, Values: totals},
		}}}
	}
	return res, nil
}

// shareOver is the solar share over a whole range. Each row holds its own
// period's share, and shares do not add, so the range's share is worked out
// from the sums: solar used at home over the house total, the same two
// figures a row divides. It reads the asked range rather than the rows'
// periods, which reach past it when it starts or ends inside one.
func shareOver(from, to time.Time, period string) float64 {
	whole, err := Compute(TargetHouse, []string{MeasureSelfConsumption, MeasureUsage}, from, to, period)
	if err != nil {
		return 0
	}
	self, use := 0.0, 0.0
	for _, row := range whole.Rows {
		self += row.Values[MeasureSelfConsumption]
		use += row.Values[MeasureUsage]
	}
	if use == 0 {
		return 0
	}
	return self / use * 100
}

// Metadata lists the energy targets for editors.
func (Querier) Metadata(ctx context.Context, req tsdb.MetaRequest) ([]string, error) {
	if req.Kind != "" && req.Kind != tsdb.MetaTargets {
		return nil, &tsdb.Error{Class: tsdb.ClassUnsupported, Message: "the energy connection lists targets only"}
	}
	list, err := TargetList()
	if err != nil {
		return nil, &tsdb.Error{Class: tsdb.ClassUnavailable, Message: err.Error()}
	}
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Target)
	}
	return out, nil
}
