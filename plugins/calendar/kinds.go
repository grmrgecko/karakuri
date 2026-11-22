package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// Calendar connection kinds.
const (
	kindICS    = "calendar-ics"
	kindCalDAV = "calendar-caldav"
	kindGoogle = "calendar-google"
)

// calendarKinds lists the kinds a calendar node or widget can select, in the
// form a connection field's ConnectionKind takes.
const calendarKinds = kindICS + "," + kindCalDAV + "," + kindGoogle

// connConfig is every calendar kind's configuration; each kind reads its own
// fields.
type connConfig struct {
	URL              string  `json:"url"`
	Auth             string  `json:"auth"`
	Username         string  `json:"username"`
	Password         string  `json:"password"`
	Token            string  `json:"token"`
	Calendars        string  `json:"calendars"`
	APIKey           string  `json:"api_key"`
	OAuth2Connection string  `json:"oauth2_connection"`
	Timezone         string  `json:"timezone"`
	WindowDays       float64 `json:"window_days"`
	MaxOccurrences   float64 `json:"max_occurrences"`
}

// list splits a comma-separated field into trimmed, non-empty values.
func list(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// location returns the zone for floating times and all-day events.
func (c connConfig) location() (*time.Location, error) {
	if strings.TrimSpace(c.Timezone) == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(strings.TrimSpace(c.Timezone))
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", c.Timezone)
	}
	return loc, nil
}

// bounds returns the refresh window in days and the occurrence cap.
func (c connConfig) bounds() (int, int) {
	days, max := int(c.WindowDays), int(c.MaxOccurrences)
	if days <= 0 {
		days = defaultWindowDays
	}
	if max <= 0 {
		max = maxOccurrences
	}
	return days, max
}

// sharedFields are the fields every calendar kind ends with.
func sharedFields() []flow.FieldSpec {
	return []flow.FieldSpec{
		{ID: "timezone", Label: "Time zone", Type: flow.FieldTimezone,
			Help: "Zone for times without one and for all-day events. Empty uses the server's."},
		{ID: "window_days", Label: "Days ahead", Type: flow.FieldNumber, Default: defaultWindowDays,
			Help: "How far ahead events are read."},
		{ID: "max_occurrences", Label: "Most occurrences", Type: flow.FieldNumber, Default: maxOccurrences,
			Help: "A refresh reads at most this many occurrences, so a runaway recurring event cannot flood the server."},
	}
}

// calendarKindDefs returns the three kinds, with factories and probes bound
// to the plugin.
func (p *CalendarPlugin) calendarKindDefs() []integrations.ConnectionKind {
	ics := integrations.ConnectionKind{
		Kind: kindICS, Label: "Calendar (ICS feed)",
		ConfigSpec: append([]flow.FieldSpec{
			{ID: "url", Label: "Feed URL", Type: flow.FieldString, Required: true, Placeholder: "https://example.com/calendar.ics",
				Help: "An ICS or webcal address."},
			{ID: "auth", Label: "Authentication", Type: flow.FieldSelect, Default: "none", Options: []flow.Option{
				{Value: "none", Label: "None"}, {Value: "basic", Label: "Username and password"}, {Value: "bearer", Label: "Bearer token"}}},
			{ID: "username", Label: "Username", Type: flow.FieldString, ShowIf: &flow.ShowIf{Field: "auth", Equals: "basic"}},
			{ID: "password", Label: "Password", Type: flow.FieldString, Control: "password", ShowIf: &flow.ShowIf{Field: "auth", Equals: "basic"}},
			{ID: "token", Label: "Token", Type: flow.FieldString, Control: "password", ShowIf: &flow.ShowIf{Field: "auth", Equals: "bearer"}},
		}, sharedFields()...),
		Secrets: []string{"password", "token"},
	}
	caldav := integrations.ConnectionKind{
		Kind: kindCalDAV, Label: "Calendar (CalDAV)",
		ConfigSpec: append([]flow.FieldSpec{
			{ID: "url", Label: "Server URL", Type: flow.FieldString, Required: true, Placeholder: "https://cloud.example.com/remote.php/dav",
				Help: "The account's CalDAV address, or one calendar's address."},
			{ID: "username", Label: "Username", Type: flow.FieldString, Required: true},
			{ID: "password", Label: "Password", Type: flow.FieldString, Control: "password", Required: true,
				Help: "An app password where the server offers them."},
			{ID: "calendars", Label: "Calendars", Type: flow.FieldString, Control: "tokens",
				Help: "Calendar names or paths to read. Empty reads every calendar; Test lists them."},
		}, sharedFields()...),
		Secrets: []string{"password"},
	}
	google := integrations.ConnectionKind{
		Kind: kindGoogle, Label: "Calendar (Google)",
		ConfigSpec: append([]flow.FieldSpec{
			{ID: "calendars", Label: "Calendar IDs", Type: flow.FieldString, Control: "tokens", Required: true,
				Help: "Calendar IDs from each calendar's settings, such as primary or name@group.calendar.google.com."},
			{ID: "auth", Label: "Access", Type: flow.FieldSelect, Default: "api_key", Options: []flow.Option{
				{Value: "api_key", Label: "API key (public calendars)"}, {Value: "oauth2", Label: "OAuth2 connection (private calendars)"}}},
			{ID: "api_key", Label: "API key", Type: flow.FieldString, Control: "password", ShowIf: &flow.ShowIf{Field: "auth", Equals: "api_key"}},
			{ID: "oauth2_connection", Label: "OAuth2 connection", Type: flow.FieldConnection, ConnectionKind: "oauth2",
				ShowIf: &flow.ShowIf{Field: "auth", Equals: "oauth2"},
				Help:   "An oauth2 connection signed in to Google with the calendar.readonly scope."},
		}, sharedFields()...),
		Secrets: []string{"api_key"},
	}
	out := []integrations.ConnectionKind{ics, caldav, google}
	for i := range out {
		kind := out[i].Kind
		out[i].Factory = func(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
			return p.newDriver(model, deps)
		}
		out[i].Validate = func(cfg []byte) error {
			_, err := p.sourceFor(kind, "", cfg)
			return err
		}
		out[i].Probe = func(ctx context.Context, id uuid.UUID, cfg []byte) (string, error) {
			return p.probe(ctx, kind, cfg)
		}
	}
	return out
}

// sourceFor builds the event source for one connection's configuration.
func (p *CalendarPlugin) sourceFor(kind, name string, raw []byte) (eventSource, error) {
	var cfg connConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("invalid calendar configuration: %w", err)
		}
	}
	if _, err := cfg.location(); err != nil {
		return nil, err
	}
	switch kind {
	case kindICS:
		u := strings.TrimSpace(cfg.URL)
		if u == "" {
			return nil, fmt.Errorf("a feed URL is required")
		}
		if strings.HasPrefix(strings.ToLower(u), "webcal://") {
			u = "https://" + u[len("webcal://"):]
		}
		switch cfg.Auth {
		case "", "none":
		case "basic":
			if cfg.Username == "" {
				return nil, fmt.Errorf("a username is required for basic authentication")
			}
		case "bearer":
			if cfg.Token == "" {
				return nil, fmt.Errorf("a token is required for bearer authentication")
			}
		default:
			return nil, fmt.Errorf("unknown authentication %q", cfg.Auth)
		}
		return &icsSource{url: u, auth: cfg.Auth, username: cfg.Username, password: cfg.Password, token: cfg.Token, name: name}, nil
	case kindCalDAV:
		if strings.TrimSpace(cfg.URL) == "" || cfg.Username == "" {
			return nil, fmt.Errorf("a server URL and a username are required")
		}
		return &caldavSource{url: strings.TrimSpace(cfg.URL), username: cfg.Username, password: cfg.Password, selected: list(cfg.Calendars)}, nil
	case kindGoogle:
		ids := list(cfg.Calendars)
		if len(ids) == 0 {
			return nil, fmt.Errorf("at least one calendar ID is required")
		}
		src := &googleSource{ids: ids, apiKey: cfg.APIKey}
		if cfg.Auth == "oauth2" {
			conn := strings.TrimSpace(cfg.OAuth2Connection)
			if conn == "" {
				return nil, fmt.Errorf("choose the oauth2 connection signed in to Google")
			}
			src.token = func(ctx context.Context) (string, error) {
				if p.host == nil {
					return "", fmt.Errorf("the calendar plugin is not initialized")
				}
				return p.host.ConnectionAccessToken(ctx, conn)
			}
		} else if cfg.APIKey == "" {
			return nil, fmt.Errorf("an API key is required for public calendars")
		}
		return src, nil
	}
	return nil, fmt.Errorf("unknown calendar kind %q", kind)
}

// probe reads a configuration's calendars and counts the coming week's
// events, which is what a connection test reports.
func (p *CalendarPlugin) probe(ctx context.Context, kind string, raw []byte) (string, error) {
	src, err := p.sourceFor(kind, "", raw)
	if err != nil {
		return "", err
	}
	var cfg connConfig
	_ = json.Unmarshal(raw, &cfg)
	loc, _ := cfg.location()
	_, max := cfg.bounds()
	names, err := src.calendars(ctx)
	if err != nil {
		return "", err
	}
	now := time.Now()
	events, err := src.fetch(ctx, now, now.AddDate(0, 0, 7), loc, max)
	if err != nil {
		return "calendars: " + strings.Join(names, ", "), err
	}
	label := "calendar"
	if len(names) != 1 {
		label = "calendars"
	}
	return fmt.Sprintf("%d %s (%s); %d events in the next 7 days", len(names), label, strings.Join(names, ", "), len(events)), nil
}
