package tsdb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseTime pins the relative time grammar range variables
// and chart queries share: offsets chain, a rounding unit rounds a
// start down and an end up to the last millisecond of the unit, and weeks
// start on Monday.
func TestParseTime(t *testing.T) {
	loc := time.UTC
	// Thursday 2026-10-29 15:42:10 UTC.
	now := time.Date(2026, 10, 29, 15, 42, 10, 0, loc)
	cases := []struct {
		expr  string
		isEnd bool
		want  time.Time
	}{
		{"now", false, now},
		{"now-6h", false, now.Add(-6 * time.Hour)},
		{"now-1d/d", false, time.Date(2026, 10, 28, 0, 0, 0, 0, loc)},
		{"now-1d/d", true, time.Date(2026, 10, 28, 23, 59, 59, 999e6, loc)},
		{"now/w", false, time.Date(2026, 10, 26, 0, 0, 0, 0, loc)},
		{"now-2M+3d", false, time.Date(2026, 9, 1, 15, 42, 10, 0, loc)},
		{"now-90ms", false, now.Add(-90 * time.Millisecond)},
		{"1730000000000", false, time.UnixMilli(1730000000000)},
		{"2026-10-01T00:00:00Z", false, time.Date(2026, 10, 1, 0, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		got, err := ParseTime(tc.expr, now, tc.isEnd)
		require.NoError(t, err, "parse %s", tc.expr)
		assert.True(t, tc.want.Equal(got), "%s (end=%v): got %s, want %s", tc.expr, tc.isEnd, got, tc.want)
	}

	_, err := ParseRange("now", "now-1h", now)
	assert.Error(t, err, "a start after its end must be refused")
	_, err = ParseRange("now-11y", "now", now)
	assert.Error(t, err, "a span over ten years must be refused")
	r, err := ParseRange("", "", now)
	require.NoError(t, err)
	assert.Equal(t, 6*time.Hour, r.Span(), "empty ends default to the last six hours")
}

// TestParseTimeMonthOffsetsStayInTheTargetMonth locks that a month or year
// offset from a day the target month lacks lands on that month's last day.
// The offset used to overflow into the month after: on 31 October "now-1M"
// was 31 September, which is 1 October, so "now-1M/M" selected October and a
// "last month" chart showed this month on the 29th to 31st. A day the target
// month has is kept.
func TestParseTimeMonthOffsetsStayInTheTargetMonth(t *testing.T) {
	loc := time.UTC
	end := time.Date(2026, 10, 31, 12, 0, 0, 0, loc)
	leap := time.Date(2028, 2, 29, 12, 0, 0, 0, loc)
	cases := []struct {
		name  string
		now   time.Time
		expr  string
		isEnd bool
		want  time.Time
	}{
		{"a month back from the 31st", end, "now-1M", false, time.Date(2026, 9, 30, 12, 0, 0, 0, loc)},
		{"last month starts", end, "now-1M/M", false, time.Date(2026, 9, 1, 0, 0, 0, 0, loc)},
		{"last month ends", end, "now-1M/M", true, time.Date(2026, 9, 30, 23, 59, 59, 999e6, loc)},
		{"months ahead into February", end, "now+4M", false, time.Date(2027, 2, 28, 12, 0, 0, 0, loc)},
		{"a year back from a leap day", leap, "now-1y", false, time.Date(2027, 2, 28, 12, 0, 0, 0, loc)},
		{"a day the target month has", end.AddDate(0, 0, -1), "now-1M", false, time.Date(2026, 9, 30, 12, 0, 0, 0, loc)},
		{"across a year end", time.Date(2027, 1, 15, 12, 0, 0, 0, loc), "now-2M", false, time.Date(2026, 11, 15, 12, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		got, err := ParseTime(tc.expr, tc.now, tc.isEnd)
		require.NoError(t, err, "parse %s", tc.expr)
		assert.True(t, tc.want.Equal(got), "%s: %s (end=%v) from %s: got %s, want %s", tc.name, tc.expr, tc.isEnd, tc.now, got, tc.want)
	}
}

// TestParseTimeRepeatedHour pins hour and minute rounding inside the hour a
// daylight saving change repeats: the second 01:30 of the night rounds to the
// second 01:00, not the first. Rebuilding the time from its clock fields picks
// the first instance, which put the start an hour early and the rounded end
// before now.
func TestParseTimeRepeatedHour(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	// 2026-11-01 01:30 EST, the second pass through 01:30 that night.
	now := time.Date(2026, 11, 1, 6, 30, 20, 0, time.UTC).In(loc)
	cases := []struct {
		expr  string
		isEnd bool
		want  time.Time
	}{
		{"now/h", false, time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)},
		{"now/h", true, time.Date(2026, 11, 1, 6, 59, 59, 999e6, time.UTC)},
		{"now/m", false, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)},
		{"now/m", true, time.Date(2026, 11, 1, 6, 30, 59, 999e6, time.UTC)},
	}
	for _, tc := range cases {
		got, err := ParseTime(tc.expr, now, tc.isEnd)
		require.NoError(t, err, "parse %s", tc.expr)
		assert.True(t, tc.want.Equal(got), "%s (end=%v) must round within the second instance of the hour: got %s, want %s",
			tc.expr, tc.isEnd, got.UTC(), tc.want)
	}
}

// TestCalculateInterval pins the rounding table, which decides the
// step a chart and a PromQL range query use: 6h over 1000 points is 21.6s,
// which rounds to 20s; 24h over 100 points is 864s, which rounds to 15m.
func TestCalculateInterval(t *testing.T) {
	now := time.Now()
	r := Range{From: now.Add(-6 * time.Hour), To: now}
	assert.Equal(t, 20*time.Second, CalculateInterval(r, 1000, 0))
	r = Range{From: now.Add(-24 * time.Hour), To: now}
	assert.Equal(t, 15*time.Minute, CalculateInterval(r, 100, 0))
	assert.Equal(t, time.Hour, CalculateInterval(r, 100, time.Hour), "the minimum interval wins over a finer step")
	assert.Equal(t, 60*time.Second, RateInterval(20*time.Second, 15*time.Second),
		"rate interval is max(20s+15s, 4*15s) = 60s")
	assert.Equal(t, 6*time.Hour, AutoInterval(7*24*time.Hour, 30, 0),
		"7 days over 30 steps is 5.6h, and the next auto step is 6h")
}

// TestExpandMacros pins the expansions each language's syntax requires:
// InfluxQL compares time with ms literals, SQL filters with quoted UTC
// timestamps, Flux takes RFC 3339 times, and durations use whole units.
func TestExpandMacros(t *testing.T) {
	r := Range{From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700021600000)}
	assert.Equal(t,
		`SELECT mean("value") FROM "t" WHERE time >= 1700000000000ms and time <= 1700021600000ms GROUP BY time(20s)`,
		ExpandMacros(`SELECT mean("value") FROM "t" WHERE $timeFilter GROUP BY time($__interval)`, LangInfluxQL, r, 20*time.Second, 0))
	assert.Equal(t,
		`SELECT to_timestamp(floor(extract(epoch from time)/300)*300) AS "time", avg(v) FROM h WHERE time BETWEEN '2023-11-14T22:13:20.000Z' AND '2023-11-15T04:13:20.000Z'`,
		ExpandMacros(`SELECT $__timeGroupAlias(time, '5m'), avg(v) FROM h WHERE $__timeFilter(time)`, LangSQL, r, 20*time.Second, 0))
	assert.Equal(t,
		`range(start: 2023-11-14T22:13:20Z, stop: 2023-11-15T04:13:20Z) |> aggregateWindow(every: 1m)`,
		ExpandMacros(`range(start: v.timeRangeStart, stop: v.timeRangeStop) |> aggregateWindow(every: v.windowPeriod)`, LangFlux, r, time.Minute, 0))
	assert.Equal(t, `rate(x[1m]) 6h 21600 20000`,
		ExpandMacros(`rate(x[$__rate_interval]) $__range $__range_s $__interval_ms`, LangPromQL, r, 20*time.Second, time.Minute))
}

// TestEscapeValue pins that a substituted value cannot close the string
// literal it is placed in, per each language's quoting rules.
func TestEscapeValue(t *testing.T) {
	v := `a"b'c\d`
	assert.Equal(t, `a\"b'c\\d`, EscapeValue(LangPromQL, v))
	assert.Equal(t, `a"b\'c\\d`, EscapeValue(LangInfluxQL, v))
	assert.Equal(t, `a"b''c\d`, EscapeValue(LangSQL, v))
	assert.Equal(t, `'it''s'`, SQLString("it's"))
}
