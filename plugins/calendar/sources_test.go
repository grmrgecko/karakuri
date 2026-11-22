package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// familyICS is a feed exercising what real calendars do: a weekly event in a
// named zone across the daylight saving change, one excluded occurrence, one
// moved occurrence, a cancelled event, a week-long all-day event, and an
// event with no end.
const familyICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
X-WR-CALNAME:Family
BEGIN:VEVENT
UID:standup
SUMMARY:Standup
DTSTART;TZID=America/Chicago:20261026T090000
DTEND;TZID=America/Chicago:20261026T091500
RRULE:FREQ=WEEKLY;COUNT=6
EXDATE;TZID=America/Chicago:20261109T090000
END:VEVENT
BEGIN:VEVENT
UID:standup
RECURRENCE-ID;TZID=America/Chicago:20261116T090000
SUMMARY:Standup (late)
DTSTART;TZID=America/Chicago:20261116T100000
DTEND;TZID=America/Chicago:20261116T101500
END:VEVENT
BEGIN:VEVENT
UID:cancelled
SUMMARY:Dentist
STATUS:CANCELLED
DTSTART:20261110T150000Z
DTEND:20261110T160000Z
END:VEVENT
BEGIN:VEVENT
UID:vacation
SUMMARY:Vacation
DTSTART;VALUE=DATE:20261120
DTEND;VALUE=DATE:20261127
END:VEVENT
BEGIN:VEVENT
UID:reminder
SUMMARY:Take out bins
DTSTART:20261103T013000Z
END:VEVENT
END:VCALENDAR
`

func chicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	return loc
}

// byTitle indexes occurrences by title for the assertions below.
func byTitle(events []CalendarEvent) map[string][]CalendarEvent {
	out := map[string][]CalendarEvent{}
	for _, e := range events {
		out[e.Title] = append(out[e.Title], e)
	}
	return out
}

// TestExpandCalendar covers occurrence expansion: local times hold across the
// daylight saving change, EXDATE removes an occurrence, a moved occurrence
// keeps its original identity, cancelled events are left out, and all-day and
// endless events get sensible ends.
func TestExpandCalendar(t *testing.T) {
	loc := chicago(t)
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	to := time.Date(2026, 12, 1, 0, 0, 0, 0, loc)
	events, err := parseCalendar(strings.NewReader(familyICS), parseOptions{from: from, to: to, loc: loc})
	require.NoError(t, err)
	got := byTitle(events)

	var starts []string
	for _, e := range got["Standup"] {
		starts = append(starts, e.startTime().In(loc).Format("01-02 15:04"))
		assert.Equal(t, "Family", e.Calendar, "the feed's own name labels its events")
		assert.True(t, e.Recurring)
	}
	assert.Equal(t, []string{"10-26 09:00", "11-02 09:00", "11-23 09:00", "11-30 09:00"}, starts,
		"09:00 stays 09:00 after the clocks change, 11-09 is excluded, and 11-16 moved")

	require.Len(t, got["Standup (late)"], 1, "the moved occurrence appears once, at its new time")
	moved := got["Standup (late)"][0]
	assert.Equal(t, "11-16 10:00", moved.startTime().In(loc).Format("01-02 15:04"))
	assert.Equal(t, "standup@"+time.Date(2026, 11, 16, 9, 0, 0, 0, loc).UTC().Format(time.RFC3339), moved.ID,
		"a moved occurrence keeps the identity of its original start")

	assert.Empty(t, got["Dentist"], "a cancelled event is left out")

	require.Len(t, got["Vacation"], 1)
	vacation := got["Vacation"][0]
	assert.True(t, vacation.AllDay)
	assert.Equal(t, time.Date(2026, 11, 20, 0, 0, 0, 0, loc), vacation.startTime().In(loc), "all-day starts at local midnight")
	assert.Equal(t, time.Date(2026, 11, 27, 0, 0, 0, 0, loc), vacation.endTime().In(loc))

	require.Len(t, got["Take out bins"], 1)
	assert.Equal(t, got["Take out bins"][0].Start, got["Take out bins"][0].End, "an event without an end ends at its start")
}

// TestExpandCalendarBounds covers the occurrence cap, which keeps an
// unbounded rule from flooding a refresh.
func TestExpandCalendarBounds(t *testing.T) {
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:tick\nSUMMARY:Tick\nDTSTART:20261101T000000Z\nRRULE:FREQ=MINUTELY\nEND:VEVENT\nEND:VCALENDAR\n"
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: from, to: from.AddDate(0, 1, 0), loc: time.UTC, max: 50})
	require.NoError(t, err)
	assert.Len(t, events, 50)
}

// TestExpandCalendarWithoutUIDs covers feeds that leave UID out. Each such
// VEVENT is its own event and keeps a title-based identity, rather than all of
// them collapsing into whichever came last.
func TestExpandCalendarWithoutUIDs(t *testing.T) {
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\n" +
		"BEGIN:VEVENT\nSUMMARY:Bins\nDTSTART:20261103T013000Z\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nSUMMARY:Recycling\nDTSTART:20261104T013000Z\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: from, to: from.AddDate(0, 1, 0), loc: time.UTC})
	require.NoError(t, err)
	got := byTitle(events)
	require.Len(t, got["Bins"], 1, "the first UID-less event must survive the second")
	require.Len(t, got["Recycling"], 1)
	assert.NotEqual(t, got["Bins"][0].ID, got["Recycling"][0].ID, "UID-less events are told apart by title")
}

// TestICSSourceAuth covers the ICS kind's authentication modes against a feed
// that requires them, and the reason a refused request reports.
func TestICSSourceAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, basic := r.BasicAuth()
		if (basic && user == "me" && pass == "secret") || r.Header.Get("Authorization") == "Bearer tok" {
			_, _ = w.Write([]byte(familyICS))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)

	for _, src := range []*icsSource{
		{url: srv.URL, auth: "basic", username: "me", password: "secret"},
		{url: srv.URL, auth: "bearer", token: "tok"},
	} {
		events, err := src.fetch(context.Background(), from, to, time.UTC, 100)
		require.NoError(t, err, src.auth)
		assert.NotEmpty(t, events)
	}
	_, err := (&icsSource{url: srv.URL, auth: "basic", username: "me", password: "wrong"}).fetch(context.Background(), from, to, time.UTC, 100)
	assert.EqualError(t, err, "the feed returned HTTP 401")
}

// memCalDAV is an in-memory CalDAV backend with two calendars.
type memCalDAV struct {
	objects map[string][]caldav.CalendarObject
}

func (b *memCalDAV) CurrentUserPrincipal(context.Context) (string, error) { return "/user/", nil }
func (b *memCalDAV) CalendarHomeSetPath(context.Context) (string, error) {
	return "/user/calendars/", nil
}
func (b *memCalDAV) CreateCalendar(context.Context, *caldav.Calendar) error {
	return errors.New("read-only")
}
func (b *memCalDAV) ListCalendars(context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{
		{Path: "/user/calendars/family/", Name: "Family", SupportedComponentSet: []string{"VEVENT"}},
		{Path: "/user/calendars/work/", Name: "Work", SupportedComponentSet: []string{"VEVENT"}},
	}, nil
}
func (b *memCalDAV) GetCalendar(ctx context.Context, path string) (*caldav.Calendar, error) {
	cals, _ := b.ListCalendars(ctx)
	for _, c := range cals {
		if c.Path == path {
			return &c, nil
		}
	}
	return nil, errors.New("no such calendar")
}
func (b *memCalDAV) GetCalendarObject(context.Context, string, *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	return nil, errors.New("not found")
}
func (b *memCalDAV) ListCalendarObjects(_ context.Context, path string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	return b.objects[path], nil
}
func (b *memCalDAV) QueryCalendarObjects(_ context.Context, path string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	return caldav.Filter(q, b.objects[path])
}
func (b *memCalDAV) PutCalendarObject(context.Context, string, *ical.Calendar, *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	return nil, errors.New("read-only")
}
func (b *memCalDAV) DeleteCalendarObject(context.Context, string) error {
	return errors.New("read-only")
}

// calObject builds one stored event.
func calObject(t *testing.T, path, uid, summary string, start time.Time) caldav.CalendarObject {
	t.Helper()
	feed := fmt.Sprintf("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//test//EN\nBEGIN:VEVENT\nUID:%s\nDTSTAMP:20261001T000000Z\nSUMMARY:%s\nDTSTART:%s\nDTEND:%s\nEND:VEVENT\nEND:VCALENDAR\n",
		uid, summary, start.UTC().Format("20060102T150405Z"), start.Add(time.Hour).UTC().Format("20060102T150405Z"))
	cal, err := ical.NewDecoder(strings.NewReader(feed)).Decode()
	require.NoError(t, err)
	return caldav.CalendarObject{Path: path + uid + ".ics", Data: cal}
}

// TestCalDAVSource covers CalDAV discovery and calendar selection against a
// real CalDAV server: every calendar is listed, a selection reads only the
// calendars it names, and an unknown selection says so.
func TestCalDAVSource(t *testing.T) {
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	backend := &memCalDAV{objects: map[string][]caldav.CalendarObject{
		"/user/calendars/family/": {calObject(t, "/user/calendars/family/", "dinner", "Dinner", start)},
		"/user/calendars/work/":   {calObject(t, "/user/calendars/work/", "review", "Review", start.Add(-4*time.Hour))},
	}}
	srv := httptest.NewServer(&caldav.Handler{Backend: backend})
	defer srv.Close()
	from, to := start.AddDate(0, 0, -1), start.AddDate(0, 0, 1)

	all := &caldavSource{url: srv.URL, username: "me", password: "pw"}
	names, err := all.calendars(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Family", "Work"}, names)

	events, err := all.fetch(context.Background(), from, to, time.UTC, 100)
	require.NoError(t, err)
	assert.Len(t, events, 2, "an empty selection reads every calendar")

	work := &caldavSource{url: srv.URL, username: "me", password: "pw", selected: []string{"work"}}
	events, err = work.fetch(context.Background(), from, to, time.UTC, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Review", events[0].Title)
	assert.Equal(t, "Work", events[0].Calendar)

	_, err = (&caldavSource{url: srv.URL, username: "me", password: "pw", selected: []string{"Holidays"}}).fetch(context.Background(), from, to, time.UTC, 100)
	assert.ErrorContains(t, err, "none of the selected calendars (Holidays) exist")
}

// TestGoogleSource covers both Google access modes against a recorded API:
// API keys go in the query, oauth2 tokens in the header, cancelled instances
// are dropped, dates are all-day, and the API's error message is surfaced.
func TestGoogleSource(t *testing.T) {
	var lastKey, lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastKey, lastAuth = r.URL.Query().Get("key"), r.Header.Get("Authorization")
		if strings.Contains(r.URL.Path, "missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Not Found"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"summary": "Holidays",
			"items": []map[string]any{
				{"id": "a", "iCalUID": "a@google", "summary": "Thanksgiving", "start": map[string]any{"date": "2026-11-26"}, "end": map[string]any{"date": "2026-11-27"}},
				{"id": "b", "summary": "Gone", "status": "cancelled", "start": map[string]any{"dateTime": "2026-11-27T10:00:00Z"}},
			},
		})
	}))
	defer srv.Close()
	old := googleEndpoint
	googleEndpoint = srv.URL
	defer func() { googleEndpoint = old }()
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	keyed := &googleSource{ids: []string{"holidays"}, apiKey: "k1"}
	events, err := keyed.fetch(context.Background(), from, from.AddDate(0, 1, 0), time.UTC, 100)
	require.NoError(t, err)
	require.Len(t, events, 1, "cancelled instances are dropped")
	assert.Equal(t, "k1", lastKey)
	assert.True(t, events[0].AllDay)
	assert.Equal(t, "Holidays", events[0].Calendar)

	tokened := &googleSource{ids: []string{"holidays"}, token: func(context.Context) (string, error) { return "ya29", nil }}
	_, err = tokened.fetch(context.Background(), from, from.AddDate(0, 1, 0), time.UTC, 100)
	require.NoError(t, err)
	assert.Equal(t, "Bearer ya29", lastAuth)
	assert.Empty(t, lastKey, "a token request carries no API key")

	_, err = (&googleSource{ids: []string{"missing"}, apiKey: "k1"}).fetch(context.Background(), from, from.AddDate(0, 1, 0), time.UTC, 100)
	assert.ErrorContains(t, err, "HTTP 404: Not Found")
}

// flakySource succeeds once and then fails.
type flakySource struct{ calls int }

func (s *flakySource) fetch(context.Context, time.Time, time.Time, *time.Location, int) ([]CalendarEvent, error) {
	s.calls++
	if s.calls == 1 {
		return []CalendarEvent{{ID: "x", Title: "Planned"}}, nil
	}
	return nil, errors.New("server down")
}
func (s *flakySource) calendars(context.Context) ([]string, error) { return nil, nil }

// TestDriverStaleFallback covers a failing refresh: the last good events
// stand in for up to a day, marked with when they were read, and the
// connection reports the error meanwhile.
func TestDriverStaleFallback(t *testing.T) {
	p := &CalendarPlugin{}
	d := &calendarDriver{id: uuid.New(), name: "Family", src: &flakySource{}, loc: time.UTC, days: 7, max: 100, p: p,
		deps: integrations.Deps{Logger: zap.NewNop()}}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	events, stale, err := d.Events(context.Background(), now, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, stale.IsZero())
	assert.Equal(t, integrations.StateConnected, d.Status().State)

	events, stale, err = d.Events(context.Background(), now.Add(time.Hour), 0)
	require.NoError(t, err, "a failing refresh within a day serves the last good list")
	assert.Len(t, events, 1)
	assert.Equal(t, now, stale)
	assert.Equal(t, integrations.StateError, d.Status().State)

	_, _, err = d.Events(context.Background(), now.Add(25*time.Hour), 0)
	assert.ErrorContains(t, err, "server down", "after a day the failure is the answer")
}

// TestProbeDetail covers what a connection test reports for an ICS feed.
func TestProbeDetail(t *testing.T) {
	soon := time.Now().Add(time.Hour).UTC().Format("20060102T150405Z")
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\nX-WR-CALNAME:Family\nBEGIN:VEVENT\nUID:soon\nSUMMARY:Soon\nDTSTART:" + soon + "\nEND:VEVENT\nEND:VCALENDAR\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(feed)) }))
	defer srv.Close()
	p := &CalendarPlugin{}
	cfg, _ := json.Marshal(map[string]any{"url": srv.URL})
	detail, err := p.probe(context.Background(), kindICS, cfg)
	require.NoError(t, err)
	assert.Equal(t, "1 calendar (Family); 1 events in the next 7 days", detail)

	_, err = p.probe(context.Background(), kindGoogle, []byte(`{"calendars":"x"}`))
	assert.EqualError(t, err, "an API key is required for public calendars")
}

// TestExpandCalendarFloatingUntil locks RFC 5545 section 3.3.10: an UNTIL
// without a zone is read in the same zone as DTSTART, and the occurrence that
// falls on it counts. Read as UTC, the bound lands before the last local
// occurrence in every zone west of Greenwich. Here 5 March 00:00 Chicago is
// 06:00Z, past an UNTIL taken as 5 March 00:00Z, and the last day vanished.
func TestExpandCalendarFloatingUntil(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err, "the zone database must hold America/Chicago")
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\n" +
		"BEGIN:VEVENT\nUID:trip\nSUMMARY:Trip\nDTSTART;VALUE=DATE:20240301\nRRULE:FREQ=DAILY;UNTIL=20240305\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:alarm\nSUMMARY:Alarm\nDTSTART:20240301T070000\nRRULE:FREQ=DAILY;UNTIL=20240305T070000\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	from := time.Date(2024, 2, 25, 0, 0, 0, 0, chicago)
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: from, to: from.AddDate(0, 1, 0), loc: chicago})
	require.NoError(t, err)

	got := byTitle(events)
	assert.Len(t, got["Trip"], 5, "an all-day rule runs through its UNTIL date: 1, 2, 3, 4, and 5 March")
	assert.Len(t, got["Alarm"], 5, "a floating rule includes the occurrence at its UNTIL time")
}

// TestExpandCalendarUnescapesText pins that TEXT values come out of a feed as
// the text they encode. iCalendar writes a comma, semicolon, backslash, and
// newline inside a value as backslash escapes, and nearly every address holds
// a comma, so reading the value raw showed "123 Main St\, Springfield" on
// dashboards and in flow payloads. A comma a sloppy producer left bare stays.
func TestExpandCalendarUnescapesText(t *testing.T) {
	feed := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0",
		`X-WR-CALNAME:Home\, shared`,
		"BEGIN:VEVENT", "UID:lunch",
		`SUMMARY:Lunch\, with Bob\; bring C:\\notes\nRoom 2, upstairs`,
		`LOCATION:123 Main St\, Springfield`,
		"DTSTART:20261103T180000Z", "DTEND:20261103T190000Z",
		"END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: from, to: from.AddDate(0, 1, 0), loc: time.UTC})
	require.NoError(t, err, "the feed must parse")
	require.Len(t, events, 1, "the feed holds one event")

	assert.Equal(t, "Lunch, with Bob; bring C:\\notes\nRoom 2, upstairs", events[0].Title,
		"every escape in the summary must decode, and a bare comma must survive")
	assert.Equal(t, "123 Main St, Springfield", events[0].Location, "an address must read without its escape")
	assert.Equal(t, "Home, shared", events[0].Calendar, "the calendar name is text like any other")
}

// TestExpandCalendarAllDayDuration pins the end of an all-day event that
// gives a DURATION instead of DTEND: whole calendar days after its start. A
// day is 25 hours long when the clocks go back, so adding 24 hours ended the
// event at 23:00 on its own date instead of the next midnight.
func TestExpandCalendarAllDayDuration(t *testing.T) {
	loc := chicago(t)
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:fallback\nSUMMARY:Clocks change\n" +
		"DTSTART;VALUE=DATE:20261101\nDURATION:P1D\nEND:VEVENT\nEND:VCALENDAR\n"
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: from, to: from.AddDate(0, 1, 0), loc: loc})
	require.NoError(t, err, "the feed must parse")
	require.Len(t, events, 1, "the feed holds one event")

	assert.Equal(t, time.Date(2026, 11, 2, 0, 0, 0, 0, loc), events[0].endTime().In(loc),
		"a one-day all-day event ends at the next local midnight, however long the day is")
}

// TestExpandCalendarAllDayEndOnStartDate pins the span of an all-day event
// whose DTEND repeats its DTSTART date, which some producers write for a
// single day. DTEND is exclusive, so read literally the event was empty: it
// was never in progress on its own day and its end fired at its start. It
// covers its start date, and an event with a real exclusive end keeps it.
func TestExpandCalendarAllDayEndOnStartDate(t *testing.T) {
	loc := chicago(t)
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\n" +
		"BEGIN:VEVENT\nUID:same\nSUMMARY:Same date\nDTSTART;VALUE=DATE:20261103\nDTEND;VALUE=DATE:20261103\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:two\nSUMMARY:Two days\nDTSTART;VALUE=DATE:20261103\nDTEND;VALUE=DATE:20261105\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	from := time.Date(2026, 11, 3, 9, 0, 0, 0, loc)
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: from, to: from.AddDate(0, 1, 0), loc: loc})
	require.NoError(t, err, "the feed must parse")

	got := byTitle(events)
	require.Len(t, got["Same date"], 1, "an all-day event is still on while its day is under way")
	assert.Equal(t, time.Date(2026, 11, 4, 0, 0, 0, 0, loc), got["Same date"][0].endTime().In(loc),
		"an all-day event ending on its start date covers that date")
	require.Len(t, got["Two days"], 1, "the two-day event is in the window")
	assert.Equal(t, time.Date(2026, 11, 5, 0, 0, 0, 0, loc), got["Two days"][0].endTime().In(loc),
		"an exclusive end after the start must be kept as written")
}
