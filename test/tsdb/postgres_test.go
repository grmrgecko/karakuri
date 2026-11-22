//go:build karakuri_tsdb

package tsdbtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/postgresql"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// postgresConfig is the TimescaleDB container from compose.yaml.
func postgresConfig(table string) map[string]any {
	return map[string]any{
		"host": "localhost", "port": 19432, "database": "karakuri",
		"username": "karakuri", "password": "karakuri-test", "sslmode": "disable",
		"write_enabled": true, "table": table,
	}
}

func postgresDriver(t *testing.T, cfg map[string]any) *postgresql.Driver {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	d, err := postgresql.New(integrations.ConnModel{ID: uuid.New(), Kind: postgresql.Kind, Config: raw},
		integrations.Deps{Logger: zap.NewNop(), PublishStatus: func(integrations.Status) {}})
	require.NoError(t, err)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	return d
}

func postgresQuery(t *testing.T, d *postgresql.Driver, sql, format string) *tsdb.Result {
	t.Helper()
	res, err := d.Query(context.Background(), tsdb.Request{Query: tsdb.Query{
		RefID: "A", Language: tsdb.LangSQL, Text: sql, Format: format,
	}})
	require.NoError(t, err, sql)
	return res
}

// TestPostgres writes history into TimescaleDB, where the table must become
// a hypertable, replays a batch to prove ON CONFLICT DO NOTHING keeps rows
// unique, reads the rows back as series and as a table, lists metadata, and
// checks that the read-only transaction refuses an INSERT sent as a query.
func TestPostgres(t *testing.T) {
	ctx := context.Background()
	table := fmt.Sprintf("karakuri_history_test_%d", time.Now().UnixNano())
	cfg := postgresConfig(table)
	raw, _ := json.Marshal(cfg)
	require.NoError(t, postgresql.KindDef().Test(ctx, uuid.New(), raw), "the connection test passes against the container")

	d := postgresDriver(t, cfg)
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), "postgres://karakuri:karakuri-test@localhost:19432/karakuri?sslmode=disable")
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		_, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
	})

	base := time.Now().Truncate(time.Second).Add(-time.Minute).UnixMilli()
	metric := "sensor.pg_test_temp"
	labels := map[string]string{"area": "kitchen", "entity_id": metric}
	points := []tsdb.Point{
		{Time: base, Metric: metric, Labels: labels, Value: tsdb.Float(20.5)},
		{Time: base + 1000, Metric: metric, Labels: labels, Value: tsdb.Float(21)},
		{Time: base + 1000, Metric: "light.pg_test", Labels: map[string]string{"entity_id": "light.pg_test"}, State: tsdb.Text("on")},
	}
	require.NoError(t, d.Write(ctx, points))
	require.NoError(t, d.Write(ctx, points), "a replayed batch is accepted")

	res := postgresQuery(t, d, fmt.Sprintf(
		`SELECT count(*) AS n FROM timescaledb_information.hypertables WHERE hypertable_name = '%s'`, table), tsdb.FormatTable)
	assert.Equal(t, []any{1.0}, res.Series[0].Fields[0].Values, "the history table is a hypertable")

	res = postgresQuery(t, d, fmt.Sprintf(`SELECT count(*) AS n FROM %s`, table), tsdb.FormatTable)
	assert.Equal(t, []any{3.0}, res.Series[0].Fields[0].Values, "the replayed batch added no rows")

	res = postgresQuery(t, d, fmt.Sprintf(
		`SELECT time, metric, labels->>'area' AS area, value FROM %s WHERE value IS NOT NULL ORDER BY time DESC`, table), "")
	require.Len(t, res.Series, 1)
	s := res.Series[0]
	assert.Equal(t, `sensor.pg_test_temp{area="kitchen"}`, s.Name)
	assert.Equal(t, "A", s.RefID)
	assert.Equal(t, []any{base, base + 1000}, s.Fields[0].Values, "series come back sorted by time")
	assert.Equal(t, []any{20.5, 21.0}, s.Fields[1].Values)

	res = postgresQuery(t, d, fmt.Sprintf(`SELECT time, metric, state FROM %s WHERE state IS NOT NULL`, table), "")
	require.Len(t, res.Series, 1, "rows without a numeric column are a table")
	assert.Equal(t, []any{"on"}, res.Series[0].Fields[2].Values)
	assert.Equal(t, tsdb.FieldTime, res.Series[0].Fields[0].Type)

	tables, err := d.Metadata(ctx, tsdb.MetaRequest{Kind: tsdb.MetaTables})
	require.NoError(t, err)
	assert.Contains(t, tables, table)
	columns, err := d.Metadata(ctx, tsdb.MetaRequest{Kind: tsdb.MetaColumns, Table: "public." + table})
	require.NoError(t, err)
	assert.Equal(t, []string{"labels", "labels_hash", "metric", "state", "time", "value"}, columns)
	schemas, err := d.Metadata(ctx, tsdb.MetaRequest{Kind: tsdb.MetaSchemas})
	require.NoError(t, err)
	assert.Contains(t, schemas, "public")

	_, err = d.Query(ctx, tsdb.Request{Query: tsdb.Query{Language: tsdb.LangSQL,
		Text: fmt.Sprintf(`INSERT INTO %s (time, metric, labels_hash) VALUES (now(), 'x', 'x')`, table)}})
	var te *tsdb.Error
	require.True(t, errors.As(err, &te), "query failures are classified: %v", err)
	assert.Equal(t, tsdb.ClassRejected, te.Class, "a write in the read-only transaction is rejected: %v", err)

	_, err = d.Query(ctx, tsdb.Request{Query: tsdb.Query{Language: tsdb.LangSQL, Text: `SELECT nonsense FROM`}})
	require.True(t, errors.As(err, &te))
	assert.Equal(t, tsdb.ClassRejected, te.Class, "a syntax error is rejected")
}

// TestPostgresUnreachable checks that a closed port is a retryable failure,
// so the writer spools instead of dropping history.
func TestPostgresUnreachable(t *testing.T) {
	cfg := postgresConfig("karakuri_history")
	cfg["port"] = 1
	d := postgresDriver(t, cfg)
	err := d.Write(context.Background(), []tsdb.Point{{Time: time.Now().UnixMilli(), Metric: "x", Value: tsdb.Float(1)}})
	var te *tsdb.Error
	require.True(t, errors.As(err, &te), "write failures are classified: %v", err)
	assert.Equal(t, tsdb.ClassUnavailable, te.Class)
	assert.NotContains(t, te.Error(), "karakuri-test", "errors never carry the password")
}
