package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

// eventSource reads occurrences from one kind of calendar service.
type eventSource interface {
	// fetch returns the occurrences overlapping [from, to).
	fetch(ctx context.Context, from, to time.Time, loc *time.Location, max int) ([]CalendarEvent, error)
	// calendars lists the calendars the source reads, for a probe.
	calendars(ctx context.Context) ([]string, error)
}

// httpClient is shared by every source; requests carry their own deadline.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---- ICS feeds ----

// icsSource reads one ICS feed over HTTP.
type icsSource struct {
	url, auth, username, password, token string
	// name labels occurrences when the feed does not name itself.
	name string
}

func (s *icsSource) get(ctx context.Context) (*ical.Calendar, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the feed request: %w", err)
	}
	switch s.auth {
	case "basic":
		req.SetBasicAuth(s.username, s.password)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching the feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the feed returned HTTP %d", resp.StatusCode)
	}
	cal, err := ical.NewDecoder(io.LimitReader(resp.Body, maxFeedBytes)).Decode()
	if err != nil {
		return nil, fmt.Errorf("parsing the feed: %w", err)
	}
	return cal, nil
}

func (s *icsSource) fetch(ctx context.Context, from, to time.Time, loc *time.Location, max int) ([]CalendarEvent, error) {
	cal, err := s.get(ctx)
	if err != nil {
		return nil, err
	}
	name := textValue(cal.Component, "X-WR-CALNAME")
	if name == "" {
		name = s.name
	}
	return expandCalendar(cal, parseOptions{from: from, to: to, loc: loc, calendar: name, max: max}), nil
}

func (s *icsSource) calendars(ctx context.Context) ([]string, error) {
	cal, err := s.get(ctx)
	if err != nil {
		return nil, err
	}
	if name := textValue(cal.Component, "X-WR-CALNAME"); name != "" {
		return []string{name}, nil
	}
	return []string{s.name}, nil
}

// ---- CalDAV ----

// caldavSource reads the selected calendars of a CalDAV account.
type caldavSource struct {
	url, username, password string
	// selected names calendars by display name or path; empty reads all.
	selected []string
}

func (s *caldavSource) client() (*caldav.Client, error) {
	hc := webdav.HTTPClientWithBasicAuth(httpClient, s.username, s.password)
	c, err := caldav.NewClient(hc, s.url)
	if err != nil {
		return nil, fmt.Errorf("connecting to the CalDAV server: %w", err)
	}
	return c, nil
}

// discover lists the account's calendars. A server that cannot list them,
// or a URL that is itself a calendar, is read as that one calendar.
func (s *caldavSource) discover(ctx context.Context, c *caldav.Client) ([]caldav.Calendar, error) {
	principal, err := c.FindCurrentUserPrincipal(ctx)
	if err == nil {
		home, herr := c.FindCalendarHomeSet(ctx, principal)
		if herr == nil {
			cals, cerr := c.FindCalendars(ctx, home)
			if cerr == nil && len(cals) > 0 {
				return cals, nil
			}
		}
	}
	u, perr := url.Parse(s.url)
	if perr != nil {
		return nil, fmt.Errorf("the CalDAV URL is not valid: %w", perr)
	}
	return []caldav.Calendar{{Path: u.Path, Name: u.Path}}, nil
}

// chosen returns the calendars the connection reads.
func (s *caldavSource) chosen(ctx context.Context, c *caldav.Client) ([]caldav.Calendar, error) {
	all, err := s.discover(ctx, c)
	if err != nil {
		return nil, err
	}
	if len(s.selected) == 0 {
		return all, nil
	}
	var out []caldav.Calendar
	for _, cal := range all {
		for _, want := range s.selected {
			if strings.EqualFold(cal.Name, want) || strings.TrimSuffix(cal.Path, "/") == strings.TrimSuffix(want, "/") {
				out = append(out, cal)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of the selected calendars (%s) exist on the server", strings.Join(s.selected, ", "))
	}
	return out, nil
}

func (s *caldavSource) fetch(ctx context.Context, from, to time.Time, loc *time.Location, max int) ([]CalendarEvent, error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	cals, err := s.chosen(ctx, c)
	if err != nil {
		return nil, err
	}
	var out []CalendarEvent
	for _, cal := range cals {
		objects, err := c.QueryCalendar(ctx, cal.Path, &caldav.CalendarQuery{
			CompRequest: caldav.CalendarCompRequest{Name: "VCALENDAR", AllProps: true, AllComps: true},
			CompFilter: caldav.CompFilter{
				Name:  "VCALENDAR",
				Comps: []caldav.CompFilter{{Name: "VEVENT", Start: from.UTC(), End: to.UTC()}},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("reading calendar %q: %w", cal.Name, err)
		}
		for _, obj := range objects {
			if obj.Data == nil {
				continue
			}
			out = append(out, expandCalendar(obj.Data, parseOptions{from: from, to: to, loc: loc, calendar: cal.Name, max: max})...)
		}
	}
	sortEvents(out)
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func (s *caldavSource) calendars(ctx context.Context) ([]string, error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	cals, err := s.discover(ctx, c)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cals))
	for _, cal := range cals {
		names = append(names, cal.Name)
	}
	return names, nil
}

// ---- Google Calendar ----

// googleEndpoint is the Calendar API base, a variable so tests can point it
// at a local server.
var googleEndpoint = "https://www.googleapis.com/calendar/v3"

// googleSource reads Google calendars by API key or with an access token from
// an oauth2 connection on the host.
type googleSource struct {
	ids    []string
	apiKey string
	// token returns a bearer token; nil for API-key access.
	token func(ctx context.Context) (string, error)
}

type googleEventTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
	TimeZone string `json:"timeZone"`
}

type googleEvent struct {
	ID                string          `json:"id"`
	ICalUID           string          `json:"iCalUID"`
	Status            string          `json:"status"`
	Summary           string          `json:"summary"`
	Location          string          `json:"location"`
	Start             googleEventTime `json:"start"`
	End               googleEventTime `json:"end"`
	RecurringEventID  string          `json:"recurringEventId"`
	OriginalStartTime googleEventTime `json:"originalStartTime"`
}

type googleEventsResponse struct {
	Summary       string        `json:"summary"`
	Items         []googleEvent `json:"items"`
	NextPageToken string        `json:"nextPageToken"`
}

// call performs one authenticated GET against the Calendar API.
func (s *googleSource) call(ctx context.Context, path string, params url.Values, out any) error {
	if s.token == nil {
		if s.apiKey == "" {
			return fmt.Errorf("an API key or an oauth2 connection is required")
		}
		params.Set("key", s.apiKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleEndpoint+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	if s.token != nil {
		tok, err := s.token(ctx)
		if err != nil {
			return fmt.Errorf("getting a token from the oauth2 connection: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling Google Calendar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		if e.Error.Message != "" {
			return fmt.Errorf("google calendar returned HTTP %d: %s", resp.StatusCode, e.Error.Message)
		}
		return fmt.Errorf("google calendar returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxFeedBytes)).Decode(out)
}

// events reads one calendar's occurrences, following pages. singleEvents
// makes Google expand recurrences itself, with cancelled instances removed.
func (s *googleSource) events(ctx context.Context, id string, from, to time.Time, loc *time.Location, max int) (string, []CalendarEvent, error) {
	var out []CalendarEvent
	name := id
	page := ""
	for len(out) < max {
		params := url.Values{
			"timeMin":      {from.UTC().Format(time.RFC3339)},
			"timeMax":      {to.UTC().Format(time.RFC3339)},
			"singleEvents": {"true"},
			"orderBy":      {"startTime"},
			"maxResults":   {"250"},
		}
		if page != "" {
			params.Set("pageToken", page)
		}
		var resp googleEventsResponse
		if err := s.call(ctx, "/calendars/"+url.PathEscape(id)+"/events", params, &resp); err != nil {
			return "", nil, err
		}
		if resp.Summary != "" {
			name = resp.Summary
		}
		for _, item := range resp.Items {
			if strings.EqualFold(item.Status, "cancelled") {
				continue
			}
			start, allDay := googleTime(item.Start, loc)
			end, _ := googleTime(item.End, loc)
			if start.IsZero() {
				continue
			}
			if end.IsZero() {
				end = start
			}
			original := start
			if t, _ := googleTime(item.OriginalStartTime, loc); !t.IsZero() {
				original = t
			}
			uid := item.ICalUID
			if uid == "" {
				uid = item.ID
			}
			out = append(out, CalendarEvent{
				ID: occurrenceID(uid, item.Summary, original), UID: uid, Calendar: name,
				Title: item.Summary, Location: item.Location, AllDay: allDay,
				Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
				Recurring: item.RecurringEventID != "",
			})
		}
		if resp.NextPageToken == "" {
			break
		}
		page = resp.NextPageToken
	}
	return name, out, nil
}

// googleTime reads a Google date or date-time; a date is all-day and starts
// at midnight in loc.
func googleTime(t googleEventTime, loc *time.Location) (time.Time, bool) {
	if t.Date != "" {
		d, err := time.ParseInLocation("2006-01-02", t.Date, loc)
		if err != nil {
			return time.Time{}, false
		}
		return d, true
	}
	if t.DateTime != "" {
		d, err := time.Parse(time.RFC3339, t.DateTime)
		if err != nil {
			return time.Time{}, false
		}
		return d, false
	}
	return time.Time{}, false
}

func (s *googleSource) fetch(ctx context.Context, from, to time.Time, loc *time.Location, max int) ([]CalendarEvent, error) {
	var out []CalendarEvent
	for _, id := range s.ids {
		_, events, err := s.events(ctx, id, from, to, loc, max)
		if err != nil {
			return nil, fmt.Errorf("reading calendar %q: %w", id, err)
		}
		out = append(out, events...)
	}
	sortEvents(out)
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func (s *googleSource) calendars(ctx context.Context) ([]string, error) {
	names := make([]string, 0, len(s.ids))
	now := time.Now()
	for _, id := range s.ids {
		name, _, err := s.events(ctx, id, now, now.Add(time.Minute), time.UTC, 1)
		if err != nil {
			return nil, fmt.Errorf("reading calendar %q: %w", id, err)
		}
		names = append(names, name)
	}
	return names, nil
}
