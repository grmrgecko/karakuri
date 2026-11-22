package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// listSource serves whatever events the test sets.
type listSource struct {
	mu     sync.Mutex
	events []CalendarEvent
}

func (s *listSource) set(events ...CalendarEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = events
}

func (s *listSource) fetch(context.Context, time.Time, time.Time, *time.Location, int) ([]CalendarEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CalendarEvent(nil), s.events...), nil
}
func (s *listSource) calendars(context.Context) ([]string, error) { return []string{"Family"}, nil }

// triggerCtx is the slice of ExecContext the calendar nodes use, plus the
// trigger recorder.
type triggerCtx struct {
	engine.ExecContext
	cfg     string
	clock   *engine.FakeClock
	durable engine.NodeStore

	mu      sync.Mutex
	skipped []string
}

func (c *triggerCtx) Config(out any) error           { return json.Unmarshal([]byte(c.cfg), out) }
func (c *triggerCtx) Clock() engine.Clock            { return c.clock }
func (c *triggerCtx) DurableStore() engine.NodeStore { return c.durable }
func (c *triggerCtx) Logger() *zap.Logger            { return zap.NewNop() }
func (c *triggerCtx) Skipped(_ *engine.Msg, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.skipped = append(c.skipped, reason)
}

// calendarHarness is a plugin with one calendar connection and a fake clock.
type calendarHarness struct {
	t       *testing.T
	p       *CalendarPlugin
	src     *listSource
	connID  string
	clock   *engine.FakeClock
	durable engine.NodeStore
}

func newCalendarHarness(t *testing.T, start time.Time) *calendarHarness {
	p := &CalendarPlugin{}
	src := &listSource{}
	id := uuid.New()
	p.addDriver(&calendarDriver{id: id, name: "Family", kind: kindICS, src: src, loc: time.UTC, days: 60, max: 5000, p: p,
		deps: integrations.Deps{Logger: zap.NewNop()}})
	return &calendarHarness{t: t, p: p, src: src, connID: id.String(), clock: engine.NewFakeClock(start), durable: engine.NewMemDurableStore()}
}

// armed is one running trigger and what it emitted.
type armed struct {
	ctx    *triggerCtx
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	msgs []*engine.Msg
}

func (a *armed) emitted() []*engine.Msg {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*engine.Msg(nil), a.msgs...)
}

// arm starts a trigger with cfg (the connection is filled in) on the shared
// durable store, and waits for its first wait on the clock.
func (h *calendarHarness) arm(cfg map[string]any) *armed {
	h.t.Helper()
	cfg["connection"] = h.connID
	raw, err := json.Marshal(cfg)
	require.NoError(h.t, err)
	a := &armed{ctx: &triggerCtx{cfg: string(raw), clock: h.clock, durable: h.durable}, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	waiters := h.clock.PendingWaiters()
	go func() {
		defer close(a.done)
		_ = (&CalendarEventNode{p: h.p}).Start(ctx, a.ctx, func(m *engine.Msg) {
			a.mu.Lock()
			a.msgs = append(a.msgs, m)
			a.mu.Unlock()
		})
	}()
	h.waitParked(waiters + 1)
	return a
}

// disarm stops a trigger, as a restart or a redeploy would.
func (h *calendarHarness) disarm(a *armed) {
	h.t.Helper()
	waiters := h.clock.PendingWaiters()
	a.cancel()
	<-a.done
	_ = waiters
}

// waitParked waits until n waiters are pending on the clock.
func (h *calendarHarness) waitParked(n int) {
	h.t.Helper()
	require.Eventually(h.t, func() bool { return h.clock.PendingWaiters() >= n }, 3*time.Second, time.Millisecond,
		"the trigger must wait on the clock")
}

// step advances the clock by d one tick at a time, letting the trigger run
// each tick.
func (h *calendarHarness) step(d, tick time.Duration) {
	h.t.Helper()
	for moved := time.Duration(0); moved < d; moved += tick {
		h.waitParked(1)
		h.clock.Advance(tick)
		// Let the woken loop reach its next wait before moving on.
		require.Eventually(h.t, func() bool { return h.clock.PendingWaiters() >= 1 }, 3*time.Second, time.Millisecond)
	}
}

// ev builds an occurrence.
func ev(id, title string, start, end time.Time) CalendarEvent {
	return CalendarEvent{ID: id, Title: title, Calendar: "Family", Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)}
}

func edgesOf(msgs []*engine.Msg) []string {
	var out []string
	for _, m := range msgs {
		p := m.Payload.(map[string]any)
		out = append(out, p["edge"].(string)+":"+p["event"].(map[string]any)["title"].(string))
	}
	return out
}

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// TestCalendarTriggerEdges covers start and end firing with offsets, title
// and all-day filters, and that each edge fires once.
func TestCalendarTriggerEdges(t *testing.T) {
	h := newCalendarHarness(t, t0)
	h.src.set(
		ev("vac@1", "Vacation", t0.Add(30*time.Minute), t0.Add(60*time.Minute)),
		ev("dent@1", "Dentist", t0.Add(40*time.Minute), t0.Add(50*time.Minute)),
		CalendarEvent{ID: "hol@1", Title: "Vacation day", AllDay: true, Start: t0.Add(45 * time.Minute).Format(time.RFC3339), End: t0.Add(46 * time.Minute).Format(time.RFC3339)},
	)
	a := h.arm(map[string]any{"edges": "both", "start_offset": "-10m", "end_offset": "5m", "match": "vacation", "all_day": "exclude", "interval_s": 60})
	h.step(19*time.Minute, time.Minute)
	assert.Empty(t, a.emitted(), "nothing fires before the start moment")
	h.step(time.Minute, time.Minute)
	assert.Equal(t, []string{"start:Vacation"}, edgesOf(a.emitted()), "the start fires ten minutes early")
	h.step(45*time.Minute, time.Minute)
	assert.Equal(t, []string{"start:Vacation", "end:Vacation"}, edgesOf(a.emitted()),
		"the end fires five minutes late, once; the dentist and the all-day event are filtered out")
	msg := a.emitted()[1]
	assert.Equal(t, "calendar:event", msg.Topic)
	assert.Equal(t, t0.Add(65*time.Minute).Format(time.RFC3339), msg.Payload.(map[string]any)["moment"])
	h.disarm(a)
}

// TestCalendarTriggerSurvivesRestart covers the durable records: a second
// trigger on the same store does not fire what the first fired, and it fires
// the edge still to come.
func TestCalendarTriggerSurvivesRestart(t *testing.T) {
	h := newCalendarHarness(t, t0)
	h.src.set(ev("vac@1", "Vacation", t0.Add(2*time.Minute), t0.Add(4*time.Minute)))
	first := h.arm(map[string]any{"edges": "both", "interval_s": 60})
	h.step(3*time.Minute, time.Minute)
	assert.Equal(t, []string{"start:Vacation"}, edgesOf(first.emitted()))
	h.disarm(first)

	second := h.arm(map[string]any{"edges": "both", "interval_s": 60})
	h.step(2*time.Minute, time.Minute)
	assert.Equal(t, []string{"end:Vacation"}, edgesOf(second.emitted()), "the start is not repeated after the restart")
	h.disarm(second)
}

// TestCalendarTriggerCatchUp covers moments that came due while the trigger
// was down: delivered late by default, dropped beyond grace when configured,
// delivered within grace either way, and never for moments before the first
// arm.
func TestCalendarTriggerCatchUp(t *testing.T) {
	cases := []struct {
		name     string
		cfg      map[string]any
		downtime time.Duration
		fired    bool
		late     bool
	}{
		{"late by default", map[string]any{}, 30 * time.Minute, true, true},
		{"dropped beyond grace", map[string]any{"missed_deadline": "drop", "missed_grace": "5s"}, 30 * time.Minute, false, false},
		{"within grace counts as on time", map[string]any{"missed_deadline": "drop", "missed_grace": "1h"}, 30 * time.Minute, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCalendarHarness(t, t0)
			h.src.set(ev("vac@1", "Vacation", t0.Add(10*time.Minute), t0.Add(24*time.Hour)))
			cfg := map[string]any{"edges": "start", "interval_s": 60}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			first := h.arm(cfg)
			h.step(time.Minute, time.Minute)
			h.disarm(first)
			h.clock.Advance(tc.downtime)

			second := h.arm(cfg)
			if tc.fired {
				require.Len(t, second.emitted(), 1)
				late, ok := second.emitted()[0].Meta["late_ms"]
				assert.Equal(t, tc.late, ok, "only a delivery later than the grace period is marked late")
				if tc.late {
					assert.Equal(t, (21 * time.Minute).Milliseconds(), late)
				}
			} else {
				assert.Empty(t, second.emitted())
				second.ctx.mu.Lock()
				require.Len(t, second.ctx.skipped, 1)
				assert.Contains(t, second.ctx.skipped[0], `missed start of "Vacation"`)
				second.ctx.mu.Unlock()
			}
			h.disarm(second)
		})
	}

	t.Run("before the first arm", func(t *testing.T) {
		h := newCalendarHarness(t, t0)
		h.src.set(ev("past@1", "Already started", t0.Add(-5*time.Minute), t0.Add(time.Hour)))
		a := h.arm(map[string]any{"edges": "start", "interval_s": 60})
		h.step(2*time.Minute, time.Minute)
		assert.Empty(t, a.emitted(), "an event that started before the trigger existed does not fire")
		h.disarm(a)
	})
}

// TestCalendarTriggerRecurringAndMoved covers SC-003 through the parser and
// moved events: a weekly event with a cancelled occurrence fires for each
// remaining occurrence, and an event moved before its moment fires once, at
// the new time.
func TestCalendarTriggerRecurringAndMoved(t *testing.T) {
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	h := newCalendarHarness(t, start)
	feed := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:bins\nSUMMARY:Bins\nDTSTART:20261102T090000Z\nDTEND:20261102T091500Z\nRRULE:FREQ=WEEKLY;COUNT=3\nEXDATE:20261109T090000Z\nEND:VEVENT\nEND:VCALENDAR\n"
	events, err := parseCalendar(strings.NewReader(feed), parseOptions{from: start.AddDate(0, 0, -1), to: start.AddDate(0, 0, 30), loc: time.UTC})
	require.NoError(t, err)
	h.src.set(events...)
	a := h.arm(map[string]any{"edges": "start", "interval_s": 3600})
	h.step(21*24*time.Hour, 6*time.Hour)
	assert.Equal(t, []string{"start:Bins", "start:Bins"}, edgesOf(a.emitted()), "11-02 and 11-16 fire; 11-09 was cancelled")
	h.disarm(a)

	h2 := newCalendarHarness(t, t0)
	h2.src.set(ev("move@1", "Meeting", t0.Add(10*time.Minute), t0.Add(time.Hour)))
	b := h2.arm(map[string]any{"edges": "start", "interval_s": 60})
	h2.step(5*time.Minute, time.Minute)
	h2.src.set(ev("move@1", "Meeting", t0.Add(20*time.Minute), t0.Add(time.Hour)))
	h2.step(10*time.Minute, time.Minute)
	assert.Empty(t, b.emitted(), "the old time no longer fires")
	h2.step(6*time.Minute, time.Minute)
	assert.Equal(t, []string{"start:Meeting"}, edgesOf(b.emitted()), "the new time fires once")
	h2.src.set()
	h2.step(time.Minute, time.Minute)
	h2.disarm(b)
}

// TestCalendarLookup covers the lookup's in-progress and upcoming lists, the
// limit, filters, and an empty calendar.
func TestCalendarLookup(t *testing.T) {
	h := newCalendarHarness(t, t0)
	h.src.set(
		ev("now@1", "Vacation", t0.Add(-time.Hour), t0.Add(time.Hour)),
		ev("ended@1", "Breakfast", t0.Add(-2*time.Hour), t0),
		ev("soon@1", "Lunch", t0.Add(4*time.Hour), t0.Add(5*time.Hour)),
		ev("later@1", "Dinner", t0.Add(10*time.Hour), t0.Add(11*time.Hour)),
		ev("far@1", "Next month", t0.AddDate(0, 1, 0), t0.AddDate(0, 1, 0).Add(time.Hour)),
	)
	run := func(cfg map[string]any) map[string]any {
		cfg["connection"] = h.connID
		raw, _ := json.Marshal(cfg)
		out, err := (&CalendarEventsNode{p: h.p}).Execute(context.Background(), &triggerCtx{cfg: string(raw), clock: h.clock, durable: h.durable}, nil)
		require.NoError(t, err)
		return out["out"][0].Payload.(map[string]any)
	}
	titles := func(list any) []string {
		var out []string
		for _, e := range list.([]map[string]any) {
			out = append(out, e["title"].(string))
		}
		return out
	}

	got := run(map[string]any{"window_days": 1})
	assert.Equal(t, []string{"Vacation"}, titles(got["in_progress"]), "an event ending now is over")
	assert.Equal(t, []string{"Lunch", "Dinner"}, titles(got["upcoming"]))

	got = run(map[string]any{"window_days": 1, "limit": 1, "match": "lunch"})
	assert.Empty(t, got["in_progress"])
	assert.Equal(t, []string{"Lunch"}, titles(got["upcoming"]))

	h.src.set()
	h.clock.Advance(2 * time.Minute)
	got = run(map[string]any{})
	assert.Empty(t, got["in_progress"], "an empty calendar is empty lists, not a failure")
	assert.Empty(t, got["upcoming"])

	_, err := (&CalendarEventsNode{p: h.p}).Execute(context.Background(),
		&triggerCtx{cfg: `{"connection":"` + uuid.NewString() + `"}`, clock: h.clock, durable: h.durable}, nil)
	assert.ErrorContains(t, err, "is not running")
}
