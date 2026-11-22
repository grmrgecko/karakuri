package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHysteresisTransitions preserves the two-threshold contract across a
// JSON-normalized store reload; readings in the band must not chatter.
func TestHysteresisTransitions(t *testing.T) {
	for _, tc := range []struct {
		name, direction string
		values          []float64
		ports           []string
	}{
		{"high active", "high", []float64{50, 65, 64, 55, 54, 65}, []string{"", "active", "", "inactive", "", "active"}},
		{"low active", "low", []float64{60, 55, 56, 65, 66, 55}, []string{"", "active", "", "inactive", "", "active"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := `{"property":"payload.reading","lower":55,"upper":65,"direction":"` + tc.direction + `","key":"topic"}`
			ec := newTestCtx(cfg, nil)
			for i, value := range tc.values {
				in := &engine.Msg{Payload: map[string]any{"reading": value}, Topic: "room-a", Meta: map[string]any{"request_id": "r"}}
				out, err := (HysteresisNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
				require.NoError(t, err)
				if tc.ports[i] == "" {
					assert.Empty(t, out, "band readings retain the remembered state")
				} else {
					assert.Equal(t, in, only(out, tc.ports[i]), "transitions preserve the message envelope")
				}
				// Reconstruct from the durable JSON shapes instead of sharing live state.
				next := newTestCtx(cfg, nil)
				state, ok := ec.durable.Get("hysteresis")
				if ok {
					next.durable.Set("hysteresis", state)
				}
				ec = next
			}
			out, err := (HysteresisNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"reset": {Topic: "room-a"}})
			require.NoError(t, err)
			assert.Empty(t, out, "reset must not actuate the downstream device")
			activeReading := float64(65)
			if tc.direction == "low" {
				activeReading = 55
			}
			for _, topic := range []string{"room-a", "room-b"} {
				out, err = (HysteresisNode{}).Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: map[string]any{"reading": activeReading}, Topic: topic}})
				require.NoError(t, err)
				assert.NotNil(t, only(out, "active"), "each key has its own inactive baseline")
			}
		})
	}
}
