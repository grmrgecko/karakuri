package dashchart

import (
	"context"
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRangeShiftsAnOverrideWithOneEnd locks that a time shift moves both ends
// of a zoom that names only one. The missing end takes its default when the
// range is parsed, which was after the shift: with a start of now-1h and no
// end, a chart shifted by a day got "now-1h-1d" to "now", 25 hours ending
// now, instead of the same hour a day earlier.
func TestRangeShiftsAnOverrideWithOneEnd(t *testing.T) {
	tests := []struct {
		name, from, to   string
		wantFrom, wantTo string
	}{
		{"both ends", "now-1h", "now", "now-1h-1d", "now-1d"},
		{"start only", "now-1h", "", "now-1h-1d", "now-1d"},
		{"end only", "", "now-1h", "now-6h-1d", "now-1h-1d"},
	}
	for _, tc := range tests {
		from, to, err := Range(&Config{TimeShift: "1d"}, nil, tc.from, tc.to)
		require.NoError(t, err, "%s: the override is a valid range", tc.name)
		assert.Equal(t, tc.wantFrom, from, "%s: the start moves back a day", tc.name)
		assert.Equal(t, tc.wantTo, to, "%s: the end moves back a day", tc.name)
	}
}

// TestRangeZoomReplacesRelativeTime locks that a zoom is read as sent on a
// chart with a relative time. The chart widget draws its axis over the
// dragged span and skips the relative time while zoomed, but the relative
// time replaced the override here, so every zoom on such a chart answered
// with the last span ending now. Without a zoom the relative time still
// replaces the configured range.
func TestRangeZoomReplacesRelativeTime(t *testing.T) {
	tests := []struct {
		name, from, to   string
		wantFrom, wantTo string
	}{
		{"zoomed", "1700000000000", "1700000600000", "1700000000000", "1700000600000"},
		{"not zoomed", "", "", "now-1h", "now"},
	}
	for _, tc := range tests {
		from, to, err := Range(&Config{TimeRange: "now-7d,now", RelativeTime: "1h"}, nil, tc.from, tc.to)
		require.NoError(t, err, "%s: the range is valid", tc.name)
		assert.Equal(t, tc.wantFrom, from, "%s: the start", tc.name)
		assert.Equal(t, tc.wantTo, to, "%s: the end", tc.name)
	}
}

// TestEnergyChartRangeFollowsEnergyZone locks that the range an energy chart
// reports is the one its queries ran over. The energy connection rounds
// "now/d" to a day of the energy time zone, and the response's range, which
// the widget draws its axis over, was rounded in the server's zone instead.
// On a server in UTC with the house in Chicago the axis ran from 18:00 to
// 18:00 and cut off the evening. Kiritimati is UTC+14 with no daylight
// saving, so its midnight is no other zone's.
func TestEnergyChartRangeFollowsEnergyZone(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "Pacific/Kiritimati", WeekStart: 1}))
	t.Cleanup(func() { _ = energy.SaveSettings(energy.Settings{WeekStart: 1}) })
	orig := Querier
	t.Cleanup(func() { Querier = orig })
	var ran tsdb.Range
	Querier = func(_ context.Context, _ *tsdbops.Source, p *tsdbops.Prepared) (*tsdb.Result, error) {
		ran = p.Request.Range
		return &tsdb.Result{Series: []tsdb.Series{}}, nil
	}

	cfg := &Config{TimeRange: "now/d,now/d", Queries: []Query{{RefID: "A", ConnectionID: tsdb.EnergyConnection, Language: tsdb.LangEnergy}}}
	resp, err := Run(context.Background(), cfg, nil, Request{})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)
	require.Nil(t, resp.Results[0].Error)

	assert.Equal(t, ran.From.UnixMilli(), resp.Range.From, "the axis starts where the energy day does")
	assert.Equal(t, ran.To.UnixMilli(), resp.Range.To, "the axis ends where the energy day does")
}
