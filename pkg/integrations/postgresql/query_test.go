package postgresql

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertLongFormat pins the long-format convention chart queries
// rely on: the time column is the one
// named time, a text column named metric names each series, other text
// columns become labels, every numeric column is its own series, and rows
// arriving out of order are sorted by time.
func TestConvertLongFormat(t *testing.T) {
	cols := []column{
		{name: "time", kind: tsdb.FieldTime},
		{name: "metric", kind: tsdb.FieldString},
		{name: "room", kind: tsdb.FieldString},
		{name: "value", kind: tsdb.FieldNumber},
	}
	rows := [][]any{
		{int64(2000), "temp", "kitchen", 21.5},
		{int64(1000), "temp", "kitchen", 21.0},
		{int64(1000), "temp", "hall", nil},
		{int64(1000), "humidity", "hall", 40.0},
	}

	got := convert(cols, rows, tsdb.Request{Query: tsdb.Query{RefID: "A"}})
	require.Len(t, got, 3, "one series per metric and label set")
	assert.Equal(t, `temp{room="kitchen"}`, got[0].Name, "metric names the series and labels distinguish it")
	assert.Equal(t, map[string]string{"room": "kitchen"}, got[0].Labels)
	assert.Equal(t, "A", got[0].RefID)
	assert.Equal(t, []any{int64(1000), int64(2000)}, got[0].Fields[0].Values, "rows are sorted by time")
	assert.Equal(t, []any{21.0, 21.5}, got[0].Fields[1].Values)
	assert.Equal(t, []any{nil}, got[1].Fields[1].Values, "a NULL value stays a gap")

	legend := convert(cols, rows, tsdb.Request{Query: tsdb.Query{LegendFormat: "{{metric}} in {{room}}"}})
	assert.Equal(t, "humidity in hall", legend[2].Name, "legend formats see the metric and labels")

	table := convert(cols, rows, tsdb.Request{Query: tsdb.Query{Format: tsdb.FormatTable}})
	require.Len(t, table, 1, "the table format keeps rows as they came")
	assert.Equal(t, []string{tsdb.FieldTime, tsdb.FieldString, tsdb.FieldString, tsdb.FieldNumber},
		[]string{table[0].Fields[0].Type, table[0].Fields[1].Type, table[0].Fields[2].Type, table[0].Fields[3].Type})

	noTime := convert(cols[1:], [][]any{{"temp", "hall", 1.0}}, tsdb.Request{})
	assert.Len(t, noTime[0].Fields, 3, "without a time column the result is a table")
}

// TestCanceledQueryKeepsStatus locks that a query its caller abandoned leaves
// the connection's status alone, while a server that cannot be reached still
// reports the failure. A browser leaving a dashboard cancels the chart
// request, and that used to log the connection as failed and then
// reconnected. Port 1 refuses connections, so the second query needs no
// server.
func TestCanceledQueryKeepsStatus(t *testing.T) {
	cfg, err := json.Marshal(map[string]any{"host": "127.0.0.1", "port": 1, "database": "db"})
	require.NoError(t, err)
	var mu sync.Mutex
	var published []integrations.Status
	d, err := New(integrations.ConnModel{ID: uuid.New(), Name: "pg", Kind: Kind, Config: cfg}, integrations.Deps{
		PublishStatus: func(s integrations.Status) {
			mu.Lock()
			published = append(published, s)
			mu.Unlock()
		},
	})
	require.NoError(t, err)
	t.Cleanup(d.pool.Close)
	before := d.Status().State

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.Query(ctx, tsdb.Request{Query: tsdb.Query{Text: "SELECT 1"}})
	require.Error(t, err, "a canceled query fails for its caller")
	mu.Lock()
	assert.Empty(t, published, "the caller's cancel must not publish a connection status")
	mu.Unlock()
	assert.Equal(t, before, d.Status().State, "the caller's cancel must not change the connection state")

	_, err = d.Query(context.Background(), tsdb.Request{Query: tsdb.Query{Text: "SELECT 1"}})
	require.Error(t, err, "nothing listens on port 1")
	assert.Equal(t, integrations.StateError, d.Status().State, "an unreachable server is the connection's failure")
}
