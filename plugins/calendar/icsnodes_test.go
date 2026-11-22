package main

import (
	"testing"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestICSEventTimesAreAbsolute pins how ics-encode writes a timed event: as a
// UTC instant. The encoder labels any other time with its zone's name, and
// the timestamps flows carry have an offset but no named zone, so the event
// went out with an empty or "Local" TZID that no other reader can resolve and
// landed at the wrong hour.
func TestICSEventTimesAreAbsolute(t *testing.T) {
	stamp := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	wall, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-03-10 09:00:00", time.Local)
	require.NoError(t, err)

	cases := []struct {
		name, start, want string
	}{
		{"offset", "2026-03-10T09:00:00-05:00", "20260310T140000Z"},
		{"odd offset", "2026-03-10T09:00:00+05:45", "20260310T031500Z"},
		{"utc", "2026-03-10T14:00:00Z", "20260310T140000Z"},
		{"server wall clock", "2026-03-10 09:00:00", wall.UTC().Format("20060102T150405Z")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := icsEvent(map[string]any{"title": "Dentist", "start": tc.start}, 0, stamp)
			require.NoError(t, err)
			start := comp.Props.Get(ical.PropDateTimeStart)
			require.NotNil(t, start, "a timed event must carry DTSTART")
			assert.Equal(t, tc.want, start.Value, "the start must be the same instant in UTC")
			assert.Nil(t, start.Params["TZID"], "a UTC time must not name a zone")
			end := comp.Props.Get(ical.PropDateTimeEnd)
			require.NotNil(t, end, "a timed event must carry DTEND")
			assert.Nil(t, end.Params["TZID"], "the end must be written the same way as the start")
		})
	}

	allDay, err := icsEvent(map[string]any{"title": "Holiday", "start": "2026-03-10T00:00:00-05:00",
		"end": "2026-03-11T00:00:00-05:00", "all_day": true}, 0, stamp)
	require.NoError(t, err)
	assert.Equal(t, "20260310", allDay.Props.Get(ical.PropDateTimeStart).Value,
		"an all-day event keeps the calendar date it was given, not the UTC date")
}

// TestICSAllDayEventWithoutEndLastsOneDay pins the end ics-encode gives an
// all-day event that names none: the next date, since DTEND is exclusive and
// must fall after DTSTART. The timed default of one hour wrote DTEND on the
// start date, an empty span that readers reject or show as no event at all.
func TestICSAllDayEventWithoutEndLastsOneDay(t *testing.T) {
	stamp := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	comp, err := icsEvent(map[string]any{"title": "Holiday", "start": "2026-03-10", "all_day": true}, 0, stamp)
	require.NoError(t, err, "an all-day event needs no end")

	assert.Equal(t, "20260310", comp.Props.Get(ical.PropDateTimeStart).Value, "the start date must be the one given")
	end := comp.Props.Get(ical.PropDateTimeEnd)
	require.NotNil(t, end, "an all-day event must carry DTEND")
	assert.Equal(t, "20260311", end.Value, "an all-day event with no end must cover its whole start date")
}

// TestICSResultLeavesTheInputMessageAlone pins that writing a result under a
// nested target copies every object on the way down. A message fans out to
// other branches as the same value, and only the top level was copied, so a
// target of "payload.mail.events" added the key to the object every other
// branch was still reading.
func TestICSResultLeavesTheInputMessageAlone(t *testing.T) {
	mail := map[string]any{"subject": "Invite"}
	in := &engine.Msg{Payload: map[string]any{"mail": mail, "kept": true}}

	out, err := icsResult(in, "payload.mail.attachment", "payload.mail.events", []any{"parsed"})
	require.NoError(t, err, "a nested payload path is a valid target")

	payload, ok := out.Payload.(map[string]any)
	require.True(t, ok, "the result keeps an object payload")
	assert.Equal(t, true, payload["kept"], "fields beside the target must carry over")
	nested, ok := payload["mail"].(map[string]any)
	require.True(t, ok, "the object holding the target must carry over")
	assert.Equal(t, "Invite", nested["subject"], "fields beside the target must carry over at every level")
	assert.Equal(t, []any{"parsed"}, nested["events"], "the result must land at the target")

	assert.Equal(t, map[string]any{"subject": "Invite"}, mail,
		"the input message's own objects must not gain the result")
}
