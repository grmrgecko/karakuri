// Package cronspec owns the cron dialect karakuri schedules on: parsing,
// next-occurrence calculation, and plain English description. The schedule
// node and the editor's preview endpoint both use it, so what the editor shows
// is what the engine will actually run.
package cronspec

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Parser accepts standard 5-field cron specs with an optional leading seconds
// field, plus descriptors like @hourly.
var Parser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// dayNames and monthNames render numeric fields as words. Cron allows both 0
// and 7 for Sunday.
var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

var monthNames = []string{
	"", "January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

// Spec applies a time zone to a cron expression using the CRON_TZ prefix the
// parser understands. An empty location leaves the server's local time.
func Spec(expr, location string) string {
	expr = strings.TrimSpace(expr)
	if location == "" {
		return expr
	}
	return "CRON_TZ=" + location + " " + expr
}

// splitZone separates the leading CRON_TZ= or TZ= word the parser reads as the
// spec's time zone from the cron fields behind it. Validate accepts that
// spelling when no separate zone is configured, so everything that reads the
// fields has to step over it the way the parser does. A spec without the word
// comes back whole with an empty zone.
func splitZone(expr string) (zone, rest string) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "TZ=") && !strings.HasPrefix(expr, "CRON_TZ=") {
		return "", expr
	}
	word, rest, ok := strings.Cut(expr, " ")
	if !ok {
		return "", expr
	}
	_, zone, _ = strings.Cut(word, "=")
	return zone, strings.TrimSpace(rest)
}

// Validate checks an expression and time zone without building a scheduler.
func Validate(expr, location string) error {
	if strings.TrimSpace(expr) == "" {
		return fmt.Errorf("no cron spec")
	}
	if location != "" {
		if _, err := time.LoadLocation(location); err != nil {
			return fmt.Errorf("invalid time zone %q: %w", location, err)
		}
	}
	if _, err := Parser.Parse(Spec(expr, location)); err != nil {
		return fmt.Errorf("invalid cron spec %q: %w", expr, err)
	}
	return nil
}

// Next returns the next n occurrences after the given time, rendered in the
// configured time zone so the editor shows local wall-clock times.
func Next(expr, location string, from time.Time, n int) ([]time.Time, error) {
	if err := Validate(expr, location); err != nil {
		return nil, err
	}
	schedule, err := Parser.Parse(Spec(expr, location))
	if err != nil {
		return nil, err
	}
	// A spec that names its own zone is shown in that zone.
	if location == "" {
		location, _ = splitZone(expr)
	}
	loc := time.Local
	if location != "" {
		if l, err := time.LoadLocation(location); err == nil {
			loc = l
		}
	}

	out := make([]time.Time, 0, n)
	next := from
	for range n {
		next = schedule.Next(next)
		if next.IsZero() {
			break
		}
		out = append(out, next.In(loc))
	}
	return out, nil
}

// Describe renders an expression as an English sentence. Expressions it
// cannot phrase naturally fall back to a field-by-field reading rather than
// guessing.
func Describe(expr string) string {
	_, expr = splitZone(expr)
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, "@") {
		return describeDescriptor(expr)
	}

	fields := strings.Fields(expr)
	var second, minute, hour, dom, month, dow string
	switch len(fields) {
	case 5:
		minute, hour, dom, month, dow = fields[0], fields[1], fields[2], fields[3], fields[4]
	case 6:
		second, minute, hour, dom, month, dow = fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]
	default:
		return ""
	}

	parts := []string{timePhrase(second, minute, hour)}
	if p := dayOfMonthPhrase(dom); p != "" {
		parts = append(parts, p)
	}
	if p := monthPhrase(month); p != "" {
		parts = append(parts, p)
	}
	if p := dayOfWeekPhrase(dow); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// describeDescriptor phrases the @-style shorthands.
func describeDescriptor(expr string) string {
	lower := strings.ToLower(expr)
	if rest, ok := strings.CutPrefix(lower, "@every "); ok {
		rest = strings.TrimSpace(rest)
		if d, err := time.ParseDuration(rest); err == nil {
			return "Every " + d.String()
		}
		return ""
	}
	switch lower {
	case "@yearly", "@annually":
		return "At 00:00 on day-of-month 1 in January"
	case "@monthly":
		return "At 00:00 on day-of-month 1"
	case "@weekly":
		return "At 00:00 on Sunday"
	case "@daily", "@midnight":
		return "At 00:00"
	case "@hourly":
		return "At minute 0 past every hour"
	}
	return ""
}

// timePhrase renders the seconds, minute, and hour fields as the leading
// clause of the sentence.
func timePhrase(second, minute, hour string) string {
	// Sub-minute schedules read as intervals rather than clock times.
	if second != "" && second != "0" {
		if step, ok := stepOf(second); ok && minute == "*" && hour == "*" {
			return fmt.Sprintf("Every %s", plural(step, "second"))
		}
		if second == "*" && minute == "*" && hour == "*" {
			return "Every second"
		}
		return fmt.Sprintf("At %s past %s past %s",
			listPhrase(second, "second", nil, 0),
			listPhrase(minute, "minute", nil, 0),
			listPhrase(hour, "hour", nil, 0))
	}

	minVal, minSingle := singleValue(minute)
	hourVal, hourSingle := singleValue(hour)
	switch {
	case minute == "*" && hour == "*":
		return "Every minute"
	case hour == "*":
		if step, ok := stepOf(minute); ok {
			return fmt.Sprintf("Every %s", plural(step, "minute"))
		}
		if minSingle {
			return fmt.Sprintf("At minute %d past every hour", minVal)
		}
		return fmt.Sprintf("At %s past every hour", listPhrase(minute, "minute", nil, 0))
	case minSingle && hourSingle:
		return fmt.Sprintf("At %02d:%02d", hourVal, minVal)
	case minSingle:
		if step, ok := stepOf(hour); ok {
			return fmt.Sprintf("At minute %d past every %s", minVal, plural(step, "hour"))
		}
		return fmt.Sprintf("At minute %d past %s", minVal, listPhrase(hour, "hour", nil, 0))
	default:
		return fmt.Sprintf("At %s past %s",
			listPhrase(minute, "minute", nil, 0),
			listPhrase(hour, "hour", nil, 0))
	}
}

// dayOfMonthPhrase renders the day-of-month field.
func dayOfMonthPhrase(dom string) string {
	if dom == "*" || dom == "?" || dom == "" {
		return ""
	}
	return "on " + listPhrase(dom, "day-of-month", nil, 0)
}

// monthPhrase renders the month field with month names.
func monthPhrase(month string) string {
	if month == "*" || month == "" {
		return ""
	}
	return "in " + listPhrase(month, "month", monthNames, 0)
}

// dayOfWeekPhrase renders the day-of-week field with weekday names.
func dayOfWeekPhrase(dow string) string {
	if dow == "*" || dow == "?" || dow == "" {
		return ""
	}
	return "on " + listPhrase(dow, "day-of-week", dayNames, 0)
}

// listPhrase renders one cron field as an English phrase. names maps values to
// words for the named fields; numeric fields carry their unit once at the
// front ("minute 5 and 10") rather than on every term.
func listPhrase(field, unit string, names []string, offset int) string {
	if field == "*" {
		return "every " + unit
	}
	terms := strings.Split(field, ",")
	rendered := make([]string, 0, len(terms))
	selfLabeled := false
	for _, term := range terms {
		phrase, standalone := termPhrase(term, unit, names, offset)
		selfLabeled = selfLabeled || standalone
		rendered = append(rendered, phrase)
	}

	var joined string
	switch len(rendered) {
	case 1:
		joined = rendered[0]
	case 2:
		joined = rendered[0] + " and " + rendered[1]
	default:
		joined = strings.Join(rendered[:len(rendered)-1], ", ") + " and " + rendered[len(rendered)-1]
	}
	// Named fields read as words already; step terms carry their own unit.
	if names != nil || selfLabeled {
		return joined
	}
	return unit + " " + joined
}

// termPhrase renders a single comma-separated term: a value, a range, or a
// step over either. It reports whether the phrase already names its unit.
func termPhrase(term, unit string, names []string, offset int) (string, bool) {
	base, step, hasStep := strings.Cut(term, "/")
	if hasStep {
		if n, err := strconv.Atoi(step); err == nil && n > 0 {
			// Steps read as ordinals here ("every 15th minute"); the bare
			// interval form is used only when the step spans the whole field,
			// which timePhrase handles before reaching this point.
			if base == "*" {
				return fmt.Sprintf("every %s %s", ordinal(n), unit), true
			}
			lo, hi, isRange := strings.Cut(base, "-")
			if isRange {
				return fmt.Sprintf("every %s %s from %s through %s", ordinal(n), unit,
					valueName(lo, names, offset), valueName(hi, names, offset)), true
			}
			return fmt.Sprintf("every %s %s from %s", ordinal(n), unit,
				valueName(base, names, offset)), true
		}
	}
	if lo, hi, isRange := strings.Cut(base, "-"); isRange {
		return valueName(lo, names, offset) + " through " + valueName(hi, names, offset), false
	}
	if base == "*" {
		return "every " + unit, true
	}
	return valueName(base, names, offset), false
}

// dayAbbrev and monthAbbrev resolve the textual forms cron accepts.
var dayAbbrev = map[string]string{
	"sun": "Sunday", "mon": "Monday", "tue": "Tuesday", "wed": "Wednesday",
	"thu": "Thursday", "fri": "Friday", "sat": "Saturday",
}

var monthAbbrev = map[string]string{
	"jan": "January", "feb": "February", "mar": "March", "apr": "April",
	"may": "May", "jun": "June", "jul": "July", "aug": "August",
	"sep": "September", "oct": "October", "nov": "November", "dec": "December",
}

// valueName renders one value, resolving both numbers and the textual weekday
// and month names cron accepts.
func valueName(value string, names []string, offset int) string {
	value = strings.TrimSpace(value)
	n, err := strconv.Atoi(value)
	if err != nil {
		key := strings.ToLower(value)
		// Only the field's own name table is consulted, so a month never
		// resolves to a weekday.
		if len(names) > 0 && names[0] == "Sunday" {
			if full, ok := dayAbbrev[key]; ok {
				return full
			}
		}
		if len(names) > 0 && names[0] == "" {
			if full, ok := monthAbbrev[key]; ok {
				return full
			}
		}
		return value
	}
	idx := n - offset
	if names != nil && idx >= 0 && idx < len(names) && names[idx] != "" {
		return names[idx]
	}
	return strconv.Itoa(n)
}

// singleValue reports whether a field is one plain number and returns it.
func singleValue(field string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(field))
	if err != nil {
		return 0, false
	}
	return n, true
}

// stepOf returns the step of a whole-range step field such as "*/5".
func stepOf(field string) (int, bool) {
	rest, ok := strings.CutPrefix(field, "*/")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// plural renders a count with its unit, pluralizing above one and dropping the
// count entirely at one ("every minute" rather than "every 1 minutes").
func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// ordinal renders a number as 1st, 2nd, 3rd, 4th, and so on.
func ordinal(n int) string {
	suffix := "th"
	// The teens are all "th" regardless of their final digit.
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// FieldLabels returns the positional field names for an expression, letting
// the editor label each position under the expression. Descriptors and
// malformed expressions return nil.
func FieldLabels(expr string) []string {
	_, expr = splitZone(expr)
	fields := strings.Fields(expr)
	switch len(fields) {
	case 5:
		return []string{"minute", "hour", "day of month", "month", "day of week"}
	case 6:
		return []string{"second", "minute", "hour", "day of month", "month", "day of week"}
	}
	return nil
}
