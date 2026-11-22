package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimeSeriesWriteRejectsAnEmptyPointsArray locks that a payload carrying a
// points array is never replaced by the configured single point. An empty
// array used to fall through to the config path, so a batch that happened to
// be empty wrote one point built from the node's metric and value settings.
func TestTimeSeriesWriteRejectsAnEmptyPointsArray(t *testing.T) {
	ec := newTestCtx(`{"connection":"c","metric":"energy_cost","value":"payload.total"}`, nil)
	in := &engine.Msg{Payload: map[string]any{"points": []any{}, "total": 4.0}}

	_, err := TimeSeriesWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.Error(t, err, "an empty batch has nothing to write")
	assert.Contains(t, err.Error(), "no points to write",
		"the batch decides what is written, so an empty one must not reach the configured point or the connection")
}
