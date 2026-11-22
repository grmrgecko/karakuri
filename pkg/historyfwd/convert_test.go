package historyfwd

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShapePerDestination pins spec 027 FR-016 and FR-017, the contract a
// chart query is written against: every attribute of a composite
// control is its own series labelled attribute; a select becomes a state-set
// of 0/1 series labelled state for numeric-only databases and a text state
// for the others; text is dropped for numeric-only databases.
func TestShapePerDestination(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000)
	light := controls.Change{At: at, Control: controls.Snapshot{
		ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "Lamp", Kind: controls.KindLight,
		Value: map[string]any{"on": true, "brightness": 40.0, "color": "#ff0000"},
	}}
	samples := controlSamples(light)
	byAttr := map[string]sample{}
	for _, s := range samples {
		byAttr[s.labels["attribute"]] = s
	}
	require.Contains(t, byAttr, "brightness")
	assert.Equal(t, map[string]string{"id": "11111111-1111-1111-1111-111111111111", "name": "Lamp", "attribute": "brightness"}, byAttr["brightness"].labels)

	on := shape(byAttr["on"], metricControl, nil, true, "")
	require.Len(t, on, 1)
	assert.Equal(t, 1.0, *on[0].Value, "a boolean is 1 or 0")
	assert.Empty(t, shape(byAttr["color"], metricControl, nil, true, ""), "a hex colour has no numeric form")
	assert.Equal(t, "#ff0000", *shape(byAttr["color"], metricControl, nil, false, "")[0].State)

	sel := controlSamples(controls.Change{At: at, Control: controls.Snapshot{
		ID: uuid.New(), Name: "Mode", Kind: controls.KindSelect, Value: "cool",
		Options: map[string]any{"options": []any{"heat", map[string]any{"value": "cool"}}},
	}})
	require.Len(t, sel, 1)
	points := shape(sel[0], metricControl, map[string]string{"site": "home"}, true, "")
	got := map[string]float64{}
	for _, p := range points {
		got[p.Labels["state"]] = *p.Value
		assert.Equal(t, "home", p.Labels["site"], "forward labels are added to every point")
		assert.Equal(t, int64(1_700_000_000_000), p.Time, "points carry the time of the change")
	}
	assert.Equal(t, map[string]float64{"heat": 0, "cool": 1}, got)
	text := shape(sel[0], metricControl, nil, false, "")
	assert.Equal(t, []tsdb.Point{{Time: 1_700_000_000_000, Metric: metricControl, Labels: sel[0].labels, State: tsdb.Text("cool")}}, text)
}

// TestLocationSamples pins how a location reaches a time-series database:
// its state is an enumeration over the control's zones, so a numeric-only
// destination records presence as 0/1 series; coordinates are numbers; and
// since is dropped, because it repeats the time of the state sample.
func TestLocationSamples(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000)
	samples := controlSamples(controls.Change{At: at, Control: controls.Snapshot{
		ID: uuid.New(), Name: "James", Kind: controls.KindLocation,
		Value: map[string]any{"state": "home", "latitude": 41.88, "since": "2026-10-31T14:02:11Z"},
		Options: map[string]any{"zones": []any{
			map[string]any{"value": "home", "label": "Home"},
			map[string]any{"value": "not_home", "label": "Away"},
		}},
	}})
	byAttr := map[string]sample{}
	for _, s := range samples {
		byAttr[s.labels["attribute"]] = s
	}
	assert.NotContains(t, byAttr, "since")
	require.Contains(t, byAttr, "state")
	got := map[string]float64{}
	for _, p := range shape(byAttr["state"], metricControl, nil, true, "") {
		got[p.Labels["state"]] = *p.Value
	}
	assert.Equal(t, map[string]float64{"home": 1, "not_home": 0}, got)
	lat := shape(byAttr["latitude"], metricControl, nil, true, "")
	require.Len(t, lat, 1)
	assert.Equal(t, 41.88, *lat[0].Value)
}

// TestDeviceAvailabilitySample pins availability as a 0/1 series beside a
// device's values, with unknown left out because it says nothing about the
// device.
func TestDeviceAvailabilitySample(t *testing.T) {
	online, ok := availabilityValue("online")
	require.True(t, ok)
	_, ok = availabilityValue("unknown")
	assert.False(t, ok, "unknown records nothing")

	samples := deviceSamples("d1", "Plug", "", map[string]any{"availability": online}, time.UnixMilli(1_700_000_000_000))
	require.Len(t, samples, 1)
	assert.Equal(t, "availability", samples[0].labels["attribute"])
	points := shape(samples[0], metricDevice, nil, true, "")
	require.Len(t, points, 1)
	assert.Equal(t, 1.0, *points[0].Value)
}

// TestDateTimeSamples locks the number a date and time control records. A
// timestamp is Unix seconds. A time-only control has no date, and it was
// parsed as a time on 1 January of year 0 in the server's zone, which
// recorded about -62 billion, shifted by that zone's local mean time. It
// records seconds since midnight instead, the same in every zone.
func TestDateTimeSamples(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  float64
	}{
		{"timestamp", "2026-11-02T07:30:00Z", float64(time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC).Unix())},
		{"time of day", "07:30:00", 7*3600 + 30*60},
		{"time of day without seconds", "23:59", 23*3600 + 59*60},
		{"midnight", "00:00:00", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := controlSamples(controls.Change{At: time.UnixMilli(1_700_000_000_000), Control: controls.Snapshot{
				ID: uuid.New(), Name: "Alarm", Kind: controls.KindDateTime, Value: tt.value,
			}})
			require.Len(t, samples, 1, "a date and time control has one recordable value")
			assert.Equal(t, valNumber, samples[0].kind, "a date and time value is recorded as a number")
			assert.Equal(t, tt.want, samples[0].num, "%q must record a number a chart can read", tt.value)
		})
	}
}
