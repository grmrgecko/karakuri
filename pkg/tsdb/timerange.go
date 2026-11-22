package tsdb

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MaxSpan is the widest range any query or time range variable accepts.
const MaxSpan = 10 * 366 * 24 * time.Hour

// Default range ends.
const (
	DefaultFrom = "now-6h"
	DefaultTo   = "now"
)

// Range is a resolved time span.
type Range struct {
	From time.Time
	To   time.Time
}

// Span returns the range's length.
func (r Range) Span() time.Duration { return r.To.Sub(r.From) }

// ParseRange resolves two range ends against now. Empty ends take the
// defaults. The start must precede the end and the span be at most MaxSpan.
// Weeks start on Monday.
func ParseRange(from, to string, now time.Time) (Range, error) {
	return ParseRangeWeek(from, to, now, 1)
}

// ParseRangeWeek is ParseRange with weeks starting on weekStart, an ISO
// weekday from 1 for Monday to 7 for Sunday.
func ParseRangeWeek(from, to string, now time.Time, weekStart int) (Range, error) {
	if strings.TrimSpace(from) == "" {
		from = DefaultFrom
	}
	if strings.TrimSpace(to) == "" {
		to = DefaultTo
	}
	f, err := parseTime(from, now, false, weekStart)
	if err != nil {
		return Range{}, fmt.Errorf("range start: %w", err)
	}
	t, err := parseTime(to, now, true, weekStart)
	if err != nil {
		return Range{}, fmt.Errorf("range end: %w", err)
	}
	if !f.Before(t) {
		return Range{}, fmt.Errorf("range start %s is not before its end %s", from, to)
	}
	if t.Sub(f) > MaxSpan {
		return Range{}, fmt.Errorf("range is wider than 10 years")
	}
	return Range{From: f, To: t}, nil
}

// SplitRangeValue splits a time range variable value, "from,to", into its
// ends.
func SplitRangeValue(value string) (string, string, bool) {
	from, to, ok := strings.Cut(value, ",")
	if !ok {
		return "", "", false
	}
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	return from, to, from != "" && to != ""
}

// ValidRangeValue reports whether a time range variable value parses.
func ValidRangeValue(value string) bool {
	from, to, ok := SplitRangeValue(value)
	if !ok {
		return false
	}
	_, err := ParseRange(from, to, time.Now())
	return err == nil
}

// ParseTime resolves one range end. It accepts now with offsets and a
// rounding unit (now-6h, now-1d/d, now/w), Unix milliseconds, RFC 3339, and a
// bare date. A rounded end rounds down for a start and up to the end of the
// unit for an end, in the server's local time zone. Weeks start on Monday.
func ParseTime(expr string, now time.Time, isEnd bool) (time.Time, error) {
	return parseTime(expr, now, isEnd, 1)
}

func parseTime(expr string, now time.Time, isEnd bool, weekStart int) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if strings.HasPrefix(expr, "now") {
		return parseRelative(expr[3:], now, isEnd, weekStart)
	}
	if isDigits(expr) {
		ms, err := strconv.ParseInt(expr, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid Unix milliseconds %q", expr)
		}
		return time.UnixMilli(ms), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		var t time.Time
		var err error
		if layout == time.RFC3339Nano {
			t, err = time.Parse(layout, expr)
		} else {
			t, err = time.ParseInLocation(layout, expr, now.Location())
		}
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q: use now-6h, RFC 3339, or Unix milliseconds", expr)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseRelative applies the offsets and rounding after "now".
func parseRelative(rest string, now time.Time, isEnd bool, weekStart int) (time.Time, error) {
	t := now
	for rest != "" {
		op := rest[0]
		rest = rest[1:]
		switch op {
		case '+', '-':
			i := 0
			for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
				i++
			}
			if i == 0 || i == len(rest) {
				return time.Time{}, fmt.Errorf("invalid offset in now%c%s", op, rest)
			}
			n, _ := strconv.Atoi(rest[:i])
			unit, size := unitAt(rest[i:])
			if unit == "" {
				return time.Time{}, fmt.Errorf("unknown unit in %q", rest)
			}
			if op == '-' {
				n = -n
			}
			t = addUnit(t, n, unit)
			rest = rest[i+size:]
		case '/':
			unit, size := unitAt(rest)
			if unit == "" {
				return time.Time{}, fmt.Errorf("unknown rounding unit %q", rest)
			}
			t = roundUnit(t, unit, isEnd, weekStart)
			rest = rest[size:]
		default:
			return time.Time{}, fmt.Errorf("unexpected %q after now", string(op))
		}
	}
	return t, nil
}

// unitAt reads a time unit at the start of s, preferring ms over m.
func unitAt(s string) (string, int) {
	if strings.HasPrefix(s, "ms") {
		return "ms", 2
	}
	if s == "" {
		return "", 0
	}
	switch s[0] {
	case 's', 'm', 'h', 'd', 'w', 'M', 'y':
		return s[:1], 1
	}
	return "", 0
}

func addUnit(t time.Time, n int, unit string) time.Time {
	switch unit {
	case "ms":
		return t.Add(time.Duration(n) * time.Millisecond)
	case "s":
		return t.Add(time.Duration(n) * time.Second)
	case "m":
		return t.Add(time.Duration(n) * time.Minute)
	case "h":
		return t.Add(time.Duration(n) * time.Hour)
	case "d":
		return t.AddDate(0, 0, n)
	case "w":
		return t.AddDate(0, 0, 7*n)
	case "M":
		return addMonths(t, n)
	case "y":
		return addMonths(t, 12*n)
	}
	return t
}

// addMonths moves t by n calendar months. A day the target month lacks
// becomes its last day: AddDate would carry 31 September into October, and
// "now-1M/M" on the 31st would then select the current month.
func addMonths(t time.Time, n int) time.Time {
	moved := t.AddDate(0, n, 0)
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, n, 0)
	if moved.Month() == first.Month() {
		return moved
	}
	// Day 0 of the month after the target is the target's last day.
	return time.Date(first.Year(), first.Month()+1, 0, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// roundUnit rounds t to its unit, with weeks starting on weekStart, an ISO
// weekday.
func roundUnit(t time.Time, unit string, isEnd bool, weekStart int) time.Time {
	var start time.Time
	y, mo, d := t.Date()
	loc := t.Location()
	switch unit {
	case "ms", "s":
		start = t.Truncate(time.Second)
	case "m":
		// Minutes and hours subtract the clock fields below them rather than
		// rebuilding the time, which would pick the first instance of the
		// hour a daylight saving change repeats.
		start = t.Add(-time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
	case "h":
		start = t.Add(-time.Duration(t.Minute())*time.Minute - time.Duration(t.Second())*time.Second -
			time.Duration(t.Nanosecond()))
	case "d":
		start = time.Date(y, mo, d, 0, 0, 0, 0, loc)
	case "w":
		// Weekday counts Sunday as 0 and ISO counts it as 7; both are 0 mod 7.
		offset := (int(t.Weekday()) - weekStart + 14) % 7
		start = time.Date(y, mo, d-offset, 0, 0, 0, 0, loc)
	case "M":
		start = time.Date(y, mo, 1, 0, 0, 0, 0, loc)
	case "y":
		start = time.Date(y, 1, 1, 0, 0, 0, 0, loc)
	}
	if !isEnd {
		return start
	}
	return addUnit(start, 1, unit).Add(-time.Millisecond)
}
