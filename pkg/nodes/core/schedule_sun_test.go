package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/sunevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// londonSunCfg is a sun schedule for a fixed location, so expected times are
// stable across machines.
const londonSunCfg = `{"mode":"sun","event":"sunset","latitude":51.4769,"longitude":-0.0005}`

// TestSunScheduleTestRunDescribesTheNextEvent covers what a test run produces
// for a sun schedule: the author cannot wait for sunset in the editor, so the
// node describes the next one instead. The expected time is a real astronomical
// fact — sunset over Greenwich on 2024-03-20 is about 18:15 UTC — checked to
// within two minutes because the various definitions of "sunset" differ by
// roughly that much.
func TestSunScheduleTestRunDescribesTheNextEvent(t *testing.T) {
	fc := engine.NewFakeClock(time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC))
	out, err := ScheduleNode{}.Execute(context.Background(), newTestCtx(londonSunCfg, fc), nil)
	require.NoError(t, err, "a test run must describe the next event rather than wait for it")

	msg := only(out, "out")
	require.NotNil(t, msg, "a test run must produce a message for the rest of the graph")

	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "the tick is an object a flow can read fields off")
	assert.Equal(t, "sunset", payload["event"], "the configured event is what the tick reports")

	stamp, ok := payload["event_at"].(string)
	require.Truef(t, ok, "event_at must be a timestamp string, got %T", payload["event_at"])
	eventAt, err := time.Parse(time.RFC3339, stamp)
	require.NoErrorf(t, err, "event_at must be RFC3339 so an expression can parse it: %q", stamp)

	// Sunset over Greenwich on 2024-03-20 is about 18:15 UTC. The tolerance is
	// two minutes because the definitions of sunset differ by roughly that.
	assert.WithinDuration(t, time.Date(2024, 3, 20, 18, 15, 0, 0, time.UTC), eventAt, 2*time.Minute,
		"the described event must be the real sunset for the configured location")
	assert.Equal(t, "sunset", msg.Meta["sun_event"], "the metadata names the event so a shared flow can branch on it")
}

// TestSunScheduleFiresAtTheEvent covers the armed trigger. The wait is computed
// against the same astronomical calculation the node uses rather than a
// hardcoded duration, so the test pins that the node waits *until sunset*
// rather than that it waits some particular number of hours — sunset moves
// every day, and a hardcoded expectation would only be right on one of them.
func TestSunScheduleFiresAtTheEvent(t *testing.T) {
	start := time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC)
	fc := engine.NewFakeClock(start)
	ec := newTestCtx(londonSunCfg, fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fired := make(chan *engine.Msg, 4)
	go func() {
		_ = ScheduleNode{}.Start(ctx, ec, func(msg *engine.Msg) { fired <- msg })
	}()

	// The trigger parks until sunset.
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second),
		"the trigger must wait on the injectable clock, or it cannot be driven by anything but real time")

	sunset, err := sunevent.On(start, sunevent.Options{
		Event: sunevent.Sunset, Latitude: 51.4769, Longitude: -0.0005,
	})
	require.NoError(t, err, "the test computes the expected sunset the same way the node does")
	assert.Equal(t, sunset.Sub(start), fc.AfterCalls()[0],
		"the wait must run until sunset itself, not for some fixed span")

	select {
	case <-fired:
		require.FailNow(t, "the trigger fired before the clock reached sunset")
	default:
	}

	fc.Advance(fc.AfterCalls()[0])
	select {
	case msg := <-fired:
		payload, ok := msg.Payload.(map[string]any)
		require.True(t, ok, "the tick is an object")
		assert.Equal(t, "sunset", payload["event"], "the fired message names the event that fired it")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the trigger never fired once the clock reached sunset")
	}
}

// TestSunScheduleAppliesOffset covers the offset, which is what a real
// automation wants: lights half an hour before sunset rather than at it. A
// negative offset has to move the wait earlier, not later.
func TestSunScheduleAppliesOffset(t *testing.T) {
	start := time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC)
	fc := engine.NewFakeClock(start)
	cfg := `{"mode":"sun","event":"sunset","offset":"-30m","latitude":51.4769,"longitude":-0.0005}`
	ec := newTestCtx(cfg, fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = ScheduleNode{}.Start(ctx, ec, func(*engine.Msg) {})
	}()
	require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second), "the trigger must park on the clock")

	sunset, err := sunevent.On(start, sunevent.Options{
		Event: sunevent.Sunset, Latitude: 51.4769, Longitude: -0.0005,
	})
	require.NoError(t, err, "the test computes the expected sunset the same way the node does")
	assert.Equal(t, sunset.Add(-30*time.Minute).Sub(start), fc.AfterCalls()[0],
		"a negative offset must fire the trigger before the event, which is what makes it useful")
}

// TestSunScheduleUsesConfiguredLocationAsFallback covers the server-wide
// location, which is why it exists: a household is in one place, so an author
// should not have to type coordinates into every sun schedule they build.
func TestSunScheduleUsesConfiguredLocationAsFallback(t *testing.T) {
	prev := config.C
	config.C = &config.Config{Location: config.LocationConfig{Latitude: 51.4769, Longitude: -0.0005}}
	t.Cleanup(func() { config.C = prev })

	fc := engine.NewFakeClock(time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC))
	out, err := ScheduleNode{}.Execute(context.Background(),
		newTestCtx(`{"mode":"sun","event":"sunset"}`, fc), nil)
	require.NoError(t, err, "a node with no coordinates must fall back to the server's location")
	assert.NotNil(t, only(out, "out"), "the fallback location must be enough to compute an event")
}

// TestSunScheduleNeedsCoordinates pairs with the fallback above: with no
// coordinates on the node and none on the server, there is no answer. Defaulting
// to 0,0 would fire the trigger at sunset in the Gulf of Guinea, which is a
// worse outcome than refusing, because the flow would appear to work.
func TestSunScheduleNeedsCoordinates(t *testing.T) {
	prev := config.C
	config.C = &config.Config{}
	t.Cleanup(func() { config.C = prev })

	_, err := ScheduleNode{}.Execute(context.Background(),
		newTestCtx(`{"mode":"sun","event":"sunset"}`, engine.NewFakeClock(time.Now())), nil)
	assert.Error(t, err,
		"with no location anywhere the node must refuse: defaulting to 0,0 would silently schedule the wrong sunset")
}

// TestSunScheduleRejectsBadConfig covers the configs a sun schedule refuses,
// against the working ones above. A schedule that fell back to a default would
// fire at the wrong time every day with nothing to indicate why, so each of
// these has to fail loudly instead.
func TestSunScheduleRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		why  string
	}{
		{
			name: "unknown event", cfg: `{"mode":"sun","event":"moonrise","latitude":51,"longitude":0}`,
			why: "there is no sensible event to substitute",
		},
		{
			name: "latitude out of range", cfg: `{"mode":"sun","event":"sunset","latitude":100,"longitude":0}`,
			why: "latitude runs to 90, so 100 is not a place on Earth",
		},
		{
			name: "unparsable offset", cfg: `{"mode":"sun","event":"sunset","offset":"soonish","latitude":51,"longitude":0}`,
			why: "reading it as zero would silently drop the offset the author asked for",
		},
	}
	for _, c := range cases {
		_, err := ScheduleNode{}.Execute(context.Background(),
			newTestCtx(c.cfg, engine.NewFakeClock(time.Now())), nil)
		assert.Errorf(t, err, "%s must fail rather than fall back: %s", c.name, c.why)
	}
}

// TestCronScheduleStillWorks covers the default mode beside the sun mode above.
// The two share one node, so the check that matters is that a cron tick carries
// no sun fields — a message with an "event" key would let a downstream rule
// match on something the author never configured.
func TestCronScheduleStillWorks(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	out, err := ScheduleNode{}.Execute(context.Background(), newTestCtx(`{"cron":"0 8 * * *"}`, fc), nil)
	require.NoError(t, err, "a plain cron schedule must still execute")

	msg := only(out, "out")
	require.NotNil(t, msg, "a cron test run must produce a tick")
	assert.Equal(t, "0 8 * * *", msg.Meta["cron"], "the tick names the spec that produced it")

	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "the tick is an object")
	assert.NotContains(t, payload, "event",
		"a cron tick must carry no sun fields, or a rule could match on an event that was never configured")
}

// TestResolveCoordinatesPrefersNodeValues covers how the two sources of a
// location combine. The node wins where it has a value, and the server fills in
// only what is missing — per field rather than all or nothing, so a node that
// overrode one coordinate does not silently lose the other.
func TestResolveCoordinatesPrefersNodeValues(t *testing.T) {
	prev := config.C
	config.C = &config.Config{Location: config.LocationConfig{Latitude: 10, Longitude: 20}}
	t.Cleanup(func() { config.C = prev })

	nodeLat, nodeLon := 51.5, -0.1
	lat, lon, err := resolveCoordinates(&nodeLat, &nodeLon)
	require.NoError(t, err, "a fully specified node must resolve")
	assert.Equal(t, 51.5, lat, "the node's own latitude wins over the server's")
	assert.Equal(t, -0.1, lon, "the node's own longitude wins over the server's")

	// One value on the node, the other from config.
	lat, lon, err = resolveCoordinates(&nodeLat, nil)
	require.NoError(t, err, "a partly specified node must still resolve")
	assert.Equal(t, 51.5, lat, "the node's latitude is kept")
	assert.Equal(t, float64(20), lon,
		"the missing longitude falls back per field, so overriding one coordinate does not discard the other")
}

// TestSunScheduleElevationDefault pins the omitted elevation to the field's
// default of 6 degrees. A graph saved over MCP or REST leaves unset fields
// out, and reading that as 0 would fire at the horizon crossing instead. An
// explicit 0 must still mean the horizon.
func TestSunScheduleElevationDefault(t *testing.T) {
	eventAt := func(cfg string) string {
		t.Helper()
		fc := engine.NewFakeClock(time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC))
		out, err := ScheduleNode{}.Execute(context.Background(), newTestCtx(cfg, fc), nil)
		require.NoError(t, err)
		msg := only(out, "out")
		require.NotNil(t, msg)
		return msg.Payload.(map[string]any)["event_at"].(string)
	}
	const base = `{"mode":"sun","event":"elevation","latitude":51.4769,"longitude":-0.0005`
	omitted := eventAt(base + `}`)
	assert.Equal(t, eventAt(base+`,"elevation":6}`), omitted, "an omitted elevation must be the documented default of 6")
	assert.NotEqual(t, eventAt(base+`,"elevation":0}`), omitted, "an explicit 0 is the horizon, not the default")
}

// TestSunScheduleEventDefault pins the omitted event to the field's default of
// sunset. A graph saved over MCP or REST leaves unset fields out, and the node
// refused to arm with an unknown sun event error. A named event is still
// honored.
func TestSunScheduleEventDefault(t *testing.T) {
	event := func(cfg string) any {
		t.Helper()
		fc := engine.NewFakeClock(time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC))
		out, err := ScheduleNode{}.Execute(context.Background(), newTestCtx(cfg, fc), nil)
		require.NoError(t, err, "the schedule must resolve its sun event")
		msg := only(out, "out")
		require.NotNil(t, msg)
		return msg.Payload.(map[string]any)["event"]
	}
	const base = `{"mode":"sun","latitude":51.4769,"longitude":-0.0005`
	assert.Equal(t, "sunset", event(base+`}`), "an omitted event must be the declared default of sunset")
	assert.Equal(t, "sunrise", event(base+`,"event":"sunrise"}`), "a named event is not replaced by the default")
}

// TestScheduleStampsUseTheConfiguredTimeZone locks that a tick's timestamps
// carry the node's time zone. The zone decides when a cron schedule fires and
// is documented as labelling a sun schedule's timestamps, but the stamps were
// written in the server's zone, so a flow reading the hour out of
// scheduled_at saw the server's wall clock instead of the schedule's. A node
// with no zone keeps the clock's own.
func TestScheduleStampsUseTheConfiguredTimeZone(t *testing.T) {
	now := time.Date(2024, 3, 20, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		cfg    string
		field  string
		suffix string
	}{
		{"cron in a zone", `{"cron":"0 8 * * *","location":"Asia/Tokyo"}`, "scheduled_at", "+09:00"},
		{"cron with no zone", `{"cron":"0 8 * * *"}`, "scheduled_at", "Z"},
		{"sun fire time in a zone", `{"mode":"sun","event":"sunset","latitude":51.4769,"longitude":-0.0005,"location":"Asia/Tokyo"}`, "scheduled_at", "+09:00"},
		{"sun event time in a zone", `{"mode":"sun","event":"sunset","latitude":51.4769,"longitude":-0.0005,"location":"Asia/Tokyo"}`, "event_at", "+09:00"},
		{"sun with no zone", londonSunCfg, "event_at", "Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ScheduleNode{}.Execute(context.Background(), newTestCtx(c.cfg, engine.NewFakeClock(now)), nil)
			require.NoError(t, err, "a test run must describe a tick")
			payload, ok := only(out, "out").Payload.(map[string]any)
			require.True(t, ok, "the tick is an object a flow can read fields off")
			stamp, ok := payload[c.field].(string)
			require.Truef(t, ok, "%s must be a timestamp string, got %T", c.field, payload[c.field])
			assert.Truef(t, strings.HasSuffix(stamp, c.suffix),
				"%s must be written in the schedule's zone (%s), got %q", c.field, c.suffix, stamp)
		})
	}

	armed := newTestCtx(`{"cron":"* * * * * *","location":"Asia/Tokyo"}`, nil)
	ticks := make(chan *engine.Msg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = (ScheduleNode{}).Start(ctx, armed, func(m *engine.Msg) { ticks <- m }) }()
	select {
	case m := <-ticks:
		stamp, _ := m.Payload.(map[string]any)["scheduled_at"].(string)
		assert.Truef(t, strings.HasSuffix(stamp, "+09:00"),
			"an armed schedule must stamp its ticks in the same zone a test run shows, got %q", stamp)
	case <-time.After(1500 * time.Millisecond):
		require.FailNow(t, "a per-second schedule did not tick within 1.5 seconds")
	}
}
