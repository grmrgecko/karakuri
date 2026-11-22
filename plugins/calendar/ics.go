package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// ICSProvider parses iCalendar documents. Feeds reach it through a calendar
// connection; the ics node hands it whatever arrived on a message, from a mail
// attachment or a file read.
type ICSProvider struct{}

// NewICSProvider creates an ICS parser.
func NewICSProvider() *ICSProvider {
	return &ICSProvider{}
}

// ParseEvents reads the events of an ICS document into occurrences within the
// window, resolving floating times in the server's zone.
func (p *ICSProvider) ParseEvents(r io.Reader, windowStart, windowEnd time.Time) ([]CalendarEvent, error) {
	return parseCalendar(r, parseOptions{from: windowStart, to: windowEnd, loc: time.Local, max: maxOccurrences})
}

// parseOptions bounds and labels one parse.
type parseOptions struct {
	from, to time.Time
	// loc resolves floating times and all-day dates, which carry no zone.
	loc *time.Location
	// calendar names the calendar the document came from.
	calendar string
	// max caps the occurrences one document may contribute.
	max int
}

// Expansion limits. maxFeedBytes bounds a downloaded feed, maxOccurrences
// bounds what one document may contribute, and maxRuleSteps bounds how far a
// rule is walked to reach the window at all: a rule with no COUNT or UNTIL is
// unbounded, and a SECONDLY one over a long window is millions of steps.
const (
	maxFeedBytes   = 16 << 20
	maxOccurrences = 5000
	maxRuleSteps   = 1000000
)

// vevent is one VEVENT with the fields expansion needs.
type vevent struct {
	uid, summary, location, status string
	start, end                     time.Time
	allDay                         bool
	rrule                          string
	exdates                        map[int64]bool
	// recurrenceID is the original start of the occurrence an override
	// replaces, zero for a master or a single event.
	recurrenceID time.Time
}

// parseCalendar expands every event in an ICS document into occurrences that
// overlap the window, sorted by start. Overrides (RECURRENCE-ID) replace the
// occurrence they name, EXDATE removes occurrences, and cancelled events and
// occurrences are left out.
func parseCalendar(r io.Reader, opt parseOptions) ([]CalendarEvent, error) {
	if opt.loc == nil {
		opt.loc = time.Local
	}
	if opt.max <= 0 {
		opt.max = maxOccurrences
	}
	cal, err := ical.NewDecoder(r).Decode()
	if err != nil {
		return nil, fmt.Errorf("parsing ICS: %w", err)
	}
	return expandCalendar(cal, opt), nil
}

// expandCalendar expands the events of a decoded calendar. A document that
// names itself (X-WR-CALNAME) labels its occurrences when the caller gave no
// calendar name.
func expandCalendar(cal *ical.Calendar, opt parseOptions) []CalendarEvent {
	if opt.loc == nil {
		opt.loc = time.Local
	}
	if opt.max <= 0 {
		opt.max = maxOccurrences
	}
	if opt.calendar == "" {
		opt.calendar = textValue(cal.Component, "X-WR-CALNAME")
	}
	masters := make(map[string]*vevent)
	var order []string
	overrides := make(map[string][]*vevent)
	for i, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		ev := readEvent(comp, opt.loc)
		if ev.start.IsZero() {
			continue
		}
		if !ev.recurrenceID.IsZero() {
			overrides[ev.uid] = append(overrides[ev.uid], ev)
			continue
		}
		// Events without a UID are distinct events, not copies of one, so
		// each gets its own key. The key starts with a NUL, which no real UID
		// holds, and an override without a UID still stands on its own because
		// no master is keyed by the empty string.
		key := ev.uid
		if key == "" {
			key = fmt.Sprintf("\x00%d", i)
		}
		if _, dup := masters[key]; !dup {
			order = append(order, key)
		}
		masters[key] = ev
	}

	var out []CalendarEvent
	for _, uid := range order {
		out = append(out, expand(masters[uid], overrides[uid], opt)...)
		delete(overrides, uid)
	}
	// An override whose master is not in the document stands on its own.
	for _, list := range overrides {
		for _, ov := range list {
			if keep(ov, opt) {
				out = append(out, occurrence(ov, ov.recurrenceID, ov.start, ov.end, false, opt))
			}
		}
	}

	sortEvents(out)
	if len(out) > opt.max {
		out = out[:opt.max]
	}
	return out
}

// sortEvents orders occurrences by start time.
func sortEvents(events []CalendarEvent) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].startTime().Before(events[j].startTime()) })
}

// expand produces a master event's occurrences in the window, applying its
// overrides and exclusions.
func expand(m *vevent, overrides []*vevent, opt parseOptions) []CalendarEvent {
	if strings.EqualFold(m.status, "CANCELLED") {
		return nil
	}
	byOriginal := make(map[int64]*vevent, len(overrides))
	for _, ov := range overrides {
		byOriginal[ov.recurrenceID.Unix()] = ov
	}

	if m.rrule == "" {
		if ov, ok := byOriginal[m.start.Unix()]; ok {
			if keep(ov, opt) {
				return []CalendarEvent{occurrence(ov, m.start, ov.start, ov.end, false, opt)}
			}
			return nil
		}
		if keep(m, opt) {
			return []CalendarEvent{occurrence(m, m.start, m.start, m.end, false, opt)}
		}
		return nil
	}

	// An UNTIL without a zone is in the zone of DTSTART (RFC 5545 3.3.10).
	// Read as UTC it cuts off the last occurrence west of Greenwich.
	ropt, err := rrule.StrToROptionInLocation(m.rrule, m.start.Location())
	if err != nil {
		// A rule that does not parse still has its first occurrence.
		if keep(m, opt) {
			return []CalendarEvent{occurrence(m, m.start, m.start, m.end, false, opt)}
		}
		return nil
	}
	// The rule runs in the event's own zone, so a 07:00 alarm stays at 07:00
	// across a daylight saving change.
	ropt.Dtstart = m.start
	rule, err := rrule.NewRRule(*ropt)
	if err != nil {
		if keep(m, opt) {
			return []CalendarEvent{occurrence(m, m.start, m.start, m.end, false, opt)}
		}
		return nil
	}

	days := 0
	dur := m.end.Sub(m.start)
	if m.allDay {
		days = int(m.end.Sub(m.start).Hours()/24 + 0.5)
	}
	var out []CalendarEvent
	next := rule.Iterator()
	for steps := 0; steps < maxRuleSteps && len(out) < opt.max; steps++ {
		occ, ok := next()
		if !ok || !occ.Before(opt.to) {
			break
		}
		if m.exdates[occ.Unix()] {
			continue
		}
		if ov, ok := byOriginal[occ.Unix()]; ok {
			if keep(ov, opt) {
				out = append(out, occurrence(ov, occ, ov.start, ov.end, true, opt))
			}
			continue
		}
		end := occ.Add(dur)
		if m.allDay {
			end = occ.AddDate(0, 0, days)
		}
		if end.After(opt.from) {
			out = append(out, occurrence(m, occ, occ, end, true, opt))
		}
	}
	// Overrides moved into the window from an original start outside it are
	// still occurrences of this event.
	for _, ov := range overrides {
		if keep(ov, opt) && !containsID(out, occurrenceID(m.uid, m.summary, ov.recurrenceID)) {
			out = append(out, occurrence(ov, ov.recurrenceID, ov.start, ov.end, true, opt))
		}
	}
	return out
}

// keep reports whether a single event or override is shown: not cancelled,
// and overlapping the window.
func keep(ev *vevent, opt parseOptions) bool {
	if strings.EqualFold(ev.status, "CANCELLED") {
		return false
	}
	return ev.start.Before(opt.to) && ev.end.After(opt.from)
}

// containsID reports whether an occurrence with id is already listed.
func containsID(list []CalendarEvent, id string) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
}

// occurrence renders one occurrence. original is the start the occurrence's
// identity comes from, which stays fixed when an override moves it.
func occurrence(ev *vevent, original, start, end time.Time, recurring bool, opt parseOptions) CalendarEvent {
	return CalendarEvent{
		ID:        occurrenceID(ev.uid, ev.summary, original),
		UID:       ev.uid,
		Calendar:  opt.calendar,
		Title:     ev.summary,
		Start:     start.Format(time.RFC3339),
		End:       end.Format(time.RFC3339),
		AllDay:    ev.allDay,
		Location:  ev.location,
		Recurring: recurring,
	}
}

// occurrenceID identifies one occurrence by its event and original start, so
// a moved occurrence keeps its identity. An event without a UID is keyed by
// its title instead.
func occurrenceID(uid, summary string, original time.Time) string {
	if uid == "" {
		h := sha256.Sum256([]byte(summary))
		uid = fmt.Sprintf("%x", h[:8])
	}
	return uid + "@" + original.UTC().Format(time.RFC3339)
}

// readEvent reads the fields of one VEVENT. An event with no end ends at its
// start, or a day later when it is all-day. An all-day event always covers at
// least its start date.
func readEvent(comp *ical.Component, loc *time.Location) *vevent {
	ev := &vevent{
		uid:      propValue(comp, ical.PropUID),
		summary:  textValue(comp, ical.PropSummary),
		location: textValue(comp, ical.PropLocation),
		status:   propValue(comp, ical.PropStatus),
		rrule:    propValue(comp, ical.PropRecurrenceRule),
		exdates:  make(map[int64]bool),
	}
	ev.start, ev.allDay = parseDateTime(comp.Props.Get(ical.PropDateTimeStart), loc)
	ev.end, _ = parseDateTime(comp.Props.Get(ical.PropDateTimeEnd), loc)
	if ev.end.IsZero() && !ev.start.IsZero() {
		if dur := propValue(comp, ical.PropDuration); dur != "" {
			if d, err := parseICSDuration(dur); err == nil {
				ev.end = ev.start.Add(d)
				// An all-day event spans calendar days, and a day the
				// clocks change on is not 24 hours long.
				if ev.allDay {
					ev.end = ev.start.AddDate(0, 0, int(d/(24*time.Hour)))
				}
			}
		}
	}
	if ev.end.IsZero() && !ev.start.IsZero() {
		if ev.allDay {
			ev.end = ev.start.AddDate(0, 0, 1)
		} else {
			ev.end = ev.start
		}
	}
	// An all-day DTEND is exclusive, so one on the start date leaves an empty
	// span. Producers write that for a single day, which is what it is read as.
	if ev.allDay && !ev.start.IsZero() && !ev.end.After(ev.start) {
		ev.end = ev.start.AddDate(0, 0, 1)
	}
	ev.recurrenceID, _ = parseDateTime(comp.Props.Get(ical.PropRecurrenceID), loc)
	for _, prop := range comp.Props[ical.PropExceptionDates] {
		for _, value := range strings.Split(prop.Value, ",") {
			single := prop
			single.Value = strings.TrimSpace(value)
			if t, _ := parseDateTime(&single, loc); !t.IsZero() {
				ev.exdates[t.Unix()] = true
			}
		}
	}
	return ev
}

// propValue returns the value of the first property with the given name.
func propValue(comp *ical.Component, name string) string {
	prop := comp.Props.Get(name)
	if prop == nil {
		return ""
	}
	return prop.Value
}

// textValue returns the first property with the given name read as TEXT.
func textValue(comp *ical.Component, name string) string {
	return unescapeText(propValue(comp, name))
}

// unescapeText decodes the backslash escapes of an RFC 5545 TEXT value: \\,
// \; and \, stand for the character itself and \n or \N for a newline. An
// escape the format does not define is kept as written, and so is a bare
// comma, which producers leave unescaped often enough that it cannot be read
// as a list separator.
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			sb.WriteByte(s[i])
			continue
		}
		switch next := s[i+1]; next {
		case '\\', ';', ',':
			sb.WriteByte(next)
			i++
		case 'n', 'N':
			sb.WriteByte('\n')
			i++
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// parseDateTime reads a date or date-time property. A date (VALUE=DATE, or
// eight digits) is all-day and starts at midnight in loc; a TZID names the
// zone; a trailing Z is UTC; anything else is floating and read in loc.
func parseDateTime(prop *ical.Prop, loc *time.Location) (time.Time, bool) {
	if prop == nil || prop.Value == "" {
		return time.Time{}, false
	}
	value := strings.TrimSpace(prop.Value)
	if strings.EqualFold(prop.Params.Get("VALUE"), "DATE") || len(value) == 8 {
		t, err := time.ParseInLocation("20060102", value, loc)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	}
	if strings.HasSuffix(value, "Z") {
		if t, err := time.Parse("20060102T150405Z", value); err == nil {
			return t, false
		}
	}
	zone := loc
	if tzid := prop.Params.Get("TZID"); tzid != "" {
		if l, err := time.LoadLocation(tzid); err == nil {
			zone = l
		}
	}
	if t, err := time.ParseInLocation("20060102T150405", value, zone); err == nil {
		return t, false
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, false
	}
	return time.Time{}, false
}

// parseICSDuration reads an RFC 5545 duration such as PT1H30M or P1D.
func parseICSDuration(s string) (time.Duration, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("duration %q does not start with P", s)
	}
	s = s[1:]
	var total time.Duration
	inTime := false
	num := 0
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			num = num*10 + int(c-'0')
		case c == 'T':
			inTime = true
		case c == 'W':
			total += time.Duration(num) * 7 * 24 * time.Hour
			num = 0
		case c == 'D':
			total += time.Duration(num) * 24 * time.Hour
			num = 0
		case c == 'H' && inTime:
			total += time.Duration(num) * time.Hour
			num = 0
		case c == 'M' && inTime:
			total += time.Duration(num) * time.Minute
			num = 0
		case c == 'S' && inTime:
			total += time.Duration(num) * time.Second
			num = 0
		default:
			return 0, fmt.Errorf("duration %q is not understood", s)
		}
	}
	if neg {
		total = -total
	}
	return total, nil
}
