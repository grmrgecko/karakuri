package energy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Targets.
const (
	TargetHouse     = "house"
	TargetUntracked = "untracked"
	TargetSolar     = "solar"
	prefixGroup     = "group:"
	prefixSource    = "source:"
)

// Measures.
const (
	MeasureUsage           = "usage"
	MeasureCost            = "cost"
	MeasureExportCredit    = "export_credit"
	MeasureDailyCharge     = "daily_charge"
	MeasureTotalCost       = "total_cost"
	MeasureImport          = "import"
	MeasureExport          = "export"
	MeasureProduction      = "production"
	MeasureSelfConsumption = "self_consumption"
	MeasureSolarShare      = "solar_share"
)

// Measures lists every measure in display order.
var Measures = []string{MeasureUsage, MeasureCost, MeasureExportCredit, MeasureDailyCharge, MeasureTotalCost,
	MeasureImport, MeasureExport, MeasureProduction, MeasureSelfConsumption, MeasureSolarShare}

// Periods.
const (
	PeriodHour  = "hour"
	PeriodDay   = "day"
	PeriodWeek  = "week"
	PeriodMonth = "month"
)

// maxBuckets bounds one rollup's rows.
const maxBuckets = 10000

// Row is one period of a rollup.
type Row struct {
	Start    time.Time          `json:"start" description:"Period start."`
	End      time.Time          `json:"end" description:"Period end."`
	Values   map[string]float64 `json:"values" description:"Value of each measure asked for."`
	Complete bool               `json:"complete" description:"False when an hour in the period had a gap, carried usage, or has not ended."`
}

// Rollup is a target's figures over a range.
type Rollup struct {
	Target   string `json:"target" description:"house, untracked, solar, group:<id>, or source:<id>."`
	Name     string `json:"name" description:"Display name of the target."`
	Currency string `json:"currency,omitempty" description:"Currency of cost measures."`
	Period   string `json:"period" description:"hour, day, week, or month."`
	Rows     []Row  `json:"rows" description:"One row per period, oldest first."`
}

// data is everything a rollup reads, loaded once per request.
type data struct {
	loc       *time.Location
	weekStart int
	sources   map[uuid.UUID]*models.EnergySource
	groups    map[uuid.UUID]*models.EnergyGroup
	members   []*models.EnergyGroupMember
	tariffs   Tariffs
	hours     map[time.Time]map[uuid.UUID]models.EnergyHour
	rates     map[time.Time]models.EnergyRate
	now       time.Time
}

// load reads sources, groups, tariffs, hours, and rates for [from, to).
func load(from, to, now time.Time) (*data, error) {
	settings := CurrentSettings()
	d := &data{loc: Location(), weekStart: settings.WeekStart, sources: map[uuid.UUID]*models.EnergySource{},
		groups: map[uuid.UUID]*models.EnergyGroup{}, hours: map[time.Time]map[uuid.UUID]models.EnergyHour{},
		rates: map[time.Time]models.EnergyRate{}, now: now}
	srcs, err := models.ListEnergySources()
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		d.sources[s.ID] = s
	}
	groups, err := models.ListEnergyGroups()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		d.groups[g.ID] = g
	}
	if d.members, err = models.ListEnergyGroupMembers(); err != nil {
		return nil, err
	}
	tariffs, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, err
	}
	d.tariffs = SortTariffs(tariffs)
	hours, err := models.EnergyHoursBetween(nil, from, to)
	if err != nil {
		return nil, err
	}
	for _, h := range hours {
		if _, live := d.sources[h.SourceID]; !live {
			continue
		}
		key := h.Hour.UTC()
		if d.hours[key] == nil {
			d.hours[key] = map[uuid.UUID]models.EnergyHour{}
		}
		d.hours[key][h.SourceID] = h
	}
	rates, err := models.EnergyRatesBetween(from, to)
	if err != nil {
		return nil, err
	}
	for _, r := range rates {
		d.rates[r.Hour.UTC()] = r
	}
	return d, nil
}

// hourFigures is the house's figures in one hour.
type hourFigures struct {
	source     map[uuid.UUID]float64
	consumers  float64
	house      float64
	imports    float64
	exports    float64
	production float64
	hasImport  bool
	partial    bool
}

// figures derives one hour's totals. House usage comes from the house
// source, else from import plus production minus export, else from the
// consumers (FR-006).
func (d *data) figures(h time.Time) hourFigures {
	f := hourFigures{source: map[uuid.UUID]float64{}}
	var houseSrc *float64
	hasGrid := false
	for _, s := range d.sources {
		switch s.Role {
		case models.EnergyRoleImport, models.EnergyRoleExport, models.EnergyRoleProduction:
			hasGrid = true
		}
		if s.Role == models.EnergyRoleImport {
			f.hasImport = true
		}
	}
	for id, row := range d.hours[h] {
		s := d.sources[id]
		f.source[id] = row.KWh
		if row.Quality == models.EnergyQualityPartial || row.Quality == models.EnergyQualityCarried {
			f.partial = true
		}
		switch s.Role {
		case models.EnergyRoleConsumer:
			f.consumers += row.KWh
		case models.EnergyRoleHouse:
			v := row.KWh
			houseSrc = &v
		case models.EnergyRoleImport:
			f.imports += row.KWh
		case models.EnergyRoleExport:
			f.exports += row.KWh
		case models.EnergyRoleProduction:
			f.production += row.KWh
		}
	}
	switch {
	case houseSrc != nil:
		f.house = *houseSrc
	case d.hasRole(models.EnergyRoleHouse):
		f.house = 0
	case hasGrid:
		f.house = f.imports + f.production - f.exports
		if f.house < 0 {
			f.house = 0
		}
	default:
		f.house = f.consumers
	}
	return f
}

func (d *data) hasRole(role string) bool {
	for _, s := range d.sources {
		if s.Role == role {
			return true
		}
	}
	return false
}

// member reports whether a source belonged to a group at hour h. The hour
// holding the join counts, and it is a local hour: in a half-hour zone it
// does not start on a UTC hour.
func (d *data) member(group, source uuid.UUID, h time.Time) bool {
	for _, m := range d.members {
		if m.GroupID != group || m.SourceID != source {
			continue
		}
		if !h.Before(HourStart(m.JoinedAt, d.loc)) && (m.LeftAt == nil || h.Before(*m.LeftAt)) {
			return true
		}
	}
	return false
}

// usageOf is a target's usage in one hour.
func (d *data) usageOf(target string, h time.Time, f hourFigures) (float64, error) {
	switch {
	case target == TargetHouse:
		return f.house, nil
	case target == TargetUntracked:
		u := f.house - f.consumers
		if u < 0 {
			u = 0
		}
		return u, nil
	case target == TargetSolar:
		return f.production, nil
	case strings.HasPrefix(target, prefixSource):
		id, err := uuid.Parse(strings.TrimPrefix(target, prefixSource))
		if err != nil {
			return 0, fmt.Errorf("unknown target %q", target)
		}
		return f.source[id], nil
	case strings.HasPrefix(target, prefixGroup):
		id, err := uuid.Parse(strings.TrimPrefix(target, prefixGroup))
		if err != nil {
			return 0, fmt.Errorf("unknown target %q", target)
		}
		sum := 0.0
		for sid, v := range f.source {
			if d.member(id, sid, h) {
				sum += v
			}
		}
		return sum, nil
	}
	return 0, fmt.Errorf("unknown target %q", target)
}

// checkTarget refuses a target that names nothing.
func (d *data) checkTarget(target string) (string, error) {
	switch target {
	case TargetHouse:
		return "House", nil
	case TargetUntracked:
		return "Untracked", nil
	case TargetSolar:
		return "Solar", nil
	}
	if strings.HasPrefix(target, prefixSource) {
		if id, err := uuid.Parse(strings.TrimPrefix(target, prefixSource)); err == nil {
			if s, ok := d.sources[id]; ok {
				return s.Name, nil
			}
		}
		return "", fmt.Errorf("no energy source %q", strings.TrimPrefix(target, prefixSource))
	}
	if strings.HasPrefix(target, prefixGroup) {
		if id, err := uuid.Parse(strings.TrimPrefix(target, prefixGroup)); err == nil {
			if g, ok := d.groups[id]; ok {
				return g.Name, nil
			}
		}
		return "", fmt.Errorf("no energy group %q", strings.TrimPrefix(target, prefixGroup))
	}
	return "", fmt.Errorf("unknown target %q: use house, untracked, solar, group:<id>, or source:<id>", target)
}

// bucketStart is the start of the period holding t.
func (d *data) bucketStart(t time.Time, period string) time.Time {
	switch period {
	case PeriodDay:
		return DayStart(t, d.loc)
	case PeriodWeek:
		return WeekStartOf(t, d.loc, d.weekStart)
	case PeriodMonth:
		return MonthStartOf(t, d.loc)
	}
	return HourStart(t, d.loc)
}

// bucketEnd is the end of the period starting at s.
func (d *data) bucketEnd(s time.Time, period string) time.Time {
	switch period {
	case PeriodDay:
		return AddDays(s, 1, d.loc)
	case PeriodWeek:
		return AddDays(s, 7, d.loc)
	case PeriodMonth:
		lt := s.In(d.loc)
		return time.Date(lt.Year(), lt.Month()+1, 1, 0, 0, 0, 0, d.loc)
	}
	return s.Add(time.Hour)
}

// ValidPeriod reports whether a period is known.
func ValidPeriod(p string) bool {
	return p == PeriodHour || p == PeriodDay || p == PeriodWeek || p == PeriodMonth
}

// Compute rolls a target's measures up by period over [from, to).
func Compute(target string, measures []string, from, to time.Time, period string) (*Rollup, error) {
	if R != nil {
		R.Flush()
	}
	now := time.Now()
	if !ValidPeriod(period) {
		return nil, fmt.Errorf("period must be hour, day, week, or month")
	}
	if !to.After(from) {
		return nil, errors.New("the range ends before it starts")
	}
	for _, m := range measures {
		if !validMeasure(m) {
			return nil, fmt.Errorf("unknown measure %q", m)
		}
	}
	if len(measures) == 0 {
		measures = []string{MeasureUsage}
	}
	d, err := load(from, to, now)
	if err != nil {
		return nil, err
	}
	return d.compute(target, measures, from, to, period)
}

func validMeasure(m string) bool {
	for _, x := range Measures {
		if x == m {
			return true
		}
	}
	return false
}

func (d *data) compute(target string, measures []string, from, to time.Time, period string) (*Rollup, error) {
	name, err := d.checkTarget(target)
	if err != nil {
		return nil, err
	}
	out := &Rollup{Target: target, Name: name, Period: period, Rows: []Row{}}
	type acc struct {
		row  Row
		self float64
		home float64
	}
	var buckets []*acc
	index := map[time.Time]*acc{}
	start := d.bucketStart(from, period)
	for s := start; s.Before(to); s = d.bucketEnd(s, period) {
		if len(buckets) >= maxBuckets {
			return nil, fmt.Errorf("the range holds more than %d periods; use a longer period", maxBuckets)
		}
		a := &acc{row: Row{Start: s, End: d.bucketEnd(s, period), Values: map[string]float64{}, Complete: true}}
		if a.row.End.After(d.now) {
			a.row.Complete = false
		}
		for _, m := range measures {
			a.row.Values[m] = 0
		}
		buckets = append(buckets, a)
		index[s.UTC()] = a
	}
	isHouse := target == TargetHouse
	for h := HourStart(start, d.loc); h.Before(to); h = h.Add(time.Hour) {
		if h.Before(from) || h.After(d.now) {
			continue
		}
		a := index[d.bucketStart(h, period).UTC()]
		if a == nil {
			continue
		}
		f := d.figures(h)
		if f.partial {
			a.row.Complete = false
		}
		usage, err := d.usageOf(target, h, f)
		if err != nil {
			return nil, err
		}
		rate, hasRate := d.rates[h]
		if hasRate && out.Currency == "" {
			out.Currency = rate.Currency
		}
		var cost, credit float64
		if hasRate {
			billed := usage
			if isHouse && f.hasImport {
				billed = f.imports
			}
			cost = billed * rate.ImportRate
			if isHouse && rate.ExportRate != nil {
				credit = f.exports * *rate.ExportRate
			}
		}
		charge := 0.0
		if isHouse && h.Equal(HourStart(DayStart(h, d.loc), d.loc)) {
			if tr := d.tariffs.At(h, d.loc); tr != nil {
				charge = tr.DailyCharge
				if out.Currency == "" {
					out.Currency = tr.Currency
				}
			}
		}
		v := a.row.Values
		for _, m := range measures {
			switch m {
			case MeasureUsage:
				v[m] += usage
			case MeasureCost:
				v[m] += cost
			case MeasureExportCredit:
				v[m] += credit
			case MeasureDailyCharge:
				v[m] += charge
			case MeasureTotalCost:
				v[m] += cost - credit + charge
			case MeasureImport:
				v[m] += f.imports
			case MeasureExport:
				v[m] += f.exports
			case MeasureProduction:
				v[m] += f.production
			case MeasureSelfConsumption:
				v[m] += selfConsumption(f)
			}
		}
		a.self += selfConsumption(f)
		a.home += f.house
	}
	for _, a := range buckets {
		if _, ok := a.row.Values[MeasureSolarShare]; ok && a.home > 0 {
			a.row.Values[MeasureSolarShare] = a.self / a.home * 100
		}
		out.Rows = append(out.Rows, a.row)
	}
	return out, nil
}

func selfConsumption(f hourFigures) float64 {
	s := f.production - f.exports
	if s < 0 {
		return 0
	}
	return s
}

// TargetList lists every target the house has, for pickers and metadata.
func TargetList() ([]TargetInfo, error) {
	srcs, err := models.ListEnergySources()
	if err != nil {
		return nil, err
	}
	groups, err := models.ListEnergyGroups()
	if err != nil {
		return nil, err
	}
	out := []TargetInfo{{Target: TargetHouse, Name: "House"}, {Target: TargetUntracked, Name: "Untracked"}}
	for _, s := range srcs {
		if s.Role == models.EnergyRoleProduction {
			out = append(out, TargetInfo{Target: TargetSolar, Name: "Solar"})
			break
		}
	}
	for _, g := range groups {
		out = append(out, TargetInfo{Target: prefixGroup + g.ID.String(), Name: g.Name})
	}
	sort.Slice(srcs, func(i, j int) bool { return srcs[i].Name < srcs[j].Name })
	for _, s := range srcs {
		out = append(out, TargetInfo{Target: prefixSource + s.ID.String(), Name: s.Name})
	}
	return out, nil
}

// TargetInfo names one target.
type TargetInfo struct {
	Target string `json:"target" description:"Target reference."`
	Name   string `json:"name" description:"Display name."`
}
