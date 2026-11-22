package energy

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
)

// Meter states.
const (
	StateWaiting     = "waiting"
	StateOK          = "ok"
	StateGap         = "gap"
	StateReset       = "reset"
	StateRejected    = "rejected"
	StateUnavailable = "unavailable"
)

// Reading limits.
const (
	// futureSlack tolerates clocks a little ahead of the server's.
	futureSlack = time.Minute
	// maxAge is the oldest reading accepted live; older ones come only
	// through a fill.
	maxAge = 24 * time.Hour
	// noiseFloor and noiseShare bound a drop treated as meter noise rather
	// than a reset: below max(1% of the last value, 0.01 kWh).
	noiseFloor = 0.01
	noiseShare = 0.01
	// rebaseAfter is how many consecutive rejected readings make a meter
	// start again from the new value, for a meter replaced with a higher
	// counter.
	rebaseAfter = 3
)

// Default limits per measurement.
const (
	DefaultGapPower  = 15 * time.Minute
	DefaultGapEnergy = 6 * time.Hour
	DefaultMaxKW     = 25.0
)

// MeterConfig is how one source's readings are read.
type MeterConfig struct {
	Measurement string
	Unit        string
	GapLimit    time.Duration
	MaxKW       float64
}

// ConfigOf builds a meter config from a source with defaults filled in.
func ConfigOf(s *models.EnergySource) MeterConfig {
	c := MeterConfig{Measurement: s.Measurement, Unit: s.Unit, GapLimit: time.Duration(s.GapLimitS) * time.Second, MaxKW: s.MaxKW}
	if c.GapLimit <= 0 {
		c.GapLimit = DefaultGapEnergy
		if c.Measurement == models.EnergyMeasurePower {
			c.GapLimit = DefaultGapPower
		}
	}
	if c.MaxKW <= 0 {
		c.MaxKW = DefaultMaxKW
	}
	return c
}

// MeterState is what a meter remembers between readings. Last is in kWh for
// energy and kW for power.
type MeterState struct {
	Last     *float64
	LastAt   *time.Time
	State    string
	Note     string
	Rejected int
}

// Contribution is usage added to one hour.
type Contribution struct {
	Hour    time.Time
	KWh     float64
	Quality string
}

// milliUnit reports the milliwatt units. They differ from the megawatt ones
// only by case, so they are told apart before a unit is lowercased.
func milliUnit(unit string) bool {
	u := strings.TrimSpace(unit)
	return u == "mW" || u == "mWh"
}

// UnitFactor converts a unit to kWh or kW.
func UnitFactor(measurement, unit string) (float64, error) {
	if milliUnit(unit) {
		return 0, fmt.Errorf("unit %q is a thousandth of a watt, which is too small to track; use W, kW, or MW", strings.TrimSpace(unit))
	}
	u := strings.ToLower(strings.TrimSpace(unit))
	if measurement == models.EnergyMeasurePower {
		switch u {
		case "w":
			return 0.001, nil
		case "kw":
			return 1, nil
		case "mw":
			return 1000, nil
		}
		return 0, fmt.Errorf("power unit must be W, kW, or MW, not %q", unit)
	}
	switch u {
	case "wh":
		return 0.001, nil
	case "kwh":
		return 1, nil
	case "mwh":
		return 1000, nil
	}
	return 0, fmt.Errorf("energy unit must be Wh, kWh, or MWh, not %q", unit)
}

// Apply reads one value in the source's unit taken at `at`, and returns the
// meter's new state and the usage it adds. fill accepts readings older than
// a day, for replaying history.
func Apply(cfg MeterConfig, st MeterState, value float64, at, now time.Time, loc *time.Location, fill bool) (MeterState, []Contribution) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		st.Note = "not a number"
		return st, nil
	}
	if at.After(now.Add(futureSlack)) {
		st.Note = fmt.Sprintf("reading stamped %s is in the future", at.Format(time.RFC3339))
		return st, nil
	}
	if !fill && now.Sub(at) > maxAge {
		st.Note = fmt.Sprintf("reading stamped %s is over a day old", at.Format(time.RFC3339))
		return st, nil
	}
	factor, err := UnitFactor(cfg.Measurement, cfg.Unit)
	if err != nil {
		st.Note = err.Error()
		return st, nil
	}
	v := value * factor
	if st.Last == nil || st.LastAt == nil {
		return accept(st, v, at, StateOK, ""), nil
	}
	if !at.After(*st.LastAt) {
		// A repeat or a reading older than the last one adds nothing.
		return st, nil
	}
	elapsed := at.Sub(*st.LastAt)
	if cfg.Measurement == models.EnergyMeasurePower {
		return applyPower(cfg, st, v, at, elapsed, loc)
	}
	return applyEnergy(cfg, st, v, at, elapsed, loc)
}

func accept(st MeterState, v float64, at time.Time, state, note string) MeterState {
	value, when := v, at
	st.Last, st.LastAt, st.State, st.Note, st.Rejected = &value, &when, state, note, 0
	return st
}

func applyEnergy(cfg MeterConfig, st MeterState, v float64, at time.Time, elapsed time.Duration, loc *time.Location) (MeterState, []Contribution) {
	last := *st.Last
	delta := v - last
	state, note := StateOK, ""
	if delta < 0 {
		if -delta < math.Max(noiseShare*last, noiseFloor) {
			// Noise: keep the higher value so the climb back is not
			// counted twice.
			when := at
			st.LastAt, st.State, st.Note = &when, StateOK, ""
			return st, nil
		}
		delta, state, note = v, StateReset, fmt.Sprintf("meter reset from %.3f to %.3f kWh", last, v)
	}
	limit := cfg.MaxKW*elapsed.Hours() + noiseFloor
	if delta > limit {
		st.Rejected++
		if st.Rejected >= rebaseAfter {
			return accept(st, v, at, StateReset,
				fmt.Sprintf("started again from %.3f kWh after %d readings above the plausible maximum", v, rebaseAfter)), nil
		}
		st.State = StateRejected
		st.Note = fmt.Sprintf("rejected %.3f kWh: %.3f kWh in %s is more than %.0f kW could use", v, delta, elapsed.Round(time.Second), cfg.MaxKW)
		return st, nil
	}
	if elapsed > cfg.GapLimit {
		// The meter kept counting through the silence; the usage is real,
		// only its hours are unknown, so it lands in this reading's hour.
		if state == StateOK {
			state, note = StateGap, fmt.Sprintf("no reading for %s; usage placed in this hour", elapsed.Round(time.Minute))
		}
		return accept(st, v, at, state, note), []Contribution{{Hour: HourStart(at, loc), KWh: delta, Quality: models.EnergyQualityCarried}}
	}
	return accept(st, v, at, state, note), split(*st.LastAt, at, delta, loc)
}

func applyPower(cfg MeterConfig, st MeterState, v float64, at time.Time, elapsed time.Duration, loc *time.Location) (MeterState, []Contribution) {
	if v < 0 {
		v = 0
	}
	if elapsed > cfg.GapLimit {
		note := fmt.Sprintf("no reading for %s; that time adds nothing", elapsed.Round(time.Minute))
		return accept(st, v, at, StateGap, note), []Contribution{{Hour: HourStart(at, loc), Quality: models.EnergyQualityPartial}}
	}
	kwh := (*st.Last + v) / 2 * elapsed.Hours()
	return accept(st, v, at, StateOK, ""), split(*st.LastAt, at, kwh, loc)
}

// split spreads usage over the hours between two times in proportion to the
// time spent in each.
func split(from, to time.Time, kwh float64, loc *time.Location) []Contribution {
	total := to.Sub(from)
	if total <= 0 {
		return nil
	}
	var out []Contribution
	for start := HourStart(from, loc); start.Before(to); start = start.Add(time.Hour) {
		lo, hi := start, start.Add(time.Hour)
		if from.After(lo) {
			lo = from
		}
		if to.Before(hi) {
			hi = to
		}
		if !hi.After(lo) {
			continue
		}
		out = append(out, Contribution{Hour: start, KWh: kwh * float64(hi.Sub(lo)) / float64(total), Quality: models.EnergyQualityLive})
	}
	return out
}

// Unavailable marks a meter that reported no usable value. A power meter
// forgets its last sample so the silence is not integrated; a cumulative
// meter keeps its count, since the meter keeps counting.
func Unavailable(cfg MeterConfig, st MeterState, reason string) MeterState {
	if cfg.Measurement == models.EnergyMeasurePower {
		st.Last, st.LastAt = nil, nil
	}
	st.State, st.Note = StateUnavailable, reason
	return st
}

// worse returns the lower of two hour qualities.
func worse(a, b string) string {
	rank := map[string]int{"": 0, models.EnergyQualityLive: 1, models.EnergyQualityFilled: 2,
		models.EnergyQualityPartial: 3, models.EnergyQualityCarried: 4}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
