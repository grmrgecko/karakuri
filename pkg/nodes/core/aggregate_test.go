package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAggregateGroupsByAField is the difference between "the average
// temperature" and "the average temperature per room", which is the version
// anyone actually asks for. It pins the shape of a grouped result, because that
// shape is what every downstream path is written against: a list of objects,
// each carrying the operations plus the key it was grouped by, named for the
// last segment of the path rather than the whole dotted thing.
//
// Bucket order is first-seen rather than sorted, so the same readings summarize
// to the same document twice running. Go map iteration would not.
func TestAggregateGroupsByAField(t *testing.T) {
	readings := []struct {
		room string
		temp float64
	}{
		{"lounge", 20},
		{"kitchen", 24},
		{"lounge", 22},
		{"kitchen", 26},
		{"lounge", 21},
	}

	ec := newTestCtx(`{"property":"payload.temp","group_by":"payload.room",
		"operations":"count,avg,max","count":"5"}`, nil)

	var out map[string][]*engine.Msg
	for i, r := range readings {
		res, err := AggregateNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{
			"in": {Payload: map[string]any{"room": r.room, "temp": r.temp}},
		})
		require.NoError(t, err, "reading %d is an ordinary message", i)
		if i < len(readings)-1 {
			require.Nil(t, res, "the group is not closed until the fifth message arrives")
			continue
		}
		out = res
	}

	require.Len(t, out["out"], 1, "a closed group summarizes to one message")
	groups, ok := out["out"][0].Payload.([]any)
	require.True(t, ok, "grouping makes the payload a list, not %T", out["out"][0].Payload)
	require.Len(t, groups, 2, "two distinct rooms means two buckets")

	lounge := groups[0].(map[string]any)
	assert.Equal(t, "lounge", lounge["room"],
		"buckets come back in first-seen order, and the key is named for the last path segment")
	assert.Equal(t, 3, lounge["count"], "three lounge readings")
	assert.Equal(t, float64(21), lounge["avg"], "(20+22+21)/3 is 21")
	assert.Equal(t, float64(22), lounge["max"], "the highest of the three")

	kitchen := groups[1].(map[string]any)
	assert.Equal(t, "kitchen", kitchen["room"])
	assert.Equal(t, 2, kitchen["count"], "two kitchen readings")
	assert.Equal(t, float64(25), kitchen["avg"], "(24+26)/2 is 25, so the rooms did not bleed into each other")
}
