package core

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseTimeValueReadsWhatSendersActuallySend locks the timestamp formats
// the dedupe and wait-until nodes accept. Every one of these comes from
// something outside karakuri: RFC 3339 from an API, the SQL-style layouts from
// a database column or device firmware, RFC 1123 from an HTTP header, and epoch
// numbers from MQTT devices, which report them far more often than they report
// a layout.
//
// The seconds-versus-milliseconds split is the part worth pinning. 1e12 seconds
// is the year 33658 and 1e12 milliseconds is 2001, so the threshold cannot be
// hit by a real second-precision timestamp from any direction.
func TestParseTimeValueReadsWhatSendersActuallySend(t *testing.T) {
	utc := time.UTC
	tests := []struct {
		name  string
		value any
		want  time.Time
		why   string
	}{
		{"RFC 3339", "2026-08-31T14:30:00Z", time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "the layout an API returns"},
		{"RFC 3339 with an offset", "2026-08-31T10:30:00-04:00", time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "a zone on the value beats the configured one"},
		{"SQL style", "2026-08-31 14:30:00", time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "a database column or a device with no zone, read in the given location"},
		{"date only", "2026-08-31", time.Date(2026, 8, 31, 0, 0, 0, 0, utc), "midnight in the given location"},
		{"HTTP date", "Mon, 31 Aug 2026 14:30:00 GMT", time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "what a Retry-After or Last-Modified header carries"},
		{"Unix seconds", 1788186600.0, time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "below 1e12, so seconds"},
		{"Unix seconds with a fraction", 1788186600.5, time.Date(2026, 8, 31, 14, 30, 0, 500_000_000, utc), "the fraction is part of the instant, not rounded away"},
		{"Unix milliseconds", 1788186600000.0, time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "at or above 1e12, so milliseconds"},
		{"Unix seconds quoted", "1788186600", time.Date(2026, 8, 31, 14, 30, 0, 0, utc), "a payload that quoted its numbers still reads as epoch time"},
	}

	for _, tc := range tests {
		got, err := parseTimeValue(tc.value, utc)
		require.NoError(t, err, "%s must parse: %s", tc.name, tc.why)
		assert.True(t, got.Equal(tc.want), "%s read as %s, want %s (%s)",
			tc.name, got.Format(time.RFC3339), tc.want.Format(time.RFC3339), tc.why)
	}

	_, err := parseTimeValue("next tuesday", utc)
	assert.Error(t, err, "a value in no recognized layout must fail rather than become some arbitrary instant")
}

// TestWaitUntilParksForTheInstantItIsGiven covers the two outcomes that matter:
// a future instant parks for exactly the gap, minus the offset, and a past one
// emits straight away marked late. The late case is the one a restart produces
// in bulk, since every event that came due while the server was down is behind
// schedule by the time the run resumes, and failing on those would turn a
// reboot into a page of errors.
func TestWaitUntilParksForTheInstantItIsGiven(t *testing.T) {
	now := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)

	t.Run("a future instant is waited out, less the offset", func(t *testing.T) {
		fc := engine.NewFakeClock(now)
		ec := newTestCtx(`{"time":"payload.starts_at","offset":"-40m","location":"UTC"}`, fc)
		in := &engine.Msg{Payload: map[string]any{"starts_at": "2026-08-31T16:00:00Z"}}

		run := runAsync(WaitUntilNode{}, ec, map[string]*engine.Msg{"in": in})
		require.True(t, fc.BlockUntilAfterCalls(1, 2*time.Second),
			"the wait must go through the injectable clock, or it holds its worker slot until the event")
		require.True(t, run.pending(), "the node returned before time moved, so it waited for nothing")

		// 16:00 less the 40m offset is 15:20, which is 80 minutes after 14:00.
		assert.Equal(t, []time.Duration{80 * time.Minute}, fc.AfterCalls(),
			"the offset shifts the target instant, it does not shift the clock")

		fc.Advance(80 * time.Minute)
		out := run.wait(t)
		require.Len(t, out.out["out"], 1, "the message is passed on once the instant arrives")
		msg := out.out["out"][0]
		assert.Equal(t, "2026-08-31T15:20:00Z", msg.Meta["waited_until"],
			"the metadata reports the instant actually waited for, offset included")
		assert.Nil(t, msg.Meta["late"], "an instant waited out in full is not late")
	})

	t.Run("an instant already past emits immediately", func(t *testing.T) {
		fc := engine.NewFakeClock(now)
		ec := newTestCtx(`{"time":"payload.starts_at","location":"UTC"}`, fc)
		in := &engine.Msg{Payload: map[string]any{"starts_at": "2026-08-31T13:00:00Z"}}

		out, err := WaitUntilNode{}.Execute(t.Context(), ec, map[string]*engine.Msg{"in": in})
		require.NoError(t, err, "an event that already happened is not an error, it is late news")
		require.Len(t, out["out"], 1, "the message is still passed on")
		assert.Equal(t, true, out["out"][0].Meta["late"],
			"the flow is told it acted after the fact, since that is what it cannot see from the payload")
		assert.Empty(t, fc.AfterCalls(), "nothing to wait for means no wait at all")
	})

	t.Run("a wait beyond the configured maximum fails", func(t *testing.T) {
		ec := newTestCtx(`{"time":"payload.starts_at","max_wait":"24h","location":"UTC"}`, engine.NewFakeClock(now))
		in := &engine.Msg{Payload: map[string]any{"starts_at": "2027-08-31T14:00:00Z"}}

		_, err := WaitUntilNode{}.Execute(t.Context(), ec, map[string]*engine.Msg{"in": in})
		assert.Error(t, err,
			"a year-long park from a wrong timestamp upstream is the case the maximum exists to catch")
	})
}
