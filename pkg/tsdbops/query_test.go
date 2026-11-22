package tsdbops

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nullQuerier is a source that is never asked anything; Prepare only reads
// its optional interfaces.
type nullQuerier struct{}

func (nullQuerier) Query(context.Context, tsdb.Request) (*tsdb.Result, error) { return nil, nil }
func (nullQuerier) Metadata(context.Context, tsdb.MetaRequest) ([]string, error) {
	return nil, nil
}

// TestPrepareKeepsARoundedStart locks that aligning a range to its interval
// never moves a calendar-rounded start. "now/M" is the first instant of the
// month in the caller's zone, and it is the same for every caller already. In
// a +01:00 zone that instant is 23:00Z; with the 2h interval a month at 300
// points gets (468h / 300 = 93.6m, rounded to 2h), truncating on the UTC grid
// gave 22:00Z, which is 23:00 on 31 October, and "this month" counted an hour
// of last month. A sliding start is still aligned so callers share a request.
func TestPrepareKeepsARoundedStart(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 20, 12, 0, 30, 0, zone)
	src := &Source{Connection: &models.Connection{}, Querier: nullQuerier{}}

	month, err := PrepareWith(src, tsdb.Query{Language: tsdb.LangEnergy, From: "now/M", To: "now", MaxDataPoints: 300}, now, nil)
	require.NoError(t, err, "a month to date is a valid range")
	require.Equal(t, 2*time.Hour, month.Request.Interval, "the case rests on an interval wider than the zone offset")
	assert.True(t, month.Request.Range.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, zone)),
		"the month starts at local midnight on the 1st, got %s", month.Request.Range.From.In(zone))

	sliding, err := PrepareWith(src, tsdb.Query{Language: tsdb.LangPromQL, From: "now-6h", To: "now", MaxDataPoints: 360}, now, nil)
	require.NoError(t, err, "a sliding window is a valid range")
	assert.Zero(t, sliding.Request.Range.From.Unix()%int64(sliding.Request.Interval/time.Second),
		"a sliding start is aligned to the interval so nearby callers share one request")
}

// TestPrepareKeepsCalendarEndsAndAbsoluteStarts locks that aligning a range
// to its interval moves only ends that slide with the clock. A calendar end
// ("now-1M/M") and an absolute start are the same for every caller already,
// and both are boundaries in the caller's zone. In a +01:00 zone last month
// ends at 22:59:59.999Z; rounding that up on a 2h UTC grid gave 00:00Z, which
// is 01:00 on 1 November, so "last month" counted this month's first hour. An
// absolute start of 1 November was pulled back to 23:00 on 31 October the
// same way.
func TestPrepareKeepsCalendarEndsAndAbsoluteStarts(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 20, 12, 0, 30, 0, zone)
	src := &Source{Connection: &models.Connection{}, Querier: nullQuerier{}}
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, zone)

	last, err := PrepareWith(src, tsdb.Query{Language: tsdb.LangEnergy, From: "now-1M/M", To: "now-1M/M", MaxDataPoints: 300}, now, nil)
	require.NoError(t, err, "last month is a valid range")
	require.Equal(t, 2*time.Hour, last.Request.Interval, "the case rests on an interval wider than the zone offset")
	assert.True(t, last.Request.Range.To.Before(november),
		"last month ends before local midnight on the 1st, got %s", last.Request.Range.To.In(zone))

	since, err := PrepareWith(src, tsdb.Query{Language: tsdb.LangEnergy, From: "2026-11-01", To: "now", MaxDataPoints: 300}, now, nil)
	require.NoError(t, err, "a date to now is a valid range")
	require.Equal(t, 2*time.Hour, since.Request.Interval, "the case rests on an interval wider than the zone offset")
	assert.True(t, since.Request.Range.From.Equal(november),
		"an absolute start stays where it was written, got %s", since.Request.Range.From.In(zone))
	assert.Zero(t, since.Request.Range.To.Unix()%int64(since.Request.Interval/time.Second),
		"a sliding end is still rounded up to the interval so nearby callers share one request")
	assert.False(t, since.Request.Range.To.Before(now), "a sliding end never rounds below now")
}

// TestPrepareRoundsEnergyRangesInTheEnergyZone locks that a calendar range on
// the energy connection is rounded in the energy time zone, which is where
// its days and months start. The server's zone was used, so with the server
// on UTC and the house in Tokyo, "today" at 05:00 Tokyo time began at 09:00
// the day before and the chart drew two partial days. Other connections keep
// the caller's zone.
func TestPrepareRoundsEnergyRangesInTheEnergyZone(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(models.All()...))
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "Asia/Tokyo", WeekStart: 1}))
	t.Cleanup(func() { _ = energy.SaveSettings(energy.Settings{WeekStart: 1}) })
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	now := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)

	house := &Source{Connection: &models.Connection{Kind: tsdb.EnergyConnection}, Querier: nullQuerier{}}
	today, err := PrepareWith(house, tsdb.Query{Language: tsdb.LangEnergy, From: "now/d", To: "now"}, now, nil)
	require.NoError(t, err, "today so far is a valid range")
	assert.True(t, today.Request.Range.From.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, tokyo)),
		"an energy day starts at midnight in the energy zone, got %s", today.Request.Range.From.In(tokyo))

	other := &Source{Connection: &models.Connection{Kind: "prometheus"}, Querier: nullQuerier{}}
	plain, err := PrepareWith(other, tsdb.Query{Language: tsdb.LangPromQL, From: "now/d", To: "now"}, now, nil)
	require.NoError(t, err, "today so far is a valid range")
	assert.True(t, plain.Request.Range.From.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)),
		"another connection's day starts at midnight in the caller's zone, got %s", plain.Request.Range.From)
}

// TestHAHistoryAcceptsTheLast31Days locks that the documented history limit
// of 31 days is reachable with a relative range. Prepare aligns a sliding
// range outward to the query interval, so "now-31d" to "now" arrived 30
// minutes wider than 31 days and was refused as too long; only 30 days and
// under ever ran. The request must get past the limit to Home Assistant,
// which here is a driver that was never connected, and a 32-day range must
// still be refused before it is sent.
func TestHAHistoryAcceptsTheLast31Days(t *testing.T) {
	d, err := homeassistant.New(integrations.ConnModel{ID: uuid.New(), Config: []byte(`{"base_url":"http://127.0.0.1:1","token":"t"}`)}, integrations.Deps{})
	require.NoError(t, err, "a driver builds from a complete config without connecting")
	q := haQuerier{d.(*homeassistant.Driver)}
	src := &Source{Connection: &models.Connection{Kind: homeassistant.Kind}, Querier: q}
	now := time.Date(2026, 11, 20, 12, 7, 30, 0, time.UTC)

	tests := []struct {
		from  string
		class string
	}{
		{"now-31d", tsdb.ClassUnavailable},
		{"now-32d", tsdb.ClassRejected},
	}
	for _, tc := range tests {
		p, err := PrepareWith(src, tsdb.Query{Language: tsdb.LangHAHistory, From: tc.from, To: "now",
			HA: &tsdb.HAQuery{Entities: []string{"sensor.outside"}}}, now, nil)
		require.NoError(t, err, "%s to now is a valid range", tc.from)
		_, err = q.Query(context.Background(), p.Request)
		var te *tsdb.Error
		require.ErrorAs(t, err, &te, "an unconnected driver or an over-long range both answer a classified error")
		assert.Equal(t, tc.class, te.Class, "%s to now: %s", tc.from, te.Message)
	}
}

// TestPrepareStartsEnergyWeeksOnTheConfiguredDay locks that "now/w" on the
// energy connection starts on the energy week start, as energy rollups and
// the Energy Usage node do. With weeks starting on Sunday and today Wednesday
// 18 November 2026, the week began Sunday the 15th; Monday the 16th left the
// preset's "This week" tile a day short. Other connections keep Monday.
func TestPrepareStartsEnergyWeeksOnTheConfiguredDay(t *testing.T) {
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, db.DB.AutoMigrate(models.All()...))
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "UTC", WeekStart: 7}))
	t.Cleanup(func() { _ = energy.SaveSettings(energy.Settings{WeekStart: 1}) })
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	house := &Source{Connection: &models.Connection{Kind: tsdb.EnergyConnection}, Querier: nullQuerier{}}
	week, err := PrepareWith(house, tsdb.Query{Language: tsdb.LangEnergy, From: "now/w", To: "now"}, now, nil)
	require.NoError(t, err, "this week so far is a valid range")
	assert.True(t, week.Request.Range.From.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)),
		"an energy week starts on the configured Sunday, got %s", week.Request.Range.From)

	other := &Source{Connection: &models.Connection{Kind: "prometheus"}, Querier: nullQuerier{}}
	plain, err := PrepareWith(other, tsdb.Query{Language: tsdb.LangPromQL, From: "now/w", To: "now"}, now, nil)
	require.NoError(t, err, "this week so far is a valid range")
	assert.True(t, plain.Request.Range.From.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)),
		"another connection's week starts on Monday, got %s", plain.Request.Range.From)
}
