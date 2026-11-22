package tsdb

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	sqlTimeFilter    = regexp.MustCompile(`\$__timeFilter\(\s*([^)]+?)\s*\)`)
	sqlTimeGroup     = regexp.MustCompile(`\$__timeGroup(Alias)?\(\s*([^,)]+?)\s*,\s*([^)]+?)\s*\)`)
	sqlUnixFilter    = regexp.MustCompile(`\$__unixEpochFilter\(\s*([^)]+?)\s*\)`)
	sqlTimeFromTo    = regexp.MustCompile(`\$__time(From|To)\(\s*\)`)
	sqlUnixFromTo    = regexp.MustCompile(`\$__unixEpoch(From|To)\(\s*\)`)
	durationMacroRef = regexp.MustCompile(`\$__(interval_ms|interval|range_ms|range_s|range|rate_interval|from|to)\b`)
)

// ExpandMacros replaces the time macros a query may use with values for its
// range and interval. Every language gets $__from, $__to (Unix milliseconds),
// $__interval, $__interval_ms, $__range, $__range_s, $__range_ms, and
// $__rate_interval. SQL adds $__timeFilter, $__timeGroup, $__timeGroupAlias,
// $__timeFrom, $__timeTo, and their Unix epoch forms; InfluxQL adds
// $timeFilter; Flux replaces v.timeRangeStart, v.timeRangeStop, and
// v.windowPeriod.
func ExpandMacros(text, lang string, r Range, interval, rate time.Duration) string {
	switch lang {
	case LangSQL:
		text = sqlTimeFilter.ReplaceAllStringFunc(text, func(m string) string {
			col := sqlTimeFilter.FindStringSubmatch(m)[1]
			return col + " BETWEEN '" + sqlTime(r.From) + "' AND '" + sqlTime(r.To) + "'"
		})
		text = sqlUnixFilter.ReplaceAllStringFunc(text, func(m string) string {
			col := sqlUnixFilter.FindStringSubmatch(m)[1]
			return col + " >= " + strconv.FormatInt(r.From.Unix(), 10) + " AND " + col + " <= " + strconv.FormatInt(r.To.Unix(), 10)
		})
		text = sqlTimeGroup.ReplaceAllStringFunc(text, func(m string) string {
			parts := sqlTimeGroup.FindStringSubmatch(m)
			step := interval
			arg := strings.Trim(parts[3], `'"`)
			if arg != "$__interval" {
				if d, err := ParseDuration(arg); err == nil && d > 0 {
					step = d
				}
			}
			secs := strconv.FormatFloat(step.Seconds(), 'f', -1, 64)
			expr := "to_timestamp(floor(extract(epoch from " + parts[2] + ")/" + secs + ")*" + secs + ")"
			if parts[1] != "" {
				expr += ` AS "time"`
			}
			return expr
		})
		text = sqlTimeFromTo.ReplaceAllStringFunc(text, func(m string) string {
			if sqlTimeFromTo.FindStringSubmatch(m)[1] == "From" {
				return "'" + sqlTime(r.From) + "'"
			}
			return "'" + sqlTime(r.To) + "'"
		})
		text = sqlUnixFromTo.ReplaceAllStringFunc(text, func(m string) string {
			if sqlUnixFromTo.FindStringSubmatch(m)[1] == "From" {
				return strconv.FormatInt(r.From.Unix(), 10)
			}
			return strconv.FormatInt(r.To.Unix(), 10)
		})
	case LangInfluxQL:
		text = strings.ReplaceAll(text, "$timeFilter",
			"time >= "+strconv.FormatInt(r.From.UnixMilli(), 10)+"ms and time <= "+strconv.FormatInt(r.To.UnixMilli(), 10)+"ms")
	case LangFlux:
		text = strings.ReplaceAll(text, "v.timeRangeStart", r.From.UTC().Format(time.RFC3339Nano))
		text = strings.ReplaceAll(text, "v.timeRangeStop", r.To.UTC().Format(time.RFC3339Nano))
		text = strings.ReplaceAll(text, "v.windowPeriod", FormatDuration(interval))
	}
	return durationMacroRef.ReplaceAllStringFunc(text, func(m string) string {
		switch m[3:] {
		case "interval_ms":
			return strconv.FormatInt(interval.Milliseconds(), 10)
		case "interval":
			return FormatDuration(interval)
		case "range_ms":
			return strconv.FormatInt(r.Span().Milliseconds(), 10)
		case "range_s":
			return strconv.FormatInt(int64(r.Span().Seconds()), 10)
		case "range":
			return FormatDuration(r.Span().Round(time.Second))
		case "rate_interval":
			return FormatDuration(rate)
		case "from":
			return strconv.FormatInt(r.From.UnixMilli(), 10)
		case "to":
			return strconv.FormatInt(r.To.UnixMilli(), 10)
		}
		return m
	})
}

func sqlTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
