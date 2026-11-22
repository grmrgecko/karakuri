package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runDedupe feeds one payload through a dedupe node sharing ec's store and
// reports whether it was passed on.
func runDedupe(t *testing.T, ec engine.ExecContext, payload any) bool {
	t.Helper()
	out, err := DedupeNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
		"in": {Payload: payload},
	})
	require.NoError(t, err, "the payload %v is one the configured logic can compare", payload)
	return len(out["out"]) == 1
}

// TestDedupeHistoryForgetsTheOldest locks the bound on the "not seen before"
// logic. The history is what stops a node that has run for a month from holding
// every key it ever saw, and the cost of that bound is that a key which has not
// appeared for a full history's worth of messages passes again. Anyone reading
// the node has to be able to trust that number, so it is pinned here: with room
// for two, seeing a, b, c evicts a, and a then looks new.
func TestDedupeHistoryForgetsTheOldest(t *testing.T) {
	ec := newTestCtx(`{"logic":"new","history":"2"}`, nil)

	assert.True(t, runDedupe(t, ec, "a"), "the first value has not been seen")
	assert.True(t, runDedupe(t, ec, "b"), "a second distinct value has not been seen either")
	assert.False(t, runDedupe(t, ec, "a"), "a is still remembered, so it is a duplicate")
	assert.True(t, runDedupe(t, ec, "c"), "c is new, and admitting it evicts a")
	assert.True(t, runDedupe(t, ec, "a"), "a was forgotten when c pushed it out of a two-value history")
}

// TestDedupeComparisonLogics pins what "higher" and "later" accept and how they
// order it. Both read values that arrived in a message rather than values this
// code produced, so the cases are the shapes a device or an API actually sends:
// a number quoted as a string, and a timestamp as RFC 3339 or as Unix seconds.
// The state is the extreme seen so far, not the previous value, which is why a
// value below an earlier peak is dropped even though it rose from the one
// before it.
func TestDedupeComparisonLogics(t *testing.T) {
	tests := []struct {
		name   string
		cfg    string
		values []any
		want   []bool
		why    string
	}{
		{
			name:   "higher keeps the peak, not the previous value",
			cfg:    `{"logic":"higher"}`,
			values: []any{10.0, 5.0, 12.0, 12.0},
			want:   []bool{true, false, true, false},
			why:    "5 is below the peak of 10, and a repeat of 12 is not higher than 12",
		},
		{
			name:   "higher reads a number that arrived as text",
			cfg:    `{"logic":"higher"}`,
			values: []any{"41.5", "41.5", "42"},
			want:   []bool{true, false, true},
			why:    "a JSON payload that quoted its readings still compares numerically",
		},
		{
			name:   "later orders RFC 3339 timestamps",
			cfg:    `{"logic":"later"}`,
			values: []any{"2026-08-31T10:00:00Z", "2026-08-31T09:00:00Z", "2026-08-31T11:00:00Z"},
			want:   []bool{true, false, true},
			why:    "an out-of-order redelivery must not look like fresh news",
		},
		{
			name:   "later tells apart Unix seconds within one second",
			cfg:    `{"logic":"later"}`,
			values: []any{1756634400.25, 1756634400.75, 1756634400.75, 1756634400.5},
			want:   []bool{true, true, false, false},
			why:    "a recorder or a script clock reports seconds with a fraction, and half a second later is later",
		},
		{
			name:   "later reads Unix seconds",
			cfg:    `{"logic":"later"}`,
			values: []any{1756634400.0, 1756634400.0, 1756638000.0},
			want:   []bool{true, false, true},
			why:    "MQTT devices report epoch seconds far more often than they report a layout",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ec := newTestCtx(tc.cfg, nil)
			for i, v := range tc.values {
				assert.Equal(t, tc.want[i], runDedupe(t, ec, v),
					"value %d (%v) of %q: %s", i, v, tc.name, tc.why)
			}
		})
	}
}

// TestDedupeReadsPersistedShapes locks the durable-store contract from the
// node's side: after a restart the history arrives as decoded JSON — a []any
// of strings for "new", a float64 for "higher", an RFC 3339 string for
// "later" — never the Go values the node computed with. A value the node let
// through before the restart must still count as seen, which is the reason
// the store is durable at all.
func TestDedupeReadsPersistedShapes(t *testing.T) {
	tests := []struct {
		name            string
		cfg             string
		key             string
		stored          any
		blocked, passed any
	}{
		{
			name: "new reads a JSON list of seen values",
			cfg:  `{"logic":"new"}`, key: dedupeSeenKey,
			stored:  []any{"door-open"},
			blocked: "door-open", passed: "door-closed",
		},
		{
			name: "higher reads a JSON number",
			cfg:  `{"logic":"higher"}`, key: dedupeMaxKey,
			stored:  21.5,
			blocked: 21.0, passed: 22.0,
		},
		{
			name: "later reads an RFC 3339 string",
			cfg:  `{"logic":"later"}`, key: dedupeLatestKey,
			stored:  "2026-08-31T10:00:00Z",
			blocked: "2026-08-31T09:00:00Z", passed: "2026-08-31T11:00:00Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ec := newTestCtx(tc.cfg, nil)
			ec.durable.Set(tc.key, tc.stored)
			assert.False(t, runDedupe(t, ec, tc.blocked),
				"%s: a value accounted for before the restart is not news", tc.name)
			assert.True(t, runDedupe(t, ec, tc.passed),
				"%s: a genuinely newer value still passes", tc.name)
		})
	}
}

// TestDedupeExpiryAndReset preserves suppression across JSON-normalized state
// reloads and measures expiry from admission rather than duplicate traffic.
func TestDedupeExpiryAndReset(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, storeKey string
		value, legacy       any
	}{
		{"new value", `{"logic":"new","expiry":"10s"}`, "seen", "event", []string{"event"}},
		{"numeric watermark", `{"logic":"higher","expiry":"10s"}`, "max", float64(42), float64(42)},
		{"date watermark", `{"logic":"later","expiry":"10s"}`, "latest", "2026-10-06T00:00:00Z", "2026-10-06T00:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := engine.NewFakeClock(time.Now())
			ec := newTestCtx(tc.cfg, fc)
			ec.durable.Set(tc.storeKey, tc.legacy)
			msg := &engine.Msg{Payload: tc.value, Meta: map[string]any{"id": "keep"}}
			out, err := (DedupeNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
			require.NoError(t, err)
			assert.Equal(t, msg, only(out, "duplicate"), "legacy history must continue suppressing when expiry is enabled")
			persisted, _ := ec.durable.Get(tc.storeKey)
			ec = newTestCtx(tc.cfg, fc)
			ec.durable.Set(tc.storeKey, persisted)
			fc.Advance(9 * time.Second)
			out, err = (DedupeNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
			require.NoError(t, err)
			assert.Equal(t, msg, only(out, "duplicate"))
			fc.Advance(time.Second)
			out, err = (DedupeNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
			require.NoError(t, err)
			assert.Equal(t, msg, only(out, "out"), "duplicates do not move the expiry boundary")
			out, err = (DedupeNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {}})
			require.NoError(t, err)
			assert.Empty(t, out)
			out, err = (DedupeNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": msg})
			require.NoError(t, err)
			assert.Equal(t, msg, only(out, "out"), "reset clears the comparison baseline")
		})
	}
}

// TestDedupeRecognizesRepeatedBinaryValues locks that a value which is not
// valid UTF-8 is still recognized the second time. History is stored as JSON,
// which rewrites invalid bytes, so the stored key of a JPEG or of Latin-1 text
// never equalled the key of the next identical message and every repeat
// passed as new. A different binary value still passes.
func TestDedupeRecognizesRepeatedBinaryValues(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}
	other := []byte{0xff, 0xd8, 0xff, 0xe1, 0x00, 0x10}
	cases := []struct {
		name          string
		first, second any
	}{
		{"binary payload", jpeg, other},
		{"text that is not UTF-8", "caf\xe9", "caf\xe8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ec := newTestCtx(`{"logic":"new"}`, nil)
			assert.True(t, runDedupe(t, ec, c.first), "the first value has not been seen")
			assert.False(t, runDedupe(t, ec, c.first), "the same bytes again are a duplicate")
			assert.True(t, runDedupe(t, ec, c.second), "different bytes have not been seen")
		})
	}
}
