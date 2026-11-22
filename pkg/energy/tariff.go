package energy

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
)

// minutesPerWeek is the size of a week's coverage map.
const minutesPerWeek = 7 * 24 * 60

// maxWindowRange bounds how far ahead the cheapest window is looked for.
const maxWindowRange = 7 * 24 * time.Hour

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

var dayNames = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// ErrFutureUnknown is a question about future prices under a rate a control
// sets, which nobody knows yet.
var ErrFutureUnknown = errors.New("the tariff takes its rate from a control, so future prices are unknown")

// ErrNoTariff is a price asked for when no tariff covers the time.
var ErrNoTariff = errors.New("no tariff covers that time; add one in Settings, Energy")

// ControlRate reads a number control's current value for a rate control.
type ControlRate func(id string) (float64, bool)

// ValidateTariff checks a tariff before it is saved.
func ValidateTariff(t *models.EnergyTariff) error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("name is required")
	}
	t.Currency = strings.ToUpper(strings.TrimSpace(t.Currency))
	if !currencyRe.MatchString(t.Currency) {
		return errors.New("currency must be a three-letter ISO 4217 code such as USD or EUR")
	}
	if _, err := time.Parse("2006-01-02", t.EffectiveFrom); err != nil {
		return errors.New("effective_from must be a date YYYY-MM-DD")
	}
	if err := validateSpec("import", t.Import.V, true); err != nil {
		return err
	}
	if t.Export.V != nil {
		if err := validateSpec("export", *t.Export.V, false); err != nil {
			return err
		}
	}
	if math.IsNaN(t.DailyCharge) || math.IsInf(t.DailyCharge, 0) {
		return errors.New("daily_charge must be a number")
	}
	return nil
}

// validateSpec checks one rate: exactly one form, and periods covering the
// week once.
func validateSpec(which string, s models.EnergyRateSpec, required bool) error {
	forms := 0
	if s.Flat != nil {
		forms++
	}
	if len(s.Periods) > 0 {
		forms++
	}
	if s.ControlID != "" {
		forms++
	}
	if forms == 0 && !required {
		return nil
	}
	if forms != 1 {
		return fmt.Errorf("the %s rate needs exactly one of a flat rate, periods, or a rate control", which)
	}
	if len(s.Periods) == 0 {
		return nil
	}
	owner := make([]int, minutesPerWeek)
	for i := range owner {
		owner[i] = -1
	}
	for pi, p := range s.Periods {
		start, err := parseClock(p.Start, false)
		if err != nil {
			return fmt.Errorf("%s period %q: %w", which, p.Name, err)
		}
		end, err := parseClock(p.End, true)
		if err != nil {
			return fmt.Errorf("%s period %q: %w", which, p.Name, err)
		}
		if end <= start {
			return fmt.Errorf("%s period %q ends before it starts; split a period that crosses midnight in two", which, p.Name)
		}
		if len(p.Days) == 0 {
			return fmt.Errorf("%s period %q has no days", which, p.Name)
		}
		for _, d := range p.Days {
			if d < 1 || d > 7 {
				return fmt.Errorf("%s period %q: days are 1 (Monday) to 7 (Sunday)", which, p.Name)
			}
			base := (d - 1) * 1440
			for m := start; m < end; m++ {
				if prev := owner[base+m]; prev >= 0 {
					return fmt.Errorf("%s %s %s is in both %q and %q", which, dayNames[d-1], clockText(m),
						s.Periods[prev].Name, p.Name)
				}
				owner[base+m] = pi
			}
		}
	}
	for m := 0; m < minutesPerWeek; m++ {
		if owner[m] >= 0 {
			continue
		}
		end := m
		for end < minutesPerWeek && owner[end] < 0 && end/1440 == m/1440 {
			end++
		}
		return fmt.Errorf("%s %s %s to %s has no rate", which, dayNames[m/1440], clockText(m%1440), clockText(end%1440))
	}
	return nil
}

// parseClock reads HH:MM as minutes; at an end, 00:00 is midnight at the end
// of the day.
func parseClock(s string, end bool) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("time %q must be HH:MM", s)
	}
	m := t.Hour()*60 + t.Minute()
	if end && m == 0 {
		m = 1440
	}
	return m, nil
}

func clockText(m int) string {
	return fmt.Sprintf("%02d:%02d", m/60%24, m%60)
}

// Tariffs is the house's tariffs ordered by effective date.
type Tariffs []*models.EnergyTariff

// SortTariffs orders tariffs by effective date.
func SortTariffs(list []*models.EnergyTariff) Tariffs {
	out := append(Tariffs{}, list...)
	sort.Slice(out, func(i, j int) bool { return out[i].EffectiveFrom < out[j].EffectiveFrom })
	return out
}

// At returns the tariff in force at t, nil when none.
func (ts Tariffs) At(t time.Time, loc *time.Location) *models.EnergyTariff {
	date := LocalDate(t, loc)
	var found *models.EnergyTariff
	for _, tr := range ts {
		if tr.EffectiveFrom <= date {
			found = tr
		}
	}
	return found
}

// Price is the rate in force at one time.
type Price struct {
	Tariff   *models.EnergyTariff
	Currency string
	Import   float64
	Export   *float64
	Period   string
}

// specRate returns a spec's rate and period name at local time t.
func specRate(s models.EnergyRateSpec, t time.Time, loc *time.Location, control ControlRate, future bool) (float64, string, error) {
	switch {
	case s.Flat != nil:
		return *s.Flat, "", nil
	case s.ControlID != "":
		if future {
			return 0, "", ErrFutureUnknown
		}
		if control == nil {
			return 0, "", errors.New("the rate control cannot be read")
		}
		v, ok := control(s.ControlID)
		if !ok {
			return 0, "", fmt.Errorf("rate control %s has no number value", s.ControlID)
		}
		return v, "", nil
	}
	lt := t.In(loc)
	day := int(lt.Weekday())
	if day == 0 {
		day = 7
	}
	m := lt.Hour()*60 + lt.Minute()
	for _, p := range s.Periods {
		start, _ := parseClock(p.Start, false)
		end, _ := parseClock(p.End, true)
		if m < start || m >= end {
			continue
		}
		for _, d := range p.Days {
			if d == day {
				return p.Rate, p.Name, nil
			}
		}
	}
	return 0, "", fmt.Errorf("no period covers %s %s", dayNames[day-1], clockText(m))
}

// PriceAt returns the price in force at t. future says t is ahead of now,
// which a rate control cannot answer.
func (ts Tariffs) PriceAt(t time.Time, loc *time.Location, control ControlRate, future bool) (Price, error) {
	tr := ts.At(t, loc)
	if tr == nil {
		return Price{}, ErrNoTariff
	}
	rate, period, err := specRate(tr.Import.V, t, loc, control, future)
	if err != nil {
		return Price{}, err
	}
	p := Price{Tariff: tr, Currency: tr.Currency, Import: rate, Period: period}
	if tr.Export.V != nil {
		x, _, err := specRate(*tr.Export.V, t, loc, control, future)
		if err != nil {
			return Price{}, err
		}
		p.Export = &x
	}
	return p, nil
}

// NextChange returns when the import price next changes after now, looking
// a week ahead at minute steps: a period boundary or a new tariff.
func (ts Tariffs) NextChange(now time.Time, loc *time.Location) (time.Time, Price, error) {
	cur, err := ts.PriceAt(now, loc, nil, true)
	if err != nil {
		return time.Time{}, Price{}, err
	}
	t := now.Truncate(time.Minute).Add(time.Minute)
	limit := now.Add(8 * 24 * time.Hour)
	for ; t.Before(limit); t = t.Add(time.Minute) {
		p, err := ts.PriceAt(t, loc, nil, true)
		if err != nil {
			return time.Time{}, Price{}, err
		}
		if p.Tariff != cur.Tariff || p.Period != cur.Period || p.Import != cur.Import {
			return t, p, nil
		}
	}
	return time.Time{}, Price{}, errors.New("the price does not change within a week")
}

// Window is the cheapest stretch found.
type Window struct {
	Start    time.Time
	End      time.Time
	Average  float64
	Currency string
}

// Cheapest finds the cheapest window of length within [from, to], starting
// on a quarter hour, earliest first among equals.
func (ts Tariffs) Cheapest(length time.Duration, from, to time.Time, loc *time.Location) (Window, error) {
	if length < time.Minute {
		return Window{}, errors.New("the window must be at least a minute long")
	}
	if to.Sub(from) > maxWindowRange {
		return Window{}, errors.New("look at most 7 days ahead")
	}
	if to.Sub(from) < length {
		return Window{}, errors.New("the range is shorter than the window")
	}
	// Prices are sampled by the minute. A start inside a minute moves to the
	// next one, since the window may not begin before from.
	if t := from.Truncate(time.Minute); t.Before(from) {
		from = t.Add(time.Minute)
	} else {
		from = t
	}
	n := int(to.Sub(from) / time.Minute)
	prefix := make([]float64, n+1)
	var currency string
	for i := 0; i < n; i++ {
		p, err := ts.PriceAt(from.Add(time.Duration(i)*time.Minute), loc, nil, true)
		if err != nil {
			return Window{}, err
		}
		currency = p.Currency
		prefix[i+1] = prefix[i] + p.Import
	}
	w := int(length / time.Minute)
	best, bestAt := math.Inf(1), -1
	first := 0
	if m := from.Minute() % 15; m != 0 {
		first = 15 - m
	}
	for s := first; s+w <= n; s += 15 {
		avg := (prefix[s+w] - prefix[s]) / float64(w)
		if avg < best-1e-12 {
			best, bestAt = avg, s
		}
	}
	if bestAt < 0 {
		return Window{}, errors.New("no window fits in the range")
	}
	start := from.Add(time.Duration(bestAt) * time.Minute)
	// A running sum of many minutes carries float error into the last
	// digits; a rate is never quoted that finely.
	best = math.Round(best*1e9) / 1e9
	return Window{Start: start, End: start.Add(length), Average: best, Currency: currency}, nil
}
