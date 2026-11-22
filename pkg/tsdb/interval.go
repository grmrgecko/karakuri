package tsdb

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxDataPoints applies when a query names none.
const DefaultMaxDataPoints = 1000

// DefaultScrapeInterval is the scrape interval $__rate_interval assumes when
// the connection does not say.
const DefaultScrapeInterval = 15 * time.Second

// roundSteps is the interval rounding table: a raw interval up to the
// first value rounds to the second.
var roundSteps = []struct{ upTo, to time.Duration }{
	{10 * time.Millisecond, time.Millisecond},
	{15 * time.Millisecond, 10 * time.Millisecond},
	{35 * time.Millisecond, 20 * time.Millisecond},
	{75 * time.Millisecond, 50 * time.Millisecond},
	{150 * time.Millisecond, 100 * time.Millisecond},
	{350 * time.Millisecond, 200 * time.Millisecond},
	{750 * time.Millisecond, 500 * time.Millisecond},
	{1500 * time.Millisecond, time.Second},
	{3500 * time.Millisecond, 2 * time.Second},
	{7500 * time.Millisecond, 5 * time.Second},
	{12500 * time.Millisecond, 10 * time.Second},
	{17500 * time.Millisecond, 15 * time.Second},
	{25 * time.Second, 20 * time.Second},
	{45 * time.Second, 30 * time.Second},
	{90 * time.Second, time.Minute},
	{210 * time.Second, 2 * time.Minute},
	{450 * time.Second, 5 * time.Minute},
	{750 * time.Second, 10 * time.Minute},
	{1050 * time.Second, 15 * time.Minute},
	{1500 * time.Second, 20 * time.Minute},
	{2700 * time.Second, 30 * time.Minute},
	{5400 * time.Second, time.Hour},
	{9000 * time.Second, 2 * time.Hour},
	{16200 * time.Second, 3 * time.Hour},
	{32400 * time.Second, 6 * time.Hour},
	{24 * time.Hour, 12 * time.Hour},
	{7 * 24 * time.Hour, 24 * time.Hour},
	{21 * 24 * time.Hour, 7 * 24 * time.Hour},
}

// RoundInterval rounds a raw interval to a readable step.
func RoundInterval(d time.Duration) time.Duration {
	for _, s := range roundSteps {
		if d <= s.upTo {
			return s.to
		}
	}
	return 30 * 24 * time.Hour
}

// CalculateInterval chooses $__interval for a range: the span divided by the
// maximum data points, rounded, and never below min.
func CalculateInterval(r Range, maxDataPoints int, min time.Duration) time.Duration {
	if maxDataPoints <= 0 {
		maxDataPoints = DefaultMaxDataPoints
	}
	raw := r.Span() / time.Duration(maxDataPoints)
	interval := RoundInterval(raw)
	if interval < min {
		interval = min
	}
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	return interval
}

// RateInterval is the $__rate_interval macro's value: at least four scrapes, and at
// least one interval plus a scrape.
func RateInterval(interval, scrape time.Duration) time.Duration {
	if scrape <= 0 {
		scrape = DefaultScrapeInterval
	}
	a := interval + scrape
	b := 4 * scrape
	if a > b {
		return a
	}
	return b
}

// autoSteps are the intervals an interval variable's auto option chooses
// among.
var autoSteps = []time.Duration{
	time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
	time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour,
}

// AutoInterval resolves an interval variable's auto option: the nearest step
// at or above span/steps, and never below min.
func AutoInterval(span time.Duration, steps int, min time.Duration) time.Duration {
	if steps <= 0 {
		steps = 30
	}
	want := span / time.Duration(steps)
	if want < min {
		want = min
	}
	for _, s := range autoSteps {
		if s >= want {
			return s
		}
	}
	return autoSteps[len(autoSteps)-1]
}

// ParseDuration reads durations in the units dashboards use: ms, s, m, h, d,
// w, and y, combined as in 1h30m. A bare number is seconds.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if isDigits(s) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * time.Second, nil
	}
	var total time.Duration
	rest := s
	for rest != "" {
		i := 0
		for i < len(rest) && (rest[i] >= '0' && rest[i] <= '9' || rest[i] == '.') {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		n, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		unit, size := unitAt(rest[i:])
		var per time.Duration
		switch unit {
		case "ms":
			per = time.Millisecond
		case "s":
			per = time.Second
		case "m":
			per = time.Minute
		case "h":
			per = time.Hour
		case "d":
			per = 24 * time.Hour
		case "w":
			per = 7 * 24 * time.Hour
		case "y":
			per = 365 * 24 * time.Hour
		default:
			return 0, fmt.Errorf("invalid duration unit in %q", s)
		}
		total += time.Duration(n * float64(per))
		rest = rest[i+size:]
	}
	return total, nil
}

// FormatDuration writes d in the largest whole unit, the form Prometheus,
// InfluxQL, and Flux all accept (15s, 5m, 1h, 1d). Durations that are not a
// whole number of any larger unit fall back to milliseconds.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	units := []struct {
		suffix string
		size   time.Duration
	}{
		{"w", 7 * 24 * time.Hour},
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
		{"ms", time.Millisecond},
	}
	for _, u := range units {
		if d%u.size == 0 {
			return strconv.FormatInt(int64(d/u.size), 10) + u.suffix
		}
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}
