package energy

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var chicago = mustLoc("America/Chicago")

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func fp(v float64) *float64 { return &v }

// reading is one value at a local wall time on 2026-11-02.
type reading struct {
	clock string
	value float64
}

func at(clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-11-02 "+clock, chicago)
	if err != nil {
		panic(err)
	}
	return t
}

// run feeds readings and returns kWh per local hour label and the final
// state.
func run(cfg MeterConfig, readings []reading) (map[string]float64, map[string]string, MeterState) {
	st := MeterState{}
	kwh, quality := map[string]float64{}, map[string]string{}
	now := at("23:59")
	for _, r := range readings {
		var cs []Contribution
		st, cs = Apply(cfg, st, r.value, at(r.clock), now, chicago, false)
		for _, c := range cs {
			label := c.Hour.In(chicago).Format("15")
			kwh[label] += c.KWh
			quality[label] = worse(quality[label], c.Quality)
		}
	}
	return kwh, quality, st
}

func TestApplyEnergy(t *testing.T) {
	energyCfg := MeterConfig{Measurement: models.EnergyMeasureEnergy, Unit: "kWh", GapLimit: 6 * time.Hour, MaxKW: 25}
	tests := []struct {
		name     string
		cfg      MeterConfig
		readings []reading
		want     map[string]float64
		quality  map[string]string
		state    string
	}{
		{name: "difference within an hour", cfg: energyCfg,
			readings: []reading{{"10:00", 100}, {"10:59", 101.5}},
			want:     map[string]float64{"10": 1.5}, state: StateOK},
		{name: "split across hours by time", cfg: energyCfg,
			readings: []reading{{"10:30", 100}, {"11:30", 102}},
			want:     map[string]float64{"10": 1, "11": 1}, state: StateOK},
		{name: "reset counts the new value", cfg: energyCfg,
			readings: []reading{{"10:00", 250.3}, {"10:30", 0.2}},
			want:     map[string]float64{"10": 0.2}, state: StateReset},
		{name: "noise adds nothing and the climb back is not counted twice", cfg: energyCfg,
			readings: []reading{{"10:00", 100}, {"10:10", 99.995}, {"10:20", 100.5}},
			want:     map[string]float64{"10": 0.5}, state: StateOK},
		{name: "implausible jump is rejected", cfg: energyCfg,
			readings: []reading{{"10:00", 100}, {"10:10", 900}, {"10:20", 100.5}},
			want:     map[string]float64{"10": 0.5}, state: StateOK},
		{name: "three implausible readings start again", cfg: energyCfg,
			readings: []reading{{"10:00", 100}, {"10:10", 900}, {"10:20", 900.1}, {"10:30", 900.2}, {"10:40", 900.7}},
			want:     map[string]float64{"10": 0.5}, state: StateOK},
		{name: "a gap is carried to the reading's hour", cfg: energyCfg,
			readings: []reading{{"02:00", 100}, {"09:30", 104}},
			want:     map[string]float64{"09": 4}, quality: map[string]string{"09": models.EnergyQualityCarried}, state: StateGap},
		{name: "Wh are converted", cfg: MeterConfig{Measurement: models.EnergyMeasureEnergy, Unit: "Wh", GapLimit: time.Hour, MaxKW: 25},
			readings: []reading{{"10:00", 1000}, {"10:30", 1500}},
			want:     map[string]float64{"10": 0.5}, state: StateOK},
		{name: "power integrates by trapezoid", cfg: MeterConfig{Measurement: models.EnergyMeasurePower, Unit: "W", GapLimit: 15 * time.Minute, MaxKW: 25},
			readings: []reading{{"10:00", 2000}, {"10:10", 2000}, {"10:20", 2000}, {"10:30", 2000}},
			want:     map[string]float64{"10": 1}, state: StateOK},
		{name: "power silence adds nothing", cfg: MeterConfig{Measurement: models.EnergyMeasurePower, Unit: "kW", GapLimit: 15 * time.Minute, MaxKW: 25},
			readings: []reading{{"10:00", 2}, {"10:10", 2}, {"11:00", 2}, {"11:10", 2}},
			want:     map[string]float64{"10": 0.33333, "11": 0.33333}, quality: map[string]string{"11": models.EnergyQualityPartial}, state: StateOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kwh, quality, st := run(tt.cfg, tt.readings)
			for hour, want := range tt.want {
				assert.InDelta(t, want, kwh[hour], 0.001, "hour %s", hour)
			}
			for hour, v := range kwh {
				if _, ok := tt.want[hour]; !ok {
					assert.InDelta(t, 0, v, 0.001, "unexpected usage in hour %s", hour)
				}
			}
			for hour, want := range tt.quality {
				assert.Equal(t, want, quality[hour], "quality of hour %s", hour)
			}
			assert.Equal(t, tt.state, st.State)
		})
	}
}

func TestApplyIgnoresOldAndFutureReadings(t *testing.T) {
	cfg := MeterConfig{Measurement: models.EnergyMeasureEnergy, Unit: "kWh", GapLimit: time.Hour, MaxKW: 25}
	now := at("12:00")
	st, cs := Apply(cfg, MeterState{}, 5, now.Add(10*time.Minute), now, chicago, false)
	assert.Nil(t, st.Last)
	assert.Nil(t, cs)
	st, _ = Apply(cfg, MeterState{}, 5, now.Add(-25*time.Hour), now, chicago, false)
	assert.Nil(t, st.Last)
	st, _ = Apply(cfg, MeterState{}, 5, now.Add(-25*time.Hour), now, chicago, true)
	assert.NotNil(t, st.Last, "a fill accepts old readings")
}

func TestHourStartAcrossDaylightSaving(t *testing.T) {
	// 2026-11-01 01:00 to 02:00 happens twice in Chicago.
	first := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)  // 01:30 CDT
	second := time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC) // 01:30 CST
	assert.Equal(t, time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC), HourStart(first, chicago))
	assert.Equal(t, time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC), HourStart(second, chicago))
	kolkata := mustLoc("Asia/Kolkata")
	assert.Equal(t, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), HourStart(time.Date(2026, 11, 1, 6, 45, 0, 0, time.UTC), kolkata),
		"a half-hour zone's hours start on its own half hour")
	day := DayStart(first, chicago)
	assert.Equal(t, 25*time.Hour, AddDays(day, 1, chicago).Sub(day))
}

func TestUnavailablePower(t *testing.T) {
	cfg := MeterConfig{Measurement: models.EnergyMeasurePower, Unit: "W", GapLimit: time.Hour, MaxKW: 25}
	st := MeterState{Last: fp(1), LastAt: &[]time.Time{at("10:00")}[0]}
	st = Unavailable(cfg, st, "unavailable")
	assert.Nil(t, st.Last)
	assert.Equal(t, StateUnavailable, st.State)
	require.Equal(t, "unavailable", st.Note)
}

// TestMilliUnitsAreNotMegaUnits locks that a milliwatt reading is never read
// as megawatts. Units were lowercased before matching, so Home Assistant's
// "mW" and "mWh" matched "MW" and "MWh": a 500 mW sensor was offered as a
// power source and then recorded as 500 MW, half a million kWh an hour. The
// lowercase spellings people type for the supported units still match.
func TestMilliUnitsAreNotMegaUnits(t *testing.T) {
	tests := []struct {
		measurement, unit string
		factor            float64
		ok                bool
	}{
		{models.EnergyMeasurePower, "MW", 1000, true},
		{models.EnergyMeasurePower, "kw", 1, true},
		{models.EnergyMeasurePower, "mW", 0, false},
		{models.EnergyMeasureEnergy, "MWh", 1000, true},
		{models.EnergyMeasureEnergy, "wh", 0.001, true},
		{models.EnergyMeasureEnergy, " mWh ", 0, false},
	}
	for _, tc := range tests {
		factor, err := UnitFactor(tc.measurement, tc.unit)
		_, suggested := unitMeasurement(tc.unit)
		if !tc.ok {
			assert.Error(t, err, "%q is a thousandth of a watt and has no supported factor", tc.unit)
			assert.False(t, suggested, "a %q reading must not be offered as an energy source", tc.unit)
			continue
		}
		require.NoError(t, err, "%q is a supported unit", tc.unit)
		assert.Equal(t, tc.factor, factor, "%q converts to kW or kWh by this factor", tc.unit)
		assert.True(t, suggested, "a %q reading is offered as an energy source", tc.unit)
	}
}
