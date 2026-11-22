package tsdbops

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// Home Assistant query bounds from spec 027.
const (
	maxHAEntities  = 50
	maxHAHistory   = 31 * 24 * time.Hour
	haDefaultType  = "mean"
	haDefaultRange = "hour"
)

var haPeriods = map[string]bool{"5minute": true, "hour": true, "day": true, "week": true, "month": true}
var haTypes = map[string]bool{"mean": true, "min": true, "max": true, "sum": true, "state": true, "change": true}

// haQuerier reads Home Assistant's recorder through a running driver.
type haQuerier struct {
	d *homeassistant.Driver
}

func (haQuerier) Languages() []string { return []string{tsdb.LangHAHistory, tsdb.LangHAStatistics} }

func (h haQuerier) Query(ctx context.Context, req tsdb.Request) (*tsdb.Result, error) {
	if req.HA == nil {
		return nil, tsdb.Rejectedf("a Home Assistant query needs entities or statistic IDs")
	}
	attrs := h.attributes(ctx)
	switch req.Language {
	case tsdb.LangHAHistory:
		return h.history(ctx, req, attrs)
	case tsdb.LangHAStatistics:
		return h.statistics(ctx, req, attrs)
	}
	return nil, tsdb.Unsupportedf("unknown Home Assistant query language %q", req.Language)
}

// attributes maps entity IDs to their current attributes, for names and
// units. A failure only loses the names.
func (h haQuerier) attributes(ctx context.Context) map[string]map[string]any {
	out := map[string]map[string]any{}
	states, err := h.d.StatesCached(ctx)
	if err != nil {
		return out
	}
	for _, s := range states {
		out[s.EntityID] = s.Attributes
	}
	return out
}

func haLabels(id string, attrs map[string]map[string]any) (map[string]string, string) {
	labels := map[string]string{"entity_id": id}
	if domain, _, ok := strings.Cut(id, "."); ok {
		labels["domain"] = domain
	}
	unit := ""
	if a, ok := attrs[id]; ok {
		if name, ok := a["friendly_name"].(string); ok {
			labels["friendly_name"] = name
		}
		if u, ok := a["unit_of_measurement"].(string); ok {
			unit = u
		}
	}
	return labels, unit
}

func (h haQuerier) history(ctx context.Context, req tsdb.Request, attrs map[string]map[string]any) (*tsdb.Result, error) {
	ids := req.HA.Entities
	if len(ids) == 0 || len(ids) > maxHAEntities {
		return nil, tsdb.Rejectedf("a history query names 1 to %d entities", maxHAEntities)
	}
	// Prepare aligns a sliding range outward to the interval, by less than
	// one step at each end, so the last 31 days arrive slightly wider.
	step := max(req.Interval, time.Second)
	if req.Range.Span() > maxHAHistory+2*step {
		return nil, tsdb.Rejectedf("Home Assistant history covers at most 31 days; use statistics for longer ranges")
	}
	hist, err := h.d.AttributeHistory(ctx, ids, req.HA.Attribute, req.Range.From, req.Range.To)
	if err != nil {
		return nil, haError(err)
	}
	res := &tsdb.Result{}
	for _, id := range ids {
		points := hist[id]
		labels, unit := haLabels(id, attrs)
		if req.HA.Attribute != "" {
			labels["attribute"] = req.HA.Attribute
			unit = ""
		}
		times := make([]any, len(points))
		nums := make([]any, len(points))
		text := make([]any, len(points))
		numeric := len(points) > 0
		for i, p := range points {
			times[i] = p.T
			text[i] = p.V
			if v, ok := homeassistant.NumericState(p.V); ok {
				nums[i] = v
			} else if p.V == "unavailable" || p.V == "unknown" {
				nums[i] = nil
			} else {
				numeric = false
			}
		}
		field := tsdb.Field{Name: "value", Type: tsdb.FieldNumber, Labels: labels, Unit: unit, Values: nums}
		if !numeric {
			field = tsdb.Field{Name: "state", Type: tsdb.FieldString, Labels: labels, Values: text}
		}
		res.Series = append(res.Series, tsdb.Series{
			Name:   seriesName(req.LegendFormat, labels, displayName(labels)),
			RefID:  req.RefID,
			Labels: labels,
			Unit:   unit,
			Fields: []tsdb.Field{{Name: "time", Type: tsdb.FieldTime, Values: times}, field},
		})
	}
	return res, nil
}

func (h haQuerier) statistics(ctx context.Context, req tsdb.Request, attrs map[string]map[string]any) (*tsdb.Result, error) {
	ids := req.HA.StatisticIDs
	if len(ids) == 0 || len(ids) > maxHAEntities {
		return nil, tsdb.Rejectedf("a statistics query names 1 to %d statistic IDs", maxHAEntities)
	}
	period := req.HA.Period
	if period == "" {
		period = haDefaultRange
	}
	if !haPeriods[period] {
		return nil, tsdb.Rejectedf("period must be 5minute, hour, day, week, or month")
	}
	types := req.HA.Types
	if len(types) == 0 {
		types = []string{haDefaultType}
	}
	for _, t := range types {
		if !haTypes[t] {
			return nil, tsdb.Rejectedf("unknown statistic type %q", t)
		}
	}
	stats, err := h.d.Statistics(ctx, ids, period, types, req.Range.From, req.Range.To)
	if err != nil {
		return nil, haError(err)
	}
	res := &tsdb.Result{}
	for _, id := range ids {
		rows := stats[id]
		for _, typ := range types {
			labels, unit := haLabels(id, attrs)
			labels["statistic_id"] = id
			labels["type"] = typ
			times := make([]any, len(rows))
			values := make([]any, len(rows))
			for i, r := range rows {
				times[i] = r.Start
				var v *float64
				switch typ {
				case "mean":
					v = r.Mean
				case "min":
					v = r.Min
				case "max":
					v = r.Max
				case "sum":
					v = r.Sum
				case "state":
					v = r.State
				case "change":
					v = r.Change
				}
				if v != nil {
					values[i] = *v
				}
			}
			name := displayName(labels)
			if len(types) > 1 {
				name += " " + typ
			}
			res.Series = append(res.Series, tsdb.Series{
				Name:   seriesName(req.LegendFormat, labels, name),
				RefID:  req.RefID,
				Labels: labels,
				Unit:   unit,
				Fields: []tsdb.Field{
					{Name: "time", Type: tsdb.FieldTime, Values: times},
					{Name: typ, Type: tsdb.FieldNumber, Labels: labels, Unit: unit, Values: values},
				},
			})
		}
	}
	return res, nil
}

func (h haQuerier) Metadata(ctx context.Context, req tsdb.MetaRequest) ([]string, error) {
	switch req.Kind {
	case tsdb.MetaEntities:
		states, err := h.d.StatesCached(ctx)
		if err != nil {
			return nil, haError(err)
		}
		out := make([]string, 0, len(states))
		for _, s := range states {
			if req.Match == "" || strings.Contains(s.EntityID, req.Match) {
				out = append(out, s.EntityID)
			}
		}
		sort.Strings(out)
		return out, nil
	case tsdb.MetaStatisticIDs:
		ids, err := h.d.ListStatisticIDs(ctx)
		if err != nil {
			return nil, haError(err)
		}
		out := make([]string, 0, len(ids))
		for _, s := range ids {
			if req.Match == "" || strings.Contains(s.StatisticID, req.Match) {
				out = append(out, s.StatisticID)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	return nil, tsdb.Unsupportedf("Home Assistant offers entities and statistic_ids lookups, not %q", req.Kind)
}

// haError classifies a driver error: Home Assistant refusing a command is
// the query's fault; anything else is the connection's.
func haError(err error) error {
	if strings.HasPrefix(err.Error(), "home assistant error") {
		return tsdb.Rejectedf("%s", err.Error())
	}
	return tsdb.Unavailable(fmt.Errorf("home assistant: %w", err))
}

func displayName(labels map[string]string) string {
	if n := labels["friendly_name"]; n != "" {
		return n
	}
	return labels["entity_id"]
}

func seriesName(legend string, labels map[string]string, def string) string {
	if legend != "" {
		return tsdb.ApplyLegend(legend, labels)
	}
	return def
}
