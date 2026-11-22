package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/sunevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimeWindowCivilBoundaries preserves weekday ownership and both occurrences
// of the repeated America/Chicago hour on the 2026 daylight saving transition.
func TestTimeWindowCivilBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, config, at, port string
		invalid                bool
	}{
		{"start included", `{}`, "2026-10-06T08:00:00Z", "inside", false},
		{"end excluded", `{}`, "2026-10-06T18:00:00Z", "outside", false},
		{"Friday night", `{"weekdays":"friday","start_time":"22:00","end_time":"06:00"}`, "2026-10-10T01:00:00Z", "inside", false},
		{"Saturday night excluded", `{"weekdays":"friday","start_time":"22:00","end_time":"06:00"}`, "2026-10-11T01:00:00Z", "outside", false},
		{"first repeated hour", `{"location":"America/Chicago","start_time":"01:15","end_time":"01:45"}`, "2026-11-01T01:30:00-05:00", "inside", false},
		{"second repeated hour", `{"location":"America/Chicago","start_time":"01:15","end_time":"01:45"}`, "2026-11-01T01:30:00-06:00", "inside", false},
		{"spring gap", `{"location":"America/Chicago","start_time":"02:30","end_time":"04:00"}`, "2026-03-08T03:00:00-05:00", "inside", false},
		{"unknown day", `{"weekdays":"funday"}`, "2026-10-06T12:00:00Z", "", true},
		{"equal endpoints", `{"start_time":"08:00","end_time":"08:00"}`, "2026-10-06T12:00:00Z", "", true},
		{"invalid clock", `{"start_time":"24:01"}`, "2026-10-06T12:00:00Z", "", true},
		{"non-finite timestamp", `{}`, "Infinity", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.config), &cfg))
			if cfg["location"] == nil {
				cfg["location"] = "UTC"
			}
			cfg["time"] = "payload.at"
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			in := &engine.Msg{Payload: map[string]any{"at": tc.at}, Topic: "motion", Meta: map[string]any{"request_id": "r"}, Group: []engine.GroupFrame{{ID: "g", Count: 2}}}
			out, err := (TimeWindowNode{}).Execute(t.Context(), newTestCtx(string(raw), nil), map[string]*engine.Msg{"in": in})
			if tc.invalid {
				assert.Error(t, err, "invalid settings must fail rather than classify the event")
				assert.Empty(t, out)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, in, only(out, tc.port), "classification must preserve the complete message")
			assert.Len(t, out, 1, "one event must select exactly one branch")
		})
	}
}

// TestTimeWindowSolarDates checks solar UTC dates against the local date at the
// date line and preserves the normal no-event result during polar night.
func TestTimeWindowSolarDates(t *testing.T) {
	for _, tc := range []struct {
		name, zone string
		lat, lon   float64
		date       string
	}{
		{"Chicago", "America/Chicago", 41.88, -87.63, "2026-10-06"},
		{"Auckland", "Pacific/Auckland", -36.85, 174.76, "2026-10-06"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			date, err := time.Parse("2006-01-02", tc.date)
			require.NoError(t, err)
			boundary := windowBoundary{kind: "sun", event: sunevent.Sunrise, offset: -30 * time.Minute}
			civil, found, err := boundary.on(date, loc, tc.lat, tc.lon)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, tc.date, civil.Format("2006-01-02"), "sunrise must belong to the requested local date")
			assert.Less(t, civil.Hour(), 12, "sunrise must resolve to the morning rather than the neighboring UTC day's event")
			boundary.offset = -12 * time.Hour
			civil, found, err = boundary.on(date, loc, tc.lat, tc.lon)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, date.AddDate(0, 0, -1).Format("2006-01-02"), civil.Format("2006-01-02"), "the maximum negative offset moves this morning event into the preceding date")
			at := time.Date(civil.Year(), civil.Month(), civil.Day(), civil.Hour(), civil.Minute(), civil.Second(), civil.Nanosecond(), loc)
			cfg, err := json.Marshal(map[string]any{
				"location": tc.zone, "weekdays": "tuesday", "start_type": "sun", "start_offset": "-12h",
				"latitude": tc.lat, "longitude": tc.lon,
			})
			require.NoError(t, err)
			in := &engine.Msg{Payload: "motion"}
			out, err := (TimeWindowNode{}).Execute(t.Context(), newTestCtx(string(cfg), engine.NewFakeClock(at)), map[string]*engine.Msg{"in": in})
			require.NoError(t, err)
			assert.Equal(t, in, only(out, "inside"), "a Tuesday solar window shifted into Monday keeps Tuesday ownership and includes its start")
		})
	}
	ec := newTestCtx(`{"start_type":"sun","start_event":"sunrise","end_type":"sun","end_event":"sunset","latitude":80,"longitude":0,"location":"UTC"}`, engine.NewFakeClock(time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC)))
	in := &engine.Msg{Payload: "motion", Meta: map[string]any{"request_id": "r"}}
	out, err := (TimeWindowNode{}).Execute(t.Context(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err)
	assert.Equal(t, "no_sun_event", only(out, "outside").Meta["time_window_reason"], "polar night has no invented sunrise")
	assert.NotContains(t, in.Meta, "time_window_reason", "diagnostic metadata must not mutate the recorded input")
}
