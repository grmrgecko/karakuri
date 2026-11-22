package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLimitSequenceRenumbersTheSurvivors locks the invariant that makes
// dropping messages safe in the middle of a fan-out. A split announces how many
// parts are coming and every fan-in node downstream waits for that many, so
// releasing three parts of five under the original frame would leave a join
// waiting forever for two messages that were thrown away. The node therefore
// opens its own group whose count is what survived, and renumbers the members
// from zero so the order a join rebuilds is the order they were kept in.
func TestLimitSequenceRenumbersTheSurvivors(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want []any
		why  string
	}{
		{
			name: "first three",
			cfg:  `{"mode":"sequence","keep":"first","limit":"3"}`,
			want: []any{"a", "b", "c"},
			why:  "the head of the group in its original order",
		},
		{
			name: "last three",
			cfg:  `{"mode":"sequence","keep":"last","limit":"3"}`,
			want: []any{"c", "d", "e"},
			why:  "the tail, still in its original order rather than reversed",
		},
		{
			name: "a limit above the group size keeps everything",
			cfg:  `{"mode":"sequence","keep":"first","limit":"99"}`,
			want: []any{"a", "b", "c", "d", "e"},
			why:  "nothing to drop is not an error, and the group passes through intact",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ec := newTestCtx(tc.cfg, nil)
			parts := []any{"a", "b", "c", "d", "e"}

			// Feed the group in one part at a time, the way a split delivers it:
			// each part is its own run and only the last one releases.
			var out map[string][]*engine.Msg
			for i, p := range parts {
				res, err := LimitNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
					"in": {Payload: p, Group: []engine.GroupFrame{{ID: "g1", Index: i, Count: len(parts)}}},
				})
				require.NoError(t, err, "collecting part %d of the group", i)
				if i < len(parts)-1 {
					require.Nil(t, res, "part %d completes nothing, so the node emits nothing yet", i)
					continue
				}
				out = res
			}

			require.Len(t, out["out"], len(tc.want), "%s: %s", tc.name, tc.why)
			for i, msg := range out["out"] {
				assert.Equal(t, tc.want[i], msg.Payload, "survivor %d of %q: %s", i, tc.name, tc.why)

				require.Len(t, msg.Group, 1, "the survivors carry exactly one frame, this node's own")
				frame := msg.Group[0]
				assert.NotEqual(t, "g1", frame.ID, "a fresh group, so the store keyed by the old one is untouched")
				assert.Equal(t, i, frame.Index, "members are renumbered from zero in the order kept")
				assert.Equal(t, len(tc.want), frame.Count,
					"the count is what survived, which is what a join downstream waits for")
			}
		})
	}
}
