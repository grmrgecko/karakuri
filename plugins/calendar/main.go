// Command calendar is a karakuri plugin aggregating calendar feeds for
// dashboards and flows. It serves an HTTP API the calendar widget reads, polls
// only the feeds something is watching, and contributes the flow nodes in
// nodes.go so an upcoming event can start a run.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/swaggest/rest/web"

	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk/httpsdk"
)

const (
	defaultPollInterval = 900 // seconds
	defaultWindowDays   = 60
)

func main() { sdk.Serve(&CalendarPlugin{}) }

// CalendarPlugin reads calendars through its connection kinds and serves
// their events to flows and dashboard widgets.
type CalendarPlugin struct {
	host         plugin.HostAPI
	cache        *gocache.Cache
	scheduler    *Scheduler
	service      *web.Service
	pollInterval time.Duration
	windowDays   int

	// drivers holds the running calendar connections by ID. Drivers run in
	// this process, so the plugin's nodes and its HTTP handlers both reach
	// them here.
	driversMu sync.Mutex
	drivers   map[string]*calendarDriver
}

// Manifest identifies the plugin.
func (p *CalendarPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "calendar",
		Version:     "0.1.0",
		Description: "Calendar events for dashboards and flows, from ICS feeds, CalDAV, or Google Calendar.",
		Author:      "James Coleman",
	}
}

// Register contributes the plugin's connection kinds and flow nodes.
func (p *CalendarPlugin) Register(r plugin.Registrar) error {
	for _, kind := range p.calendarKindDefs() {
		r.RegisterConnectionKind(kind)
	}
	r.RegisterNode(&CalendarEventNode{p: p})
	r.RegisterNode(&CalendarEventsNode{p: p})
	r.RegisterNode(&ICSNode{})
	r.RegisterNode(ICSEncodeNode{})
	return nil
}

// SettingsSchema declares the plugin's configuration. Calendars are not here:
// they are connections, which widgets and flow nodes select.
func (p *CalendarPlugin) SettingsSchema() string {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"poll_interval": map[string]any{
				"type":        "number",
				"title":       "Poll interval (seconds)",
				"description": "How often a calendar a dashboard is showing is read again.",
				"default":     defaultPollInterval,
			},
		},
	}
	b, _ := json.Marshal(schema)
	return string(b)
}

// Init reads settings, builds the providers, and registers the provider
// entries and topic subscription polling depends on.
func (p *CalendarPlugin) Init(ctx context.Context, host plugin.HostAPI) error {
	p.host = host

	pollSec := defaultPollInterval
	if val, found, _ := host.GetConfig(ctx, "poll_interval"); found {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			pollSec = n
		}
	}
	p.pollInterval = time.Duration(pollSec) * time.Second

	p.windowDays = defaultWindowDays
	p.cache = gocache.New(20*time.Minute, 30*time.Minute)
	p.scheduler = NewScheduler(host, p, p.cache, p.pollInterval)
	// Update topics are gated on dashboard read, and the per-widget access
	// check happens in the handlers: a topic carries a dashboard ID, and a
	// subscriber must be able to see that dashboard.
	if err := host.RegisterTopicPermission(ctx, "calendar:update:*", "api:dashboards:read"); err != nil {
		return fmt.Errorf("registering topic permission: %w", err)
	}
	if err := host.Subscribe(ctx, "topic:calendar:update:*:count"); err != nil {
		return fmt.Errorf("subscribing to topic counts: %w", err)
	}

	p.service = newService(p)
	log.Printf("calendar: initialized (poll=%ds, window=%dd)", pollSec, p.windowDays)
	return nil
}

// Shutdown stops polling and withdraws what Init registered.
func (p *CalendarPlugin) Shutdown(ctx context.Context) error {
	if p.scheduler != nil {
		p.scheduler.Stop()
	}
	if p.host != nil {
		_ = p.host.UnregisterTopicPermission(ctx, "calendar:update:*")
		_ = p.host.Unsubscribe(ctx, "topic:calendar:update:*:count")
	}
	return nil
}

// HandleHTTP routes a proxied request through the plugin's own API.
func (p *CalendarPlugin) HandleHTTP(_ context.Context, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, error) {
	return httpsdk.ServeHTTP(p.service, req)
}

// OpenAPISpec publishes the plugin's routes in the host's API document.
func (p *CalendarPlugin) OpenAPISpec() string { return httpsdk.OpenAPISpec(p.service) }

// HandleMessage starts and stops polling as dashboards subscribe to and leave
// a widget's update topic.
func (p *CalendarPlugin) HandleMessage(_ context.Context, _, msgType string, payload []byte) error {
	if msgType != "subscription_count" {
		return nil
	}
	var msg struct {
		Topic string `json:"topic"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil
	}

	// Topic form: calendar:update:{dashboardID}:{widgetID}.
	parts := strings.Split(msg.Topic, ":")
	if len(parts) != 4 || parts[0] != "calendar" || parts[1] != "update" {
		return nil
	}
	dashboardID, widgetID := parts[2], parts[3]

	if msg.Count > 0 {
		for _, src := range p.loadSources(context.Background(), dashboardID, widgetID) {
			p.scheduler.EnsurePolling(src, dashboardID, widgetID)
		}
	} else {
		p.scheduler.StopPolling(dashboardID, widgetID)
	}
	return nil
}

// sourcesKey names where one widget's source list is stored. The plugin's own
// namespace is added by the host, so the key starts at the dashboard.
func sourcesKey(dashboardID, widgetID string) string {
	return fmt.Sprintf("%s.%s.sources", dashboardID, widgetID)
}

// loadSources reads a widget's configured calendar feeds.
func (p *CalendarPlugin) loadSources(ctx context.Context, dashboardID, widgetID string) []SourceConfig {
	val, found, err := p.host.GetConfig(ctx, sourcesKey(dashboardID, widgetID))
	if err != nil || !found {
		return nil
	}
	var sources []SourceConfig
	if err := json.Unmarshal([]byte(val), &sources); err != nil {
		return nil
	}
	return sources
}

// fetchAllSources gathers events across a widget's calendars, labelled with
// each source's colour and tag. One failing calendar is skipped rather than
// failing the whole set.
func (p *CalendarPlugin) fetchAllSources(ctx context.Context, sources []SourceConfig, maxAge time.Duration) []CalendarEvent {
	var allEvents []CalendarEvent
	for _, src := range sources {
		events, err := p.sourceEvents(ctx, src, maxAge)
		if err != nil {
			log.Printf("calendar: reading %s: %v", src.Name, err)
			continue
		}
		allEvents = append(allEvents, events...)
	}
	sortEvents(allEvents)
	return allEvents
}

// sourceEvents reads one widget source's occurrences from its connection,
// narrowed to its calendars and coloured with its colour where the event has
// none of its own.
func (p *CalendarPlugin) sourceEvents(ctx context.Context, src SourceConfig, maxAge time.Duration) ([]CalendarEvent, error) {
	d, err := p.driverFor(src.ConnectionID)
	if err != nil {
		return nil, err
	}
	events, _, err := d.Events(ctx, time.Now(), maxAge)
	if err != nil {
		return nil, err
	}
	filter := calendarFilter{Calendars: strings.Join(src.Calendars, ",")}
	out := make([]CalendarEvent, 0, len(events))
	for _, e := range events {
		if !filter.matches(e) {
			continue
		}
		if e.Color == "" {
			e.Color = src.Color
		}
		if src.Tag != "" {
			e.Tag = src.Tag
		}
		out = append(out, e)
	}
	return out, nil
}

// newService builds the calendar plugin's own HTTP API. Access is decided per
// dashboard inside the handlers, since each route names the dashboard whose
// widget configuration it touches.
func newService(p *CalendarPlugin) *web.Service {
	s := httpsdk.NewService("Calendar Plugin API", "0.1.0",
		"Calendar event aggregation endpoints.")

	s.Method(http.MethodGet, "/connections", http.HandlerFunc(p.handleConnections))
	s.Method(http.MethodGet, "/{dashboardID}/{widgetID}/sources", http.HandlerFunc(p.handleGetSources))
	s.Method(http.MethodPost, "/{dashboardID}/{widgetID}/set_source", http.HandlerFunc(p.handleSetSource))
	s.Method(http.MethodGet, "/{dashboardID}/{widgetID}/data", http.HandlerFunc(p.handleGetData))
	s.Method(http.MethodPost, "/{dashboardID}/{widgetID}/refresh", http.HandlerFunc(p.handleRefresh))

	return s
}
