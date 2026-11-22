package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// folderCalendar is where the plugin's nodes sit in the palette.
const folderCalendar = "Calendar"

// calendarFilter is the configuration both calendar nodes filter with.
type calendarFilter struct {
	Connection string `json:"connection"`
	Calendars  string `json:"calendars"`
	Match      string `json:"match"`
	AllDay     string `json:"all_day"`
}

// filterFields are the fields both calendar nodes share.
func filterFields() []engine.FieldSpec {
	return []engine.FieldSpec{
		{ID: "connection", Label: "Calendar", Type: flow.FieldConnection, ConnectionKind: calendarKinds, Required: true,
			Help: "An ICS, CalDAV, or Google calendar connection."},
		{ID: "calendars", Label: "Only calendars", Type: flow.FieldString, Control: "tokens",
			Help: "Calendar names to include, for a connection that reads several. Empty includes all."},
		{ID: "match", Label: "Title contains", Type: flow.FieldString,
			Help: "Only events whose title contains this text. Empty matches every event."},
		{ID: "all_day", Label: "All-day events", Type: flow.FieldSelect, Default: "include", Options: []engine.Option{
			{Value: "include", Label: "Include"}, {Value: "exclude", Label: "Exclude"}, {Value: "only", Label: "Only all-day events"}}},
	}
}

// matches reports whether an occurrence passes the filter.
func (f calendarFilter) matches(e CalendarEvent) bool {
	switch f.AllDay {
	case "exclude":
		if e.AllDay {
			return false
		}
	case "only":
		if !e.AllDay {
			return false
		}
	}
	if m := strings.ToLower(strings.TrimSpace(f.Match)); m != "" && !strings.Contains(strings.ToLower(e.Title), m) {
		return false
	}
	if cals := list(f.Calendars); len(cals) > 0 {
		for _, c := range cals {
			if strings.EqualFold(c, e.Calendar) {
				return true
			}
		}
		return false
	}
	return true
}

// eventPayload flattens one occurrence for a flow message.
func eventPayload(e CalendarEvent) map[string]any {
	out := map[string]any{
		"id":       e.ID,
		"title":    e.Title,
		"start":    e.Start,
		"end":      e.End,
		"all_day":  e.AllDay,
		"location": e.Location,
	}
	if e.UID != "" {
		out["uid"] = e.UID
	}
	if e.Calendar != "" {
		out["calendar"] = e.Calendar
	}
	if e.Color != "" {
		out["color"] = e.Color
	}
	if e.Recurring {
		out["recurring"] = true
	}
	return out
}

// ---- calendar-event ----

// CalendarEventNode starts a run at events' starts and ends, so a flow can
// put the house in vacation mode for exactly as long as the vacation.
type CalendarEventNode struct{ p *CalendarPlugin }

func (CalendarEventNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "calendar-event",
		Label:             "Calendar Event",
		Category:          flow.CategoryTrigger,
		Folder:            folderCalendar,
		Description:       "Starts a run when events start or end, with an optional offset",
		Icon:              "las la-calendar-check",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: append(append(filterFields(),
			engine.FieldSpec{ID: "edges", Label: "Fire at", Type: flow.FieldSelect, Default: "start", Options: []engine.Option{
				{Value: "start", Label: "Event start"}, {Value: "end", Label: "Event end"}, {Value: "both", Label: "Start and end"}}},
			engine.FieldSpec{ID: "start_offset", Label: "Start offset", Type: flow.FieldDuration, Placeholder: "-10m",
				ShowIf: &flow.ShowIf{Field: "edges", Equals: []string{"start", "both"}},
				Help:   "Negative fires before the start, positive after. Empty fires at the start."},
			engine.FieldSpec{ID: "end_offset", Label: "End offset", Type: flow.FieldDuration, Placeholder: "0s",
				ShowIf: &flow.ShowIf{Field: "edges", Equals: []string{"end", "both"}},
				Help:   "Negative fires before the end, positive after. Empty fires at the end."},
			engine.FieldSpec{ID: "interval_s", Label: "Refresh interval (seconds)", Type: flow.FieldNumber, Default: 300,
				Help: "How often the calendar is read again. Firing follows the clock, not this; a moved event is seen within one interval."},
		), engine.MissedDeadlineFields()...),
	}
}

// eventTriggerConfig is the decoded trigger configuration.
type eventTriggerConfig struct {
	calendarFilter
	Edges          string  `json:"edges"`
	StartOffset    string  `json:"start_offset"`
	EndOffset      string  `json:"end_offset"`
	IntervalS      float64 `json:"interval_s"`
	MissedDeadline string  `json:"missed_deadline"`
	MissedGrace    string  `json:"missed_grace"`
}

// Execute passes the run's seed message through, like every trigger.
func (CalendarEventNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{}}
	}
	return engine.One("out", msg), nil
}

// Durable store keys of the trigger.
const (
	firedKey     = "fired"
	seenUntilKey = "seen_until"
	// catchUpLimit bounds how far back moments missed while the server was
	// down are looked for, and how long fired records are kept.
	catchUpLimit = 7 * 24 * time.Hour
)

// moment is one edge of one occurrence, at its offset.
type moment struct {
	at    time.Time
	edge  string
	event CalendarEvent
}

// firedID names one occurrence's edge in the fired records.
func (m moment) firedID() string { return m.event.ID + "#" + m.edge }

// Start reads the calendar on an interval and fires each occurrence's edges
// as their moments arrive. Fired occurrences are recorded in the durable
// store before the run starts, so a restart or a redeploy fires none of them
// twice; moments that came due while the server was down are caught up from
// the stored watermark, up to a week back, and follow the missed-deadline
// setting. A moment before the trigger was first armed never fires.
func (n *CalendarEventNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg eventTriggerConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	startOffset, err := parseOffset(cfg.StartOffset)
	if err != nil {
		return fmt.Errorf("invalid start offset: %w", err)
	}
	endOffset, err := parseOffset(cfg.EndOffset)
	if err != nil {
		return fmt.Errorf("invalid end offset: %w", err)
	}
	interval := time.Duration(cfg.IntervalS * float64(time.Second))
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	tick := interval
	if tick > time.Minute {
		tick = time.Minute
	}
	grace := 30 * time.Second
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.MissedGrace)); err == nil && d >= 0 {
		grace = d
	}
	drop := cfg.MissedDeadline == "drop"
	store := ec.DurableStore()

	armedAt := ec.Clock().Now()
	from := armedAt
	if seen, ok := storedTimeValue(store, seenUntilKey); ok && seen.Before(armedAt) {
		from = seen
		if earliest := armedAt.Add(-catchUpLimit); from.Before(earliest) {
			from = earliest
		}
	}
	store.Set(seenUntilKey, armedAt.Format(time.RFC3339Nano))

	for {
		now := ec.Clock().Now()
		events, staleSince, err := n.readEvents(ctx, cfg.Connection, now, interval)
		if err != nil {
			// A trigger has no error branch; the next tick retries, and the
			// connection's status shows the failure.
			ec.Logger().Debug("calendar trigger read failed")
		} else {
			var due []moment
			for _, e := range events {
				if !cfg.matches(e) {
					continue
				}
				if cfg.Edges != "end" {
					due = append(due, moment{at: e.startTime().Add(startOffset), edge: "start", event: e})
				}
				if cfg.Edges == "end" || cfg.Edges == "both" {
					due = append(due, moment{at: e.endTime().Add(endOffset), edge: "end", event: e})
				}
			}
			sort.SliceStable(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
			for _, m := range due {
				if !m.at.After(from) || m.at.After(now) {
					continue
				}
				if !claimFired(store, m.firedID(), m.at) {
					continue
				}
				msg := triggerMessage(m, now, staleSince)
				late := now.Sub(m.at)
				if m.at.Before(armedAt) && late > grace {
					if drop {
						if rec, ok := ec.(engine.TriggerRecorder); ok {
							rec.Skipped(msg, fmt.Sprintf("missed %s of %q at %s by %s; dropped",
								m.edge, m.event.Title, m.at.Format(time.RFC3339), late.Round(time.Second)))
						}
						continue
					}
					msg.Meta["late_ms"] = late.Milliseconds()
				}
				emit(msg)
			}
			from = now
			store.Set(seenUntilKey, now.Format(time.RFC3339Nano))
			pruneFired(store, now.Add(-catchUpLimit))
		}

		select {
		case <-ec.Clock().After(tick):
		case <-ctx.Done():
			return nil
		}
	}
}

// readEvents reads a connection's occurrences, reusing a list read within
// maxAge.
func (n *CalendarEventNode) readEvents(ctx context.Context, connection string, now time.Time, maxAge time.Duration) ([]CalendarEvent, time.Time, error) {
	d, err := n.p.driverFor(connection)
	if err != nil {
		return nil, time.Time{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return d.Events(rctx, now, maxAge)
}

// triggerMessage builds the run's message for one moment.
func triggerMessage(m moment, now, staleSince time.Time) *engine.Msg {
	msg := &engine.Msg{
		Topic: "calendar:event",
		Payload: map[string]any{
			"edge":     m.edge,
			"event":    eventPayload(m.event),
			"moment":   m.at.Format(time.RFC3339),
			"fired_at": now.Format(time.RFC3339),
		},
		Meta: map[string]any{},
	}
	if !staleSince.IsZero() {
		msg.Meta["stale_since"] = staleSince.Format(time.RFC3339)
	}
	return msg
}

// claimFired records an edge as fired, reporting false when it already was.
// The record is written before the run starts, so a crash loses a firing
// rather than repeating it.
func claimFired(store engine.NodeStore, id string, at time.Time) bool {
	claimed := false
	store.Update(firedKey, func(cur any, _ bool) any {
		// A remote store reruns fn after a lost compare-and-swap, so the
		// result of an earlier attempt must not carry over.
		claimed = false
		fired, _ := cur.(map[string]any)
		if fired == nil {
			fired = map[string]any{}
		}
		if _, done := fired[id]; done {
			return fired
		}
		fired[id] = at.Format(time.RFC3339)
		claimed = true
		return fired
	})
	return claimed
}

// pruneFired forgets records of moments before cutoff, which can no longer
// fire.
func pruneFired(store engine.NodeStore, cutoff time.Time) {
	store.Update(firedKey, func(cur any, ok bool) any {
		fired, _ := cur.(map[string]any)
		if !ok || fired == nil {
			return cur
		}
		for id, v := range fired {
			s, _ := v.(string)
			if t, err := time.Parse(time.RFC3339, s); err != nil || t.Before(cutoff) {
				delete(fired, id)
			}
		}
		return fired
	})
}

// storedTimeValue reads a time stored as RFC 3339 text.
func storedTimeValue(store engine.NodeStore, key string) (time.Time, bool) {
	v, ok := store.Get(key)
	if !ok {
		return time.Time{}, false
	}
	s, _ := v.(string)
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// parseOffset reads a signed duration; empty is zero.
func parseOffset(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// ---- calendar-events ----

// CalendarEventsNode is a read-only lookup of what is on a calendar now and
// next: no input port, one read per run however many consumers pull it.
type CalendarEventsNode struct{ p *CalendarPlugin }

func (CalendarEventsNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "calendar-events",
		Label:             "Calendar Events",
		Folder:            folderCalendar,
		Description:       "Events in progress now and coming up on a calendar",
		Icon:              "las la-calendar",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: append(filterFields(),
			engine.FieldSpec{ID: "window_days", Label: "Days ahead", Type: flow.FieldNumber, Default: 7,
				Help: "How far ahead upcoming events are listed."},
			engine.FieldSpec{ID: "limit", Label: "Most upcoming events", Type: flow.FieldNumber, Default: 0,
				Help: "Caps the upcoming list. Zero lists them all."},
		),
	}
}

// eventsLookupConfig is the decoded lookup configuration.
type eventsLookupConfig struct {
	calendarFilter
	WindowDays float64 `json:"window_days"`
	Limit      int     `json:"limit"`
}

// Execute lists the events in progress and the upcoming ones.
func (n *CalendarEventsNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg eventsLookupConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	d, err := n.p.driverFor(cfg.Connection)
	if err != nil {
		return nil, err
	}
	now := ec.Clock().Now()
	events, staleSince, err := d.Events(ctx, now, time.Minute)
	if err != nil {
		return nil, err
	}
	window := cfg.WindowDays
	if window <= 0 {
		window = 7
	}
	horizon := now.Add(time.Duration(window * float64(24*time.Hour)))

	inProgress := []map[string]any{}
	upcoming := []map[string]any{}
	for _, e := range events {
		if !cfg.matches(e) {
			continue
		}
		start, end := e.startTime(), e.endTime()
		switch {
		case !start.After(now) && end.After(now):
			inProgress = append(inProgress, eventPayload(e))
		case start.After(now) && start.Before(horizon):
			if cfg.Limit <= 0 || len(upcoming) < cfg.Limit {
				upcoming = append(upcoming, eventPayload(e))
			}
		}
	}
	msg := &engine.Msg{Topic: "calendar:events", Payload: map[string]any{"in_progress": inProgress, "upcoming": upcoming}}
	if !staleSince.IsZero() {
		msg.Meta = map[string]any{"stale_since": staleSince.Format(time.RFC3339)}
	}
	return engine.One("out", msg), nil
}
