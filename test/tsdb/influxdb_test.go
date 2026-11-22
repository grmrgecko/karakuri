//go:build karakuri_tsdb

package tsdbtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/influxdb"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// influxdbConfigs are the three InfluxDB containers from compose.yaml.
var influxdbConfigs = map[string]map[string]any{
	"v1": {
		"version": "1", "url": "http://localhost:19086", "database": "karakuri",
		"username": "admin", "password": "karakuri-test", "write_enabled": true,
	},
	"v2": {
		"version": "2", "url": "http://localhost:19087", "org": "karakuri", "bucket": "karakuri",
		"token": "karakuri-test-token", "write_enabled": true,
	},
	"v3": {
		"version": "3", "url": "http://localhost:19181", "database": "karakuri", "write_enabled": true,
	},
}

func influxdbDriver(t *testing.T, cfg map[string]any) *influxdb.Driver {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, influxdb.KindDef().Test(context.Background(), uuid.New(), raw), "the connection test passes against the container")
	d, err := influxdb.New(integrations.ConnModel{ID: uuid.New(), Kind: influxdb.Kind, Config: raw},
		integrations.Deps{Logger: zap.NewNop(), PublishStatus: func(integrations.Status) {}})
	require.NoError(t, err)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	return d
}

// influxdbQuery runs a query over the last hour, waiting for InfluxDB 3,
// which makes writes readable once its write-ahead log flushes.
func influxdbQuery(t *testing.T, d *influxdb.Driver, lang, text string, want int) []tsdb.Series {
	t.Helper()
	now := time.Now()
	req := tsdb.Request{
		Query: tsdb.Query{RefID: "A", Language: lang, Text: text},
		Range: tsdb.Range{From: now.Add(-time.Hour), To: now},
	}
	var series []tsdb.Series
	require.Eventually(t, func() bool {
		res, err := d.Query(context.Background(), req)
		if err != nil {
			t.Logf("%s query: %v", lang, err)
			return false
		}
		series = res.Series
		return len(series) == want
	}, 15*time.Second, 250*time.Millisecond, "%s query %q returns %d series", lang, text, want)
	return series
}

// TestInfluxDB writes numbers and a text state to InfluxDB 1.8, 2, and 3
// Core as line protocol, reads them back with InfluxQL on every version,
// Flux on 2, and SQL on 3, and runs the metadata lookups editors use. The
// tag value "a,b c" and the tag key "friendly name" exercise line protocol
// escaping against the real parsers.
func TestInfluxDB(t *testing.T) {
	for _, version := range []string{"v1", "v2", "v3"} {
		t.Run(version, func(t *testing.T) {
			d := influxdbDriver(t, influxdbConfigs[version])
			metric := fmt.Sprintf("karakuri_it_%d", rand.Uint64())
			base := time.Now().Add(-time.Minute).Truncate(time.Second).UnixMilli()
			labels := map[string]string{"room": "den", "friendly name": "a,b c"}
			require.NoError(t, d.Write(context.Background(), []tsdb.Point{
				{Time: base, Metric: metric, Labels: labels, Value: tsdb.Float(20.5)},
				{Time: base + 1000, Metric: metric, Labels: labels, Value: tsdb.Float(21)},
				{Time: base + 2000, Metric: metric, Labels: labels, State: tsdb.Text(`heat "on"`)},
			}))

			series := influxdbQuery(t, d, tsdb.LangInfluxQL,
				fmt.Sprintf(`SELECT "value" FROM %q WHERE time > now() - 1h GROUP BY "room", "friendly name"`, metric), 1)
			s := series[0]
			assert.Equal(t, "A", s.RefID)
			assert.Equal(t, map[string]string{"room": "den", "friendly name": "a,b c"}, s.Labels, "escaped tags read back unescaped")
			assert.Equal(t, []any{base, base + 1000}, s.Fields[0].Values, "millisecond timestamps survive")
			assert.Equal(t, []any{20.5, 21.0}, s.Fields[1].Values)

			series = influxdbQuery(t, d, tsdb.LangInfluxQL, fmt.Sprintf(`SELECT "state" FROM %q WHERE "state" != ''`, metric), 1)
			assert.Equal(t, []any{`heat "on"`}, series[0].Fields[1].Values, "a text state is a quoted string field")

			switch version {
			case "v2":
				series = influxdbQuery(t, d, tsdb.LangFlux, fmt.Sprintf(
					`from(bucket: "karakuri") |> range(start: -1h) |> filter(fn: (r) => r._measurement == %q)`, metric), 2)
				byField := map[string]tsdb.Series{}
				for _, s := range series {
					byField[s.Labels["_field"]] = s
				}
				assert.Equal(t, []any{20.5, 21.0}, byField["value"].Fields[1].Values)
				assert.Equal(t, "den", byField["value"].Labels["room"])
				assert.Equal(t, []any{`heat "on"`}, byField["state"].Fields[1].Values)
			case "v3":
				series = influxdbQuery(t, d, tsdb.LangSQL, fmt.Sprintf(
					`SELECT time, room, value FROM %q WHERE value IS NOT NULL ORDER BY time`, metric), 1)
				assert.Equal(t, map[string]string{"room": "den"}, series[0].Labels)
				assert.Equal(t, []any{base, base + 1000}, series[0].Fields[0].Values)
				assert.Equal(t, []any{20.5, 21.0}, series[0].Fields[1].Values)
			}

			other := tsdb.LangSQL
			if version == "v3" {
				other = tsdb.LangFlux
			}
			_, err := d.Query(context.Background(), tsdb.Request{Query: tsdb.Query{Language: other, Text: "x"}})
			var e *tsdb.Error
			require.True(t, errors.As(err, &e))
			assert.Equal(t, tsdb.ClassUnsupported, e.Class, "%s is refused on %s", other, version)

			meta := func(req tsdb.MetaRequest) []string {
				vals, err := d.Metadata(context.Background(), req)
				require.NoError(t, err, "%s lookup", req.Kind)
				return vals
			}
			assert.Contains(t, meta(tsdb.MetaRequest{Kind: tsdb.MetaDatabases}), "karakuri")
			assert.Contains(t, meta(tsdb.MetaRequest{Kind: tsdb.MetaMeasurements}), metric)
			assert.Equal(t, []string{"friendly name", "room"}, meta(tsdb.MetaRequest{Kind: tsdb.MetaTagKeys, Measurement: metric}))
			assert.Equal(t, []string{"den"}, meta(tsdb.MetaRequest{Kind: tsdb.MetaTagValues, Measurement: metric, Name: "room"}))
			assert.Equal(t, []string{"state", "value"}, meta(tsdb.MetaRequest{Kind: tsdb.MetaFieldKeys, Measurement: metric}))
		})
	}
}
