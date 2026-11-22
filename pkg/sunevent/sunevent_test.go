package sunevent

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tolerance is how far a computed event may sit from the published value. The
// NOAA algorithm is good to well under a minute; two minutes leaves room for the
// rounding in the reference tables.
const tolerance = 2 * time.Minute

// checkEvent asserts a computed event lands near the expected UTC clock time.
func checkEvent(t *testing.T, name string, o Options, date, want time.Time) {
	t.Helper()
	got, err := On(date, o)
	require.NoErrorf(t, err, "%s must be computable at this location and date", name)
	assert.WithinDurationf(t, want, got, tolerance,
		"%s must land on the published time; the algorithm is good to well under a minute", name)
}

// TestKnownSunriseSunset checks the solar calculation against published times
// from the NOAA solar calculator — facts about the world rather than about this
// code. The locations are chosen to exercise the parts of the maths that can be
// wrong independently: a longitude west of Greenwich pushes the UTC time past
// midnight, the southern hemisphere inverts the seasons, the equator is the
// degenerate case where the day barely changes length, and the twilight events
// use a different solar elevation from sunrise and sunset.
func TestKnownSunriseSunset(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		lat   float64
		lon   float64
		date  string
		want  string
	}{
		// London, spring equinox.
		{"london sunrise", Sunrise, 51.4769, -0.0005, "2024-03-20", "2024-03-20T06:03:00Z"},
		{"london sunset", Sunset, 51.4769, -0.0005, "2024-03-20", "2024-03-20T18:15:00Z"},
		// New York, summer solstice (longitude west, so UTC times are late).
		{"nyc sunrise", Sunrise, 40.7128, -74.0060, "2024-06-20", "2024-06-20T09:25:00Z"},
		{"nyc sunset", Sunset, 40.7128, -74.0060, "2024-06-20", "2024-06-21T00:31:00Z"},
		// Sydney, southern winter.
		{"sydney sunrise", Sunrise, -33.8688, 151.2093, "2024-06-21", "2024-06-20T21:00:00Z"},
		{"sydney sunset", Sunset, -33.8688, 151.2093, "2024-06-21", "2024-06-21T06:54:00Z"},
		// Quito, on the equator: within a day of the equinox sunrise lands near
		// 06:00 local solar time, which at longitude -78.47 is just after 11:00 UTC.
		{"quito sunrise", Sunrise, -0.1807, -78.4678, "2024-09-22", "2024-09-22T11:03:00Z"},
		// Civil twilight in Denver, about half an hour before a 07:18 local sunrise.
		{"denver dawn", Dawn, 39.7392, -104.9903, "2024-01-15", "2024-01-15T13:50:00Z"},
		{"denver dusk", Dusk, 39.7392, -104.9903, "2024-01-15", "2024-01-16T00:29:00Z"},
	}

	for _, c := range cases {
		date, err := time.Parse("2006-01-02", c.date)
		require.NoErrorf(t, err, "the fixture date %q must parse", c.date)
		want, err := time.Parse(time.RFC3339, c.want)
		require.NoErrorf(t, err, "the fixture time %q must parse", c.want)

		checkEvent(t, c.name, Options{Event: c.event, Latitude: c.lat, Longitude: c.lon}, date, want)
	}
}

// TestSolarNoonIsMidwayBetweenSunriseAndSunset checks an invariant of the sky
// rather than a published number: whatever the date or latitude, the sun is
// highest exactly halfway between rising and setting. It catches an error in
// the equation of time that would shift all three events together and so slip
// past a comparison against a table.
func TestSolarNoonIsMidwayBetweenSunriseAndSunset(t *testing.T) {
	date := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	loc := Options{Latitude: 48.8566, Longitude: 2.3522}

	rise, err := On(date, Options{Event: Sunrise, Latitude: loc.Latitude, Longitude: loc.Longitude})
	require.NoError(t, err, "Paris in May has a sunrise")
	set, err := On(date, Options{Event: Sunset, Latitude: loc.Latitude, Longitude: loc.Longitude})
	require.NoError(t, err, "Paris in May has a sunset")
	noon, err := On(date, Options{Event: SolarNoon, Latitude: loc.Latitude, Longitude: loc.Longitude})
	require.NoError(t, err, "solar noon is defined every day")

	midpoint := rise.Add(set.Sub(rise) / 2)
	assert.WithinDuration(t, midpoint, noon, time.Minute,
		"the sun is highest exactly halfway between rising and setting, whatever the date or latitude")
}

// TestTwilightsOrderThroughTheEvening checks the ordering the twilight
// definitions imply: each is a lower solar elevation than the last, so the
// evening must reach them in that order. A sign error or a swapped elevation
// constant would still produce plausible-looking times and only show up here.
func TestTwilightsOrderThroughTheEvening(t *testing.T) {
	date := time.Date(2024, 4, 10, 0, 0, 0, 0, time.UTC)
	base := Options{Latitude: 52.52, Longitude: 13.405}

	var last time.Time
	for _, ev := range []Event{Sunset, Dusk, NauticalDusk, AstronomicalDusk} {
		o := base
		o.Event = ev
		at, err := On(date, o)
		require.NoErrorf(t, err, "Berlin in April reaches %s", ev)
		if !last.IsZero() {
			assert.Truef(t, at.After(last),
				"%s at %s must come after the previous event at %s: each twilight is a lower sun than the last",
				ev, at.Format(time.RFC3339), last.Format(time.RFC3339))
		}
		last = at
	}
}

// TestPolarDaysHaveNoEvent covers the two dates where the maths has no solution
// because the sky has no event: at Longyearbyen the sun does not rise in
// December and does not set in June. It has to be a typed absence rather than a
// computed time, since the trigonometry would otherwise happily return
// something meaningless — and it is what Next below relies on to keep searching.
func TestPolarDaysHaveNoEvent(t *testing.T) {
	longyearbyen := Options{Latitude: 78.2232, Longitude: 15.6267}

	cases := []struct {
		name  string
		event Event
		date  time.Time
	}{
		{name: "polar night has no sunrise", event: Sunrise, date: time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC)},
		{name: "midnight sun has no sunset", event: Sunset, date: time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		o := longyearbyen
		o.Event = c.event

		_, err := On(c.date, o)
		var noEvent *ErrNoEvent
		assert.Truef(t, errors.As(err, &noEvent),
			"%s: the absence must be typed so a caller can tell it from a failure, got %v", c.name, err)
	}
}

// TestNextSkipsPastAndFindsFollowingDay covers the boundary a schedule sits on
// all day: Next must return today's event right up until it happens, and the
// next day's from the moment it has. One second either side of a real sunrise
// is the tightest statement of that, and getting it wrong means a daily
// schedule either fires twice or skips a day.
func TestNextSkipsPastAndFindsFollowingDay(t *testing.T) {
	o := Options{Event: Sunrise, Latitude: 51.4769, Longitude: -0.0005}

	// Just after one sunrise, Next must return the following day's.
	rise, err := On(time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC), o)
	require.NoError(t, err, "London in March has a sunrise")

	next, err := Next(rise.Add(time.Second), o, 0)
	require.NoError(t, err, "there is always another sunrise at this latitude")
	// Consecutive sunrises are about a day apart, drifting by minutes with the
	// season, so the window is a day either side of exact.
	assert.WithinDuration(t, rise.Add(24*time.Hour), next, time.Hour,
		"one second after a sunrise, the next one is tomorrow's")

	next, err = Next(rise.Add(-time.Second), o, 0)
	require.NoError(t, err, "the imminent sunrise must still be findable")
	assert.Equal(t, rise, next,
		"one second before a sunrise, the next one is that same sunrise: a schedule must not skip the day")
}

// TestNextAppliesOffset covers the offset an automation actually uses — lights
// half an hour before sunset. It is applied to the returned time rather than to
// the search, so a negative offset moves the result earlier by exactly that
// much rather than selecting a different day's event.
func TestNextAppliesOffset(t *testing.T) {
	o := Options{Event: Sunset, Latitude: 51.4769, Longitude: -0.0005}
	from := time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC)

	plain, err := Next(from, o, 0)
	require.NoError(t, err, "the unoffset event must be findable")
	early, err := Next(from, o, -30*time.Minute)
	require.NoError(t, err, "the offset event must be findable")

	assert.Equal(t, 30*time.Minute, plain.Sub(early),
		"the offset must move the same event by exactly that much, not select a different day's")
}

// TestNextKeepsEarlierDaysWithLongOffset locks that Next returns the first
// firing after t whatever the offset. The search started one day back, so with
// an offset past half a day the firing that belongs to an older sunset was
// never looked at and Next answered a day late: 36h after the sunset two days
// ago is this morning, and tomorrow morning came back instead.
func TestNextKeepsEarlierDaysWithLongOffset(t *testing.T) {
	o := Options{Event: Sunset, Latitude: 51.5, Longitude: 0}
	offset := 36 * time.Hour
	sunset, err := On(time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC), o)
	require.NoError(t, err, "London has a sunset in March")
	want := sunset.Add(offset)

	got, err := Next(want.Add(-time.Hour), o, offset)
	require.NoError(t, err, "the event occurs every day at this latitude")
	assert.Equal(t, want, got, "the firing an hour away belongs to the sunset two days back and must not be skipped")
}

// TestNextSkipsPolarNightUntilTheSunReturns is why Next searches forward rather
// than computing one day. Asked for a sunrise during the polar night it has to
// keep going until the sun comes back — two months later — rather than failing,
// or a schedule at that latitude would break for a quarter of the year.
func TestNextSkipsPolarNightUntilTheSunReturns(t *testing.T) {
	o := Options{Event: Sunrise, Latitude: 78.2232, Longitude: 15.6267}
	from := time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC)

	next, err := Next(from, o, 0)
	require.NoError(t, err, "the polar night ends eventually, so the search must not give up")
	require.True(t, next.After(from), "the next event must be in the future, got %s", next)

	// Longyearbyen's sun comes back in the second half of February.
	assert.Equal(t, time.February, next.Month(),
		"the first sunrise after the polar night is in February; got %s", next.Format(time.RFC3339))
	assert.Equal(t, 2025, next.Year(), "the search must cross the year boundary, got %s", next.Format(time.RFC3339))
}

// TestCustomElevationMatchesNamedTwilight ties the general case to a named one.
// Civil dusk is defined as the sun passing 6 degrees below the horizon on the
// way down, so a custom descending crossing at -6 must produce the identical
// time — not merely a close one. Any discrepancy means the named events and the
// custom path are computing through different code.
func TestCustomElevationMatchesNamedTwilight(t *testing.T) {
	date := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	named, err := On(date, Options{Event: Dusk, Latitude: 35.6762, Longitude: 139.6503})
	require.NoError(t, err, "Tokyo in August has a civil dusk")
	custom, err := On(date, Options{
		Event: CustomElevation, Elevation: -6, Morning: false,
		Latitude: 35.6762, Longitude: 139.6503,
	})
	require.NoError(t, err, "the equivalent custom crossing must be computable")

	assert.Equal(t, named, custom,
		"civil dusk is defined as -6 degrees descending, so the two must be the same time, not merely close")
}

// TestValidateRejectsImpossibleOptions covers coordinates and elevations that
// name no point in the sky, against the many real locations computed above.
// Every one of these would still produce a number from the trigonometry, which
// is exactly why they have to be refused before it runs.
func TestValidateRejectsImpossibleOptions(t *testing.T) {
	// A valid location must pass, or the rejections below prove nothing.
	require.NoError(t, Options{Event: Sunrise, Latitude: 51.4769, Longitude: -0.0005}.Validate(),
		"a real location must validate")

	cases := []struct {
		name string
		o    Options
		why  string
	}{
		{name: "latitude too high", o: Options{Event: Sunrise, Latitude: 91}, why: "latitude runs to 90"},
		{name: "longitude too low", o: Options{Event: Sunrise, Longitude: -181}, why: "longitude runs to -180"},
		{name: "unknown event", o: Options{Event: "moonrise"}, why: "this package computes solar events only"},
		{
			name: "elevation out of range", o: Options{Event: CustomElevation, Elevation: 120},
			why: "the sun is never 120 degrees above the horizon",
		},
	}
	for _, c := range cases {
		assert.Errorf(t, c.o.Validate(),
			"%s must be refused: %s, and the trigonometry would return a number regardless", c.name, c.why)
	}
}

// TestParseEventAliases pins the spellings an author may type into a schedule
// node. Case and surrounding whitespace are ignored because the value comes
// from a text field, and civil_dawn is accepted alongside dawn because both
// names are in common use for the same event.
func TestParseEventAliases(t *testing.T) {
	for input, want := range map[string]Event{
		"sunrise":    Sunrise,
		"SUNSET":     Sunset,
		" noon ":     SolarNoon,
		"civil_dawn": Dawn,
		"civil_dusk": Dusk,
	} {
		got, err := ParseEvent(input)
		if assert.NoErrorf(t, err, "%q is a spelling an author may type and must parse", input) {
			assert.Equalf(t, want, got, "ParseEvent(%q) must resolve to the event it names", input)
		}
	}

	_, err := ParseEvent("eclipse")
	assert.Error(t, err, "a name this package cannot compute must be refused rather than defaulting to sunrise")
}

// TestDeclinationRangeOverAYear checks the underlying solar geometry against a
// fact about the planet: the Earth's axial tilt is 23.44 degrees, so the sun's
// declination swings exactly that far either side of the equator over a year.
// It is the one check that would catch a units or scaling error in the
// declination itself, which every event time is computed from.
func TestDeclinationRangeOverAYear(t *testing.T) {
	min, max := math.Inf(1), math.Inf(-1)
	for day := 0; day < 365; day++ {
		date := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, day)
		d := sunDeclination(julianCentury(date))
		min = math.Min(min, d)
		max = math.Max(max, d)
	}
	assert.InDelta(t, 23.44, max, 0.5,
		"the summer solstice reaches the Earth's axial tilt of 23.44 degrees north")
	assert.InDelta(t, -23.44, min, 0.5,
		"the winter solstice reaches the same tilt south")
}

// TestNextFindsTheLastEventBeforeTheSunStopsCrossing locks the events on the
// edge of a polar day, a polar night, and a bright summer night. Each is the
// last or first crossing before the sun stops reaching that elevation for
// weeks, and each sits hours from 12:00 UTC, where the solve used to start.
// The declination at that starting point already ruled the crossing out, so
// the date was reported as having no event and Next skipped it. A schedule set
// to sunrise in Tromsø lost its last firing before the midnight sun.
//
// The expected times are where the solar elevation, computed by the same NOAA
// terms and sampled each minute through 2026, crosses the event's angle.
func TestNextFindsTheLastEventBeforeTheSunStopsCrossing(t *testing.T) {
	cases := []struct {
		name     string
		event    Event
		lat, lon float64
		after    string
		want     string
	}{
		{name: "Tromsø's last sunrise before the midnight sun", event: Sunrise, lat: 69.65, lon: 18.9,
			after: "2026-05-17T12:00:00Z", want: "2026-05-17T22:52:00Z"},
		{name: "Tromsø's first sunset after the midnight sun", event: Sunset, lat: 69.65, lon: 18.9,
			after: "2026-07-25T12:00:00Z", want: "2026-07-25T22:38:00Z"},
		{name: "Ushuaia's first astronomical dusk after its bright summer nights", event: AstronomicalDusk, lat: -54.8, lon: -68.3,
			after: "2026-01-31T12:00:00Z", want: "2026-02-01T04:30:00Z"},
		{name: "Qaanaaq's first civil dawn after the polar night, at solar noon", event: Dawn, lat: 78.2, lon: -68.3,
			after: "2026-01-29T00:00:00Z", want: "2026-01-29T16:42:00Z"},
	}
	for _, c := range cases {
		after, err := time.Parse(time.RFC3339, c.after)
		require.NoError(t, err)
		want, err := time.Parse(time.RFC3339, c.want)
		require.NoError(t, err)
		got, err := Next(after, Options{Event: c.event, Latitude: c.lat, Longitude: c.lon}, 0)
		require.NoErrorf(t, err, "%s must be found", c.name)
		assert.WithinDurationf(t, want, got, tolerance, "%s must not be skipped", c.name)
	}
}
