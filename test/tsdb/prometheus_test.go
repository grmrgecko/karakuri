//go:build karakuri_tsdb

package tsdbtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/prometheus"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// prometheusDriver builds a Prometheus-kind driver from settings.
func prometheusDriver(t *testing.T, cfg map[string]any) *prometheus.Driver {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, prometheus.KindDef().Test(context.Background(), uuid.New(), raw),
		"the connection test must pass against a running server, including the empty remote write")
	d, err := prometheus.New(integrations.ConnModel{ID: uuid.New(), Kind: prometheus.Kind, Config: raw},
		integrations.Deps{Logger: zap.NewNop(), PublishStatus: func(integrations.Status) {}})
	require.NoError(t, err)
	return d.(*prometheus.Driver)
}

// prometheusValues returns the distinct numbers of the series a query returns.
func prometheusValues(res *tsdb.Result) map[float64]bool {
	out := map[float64]bool{}
	for _, s := range res.Series {
		for _, v := range s.Fields[1].Values {
			if f, ok := v.(float64); ok {
				out[f] = true
			}
		}
	}
	return out
}

// TestPrometheusRoundTrip writes by remote write and reads back over the
// query API, against Prometheus (remote write receiver on) and
// VictoriaMetrics, which accepts the same remote write at /api/v1/write and
// answers the same API. Prometheus refuses samples older than its head
// block, so the points sit within the last two minutes.
func TestPrometheusRoundTrip(t *testing.T) {
	servers := []struct {
		name string
		url  string
	}{
		{"prometheus", "http://localhost:19090"},
		{"victoriametrics", "http://localhost:19428"},
	}
	for _, srv := range servers {
		t.Run(srv.name, func(t *testing.T) {
			ctx := context.Background()
			d := prometheusDriver(t, map[string]any{"url": srv.url, "write_enabled": true})
			run := fmt.Sprintf("r%08x", rand.Uint32())
			metric := "karakuri_it_" + run
			labels := map[string]string{"room": "kitchen", "run": run}
			now := time.Now().Truncate(time.Second)
			require.NoError(t, d.Write(ctx, []tsdb.Point{
				{Time: now.Add(-90 * time.Second).UnixMilli(), Metric: metric, Labels: labels, Value: tsdb.Float(1)},
				{Time: now.Add(-60 * time.Second).UnixMilli(), Metric: metric, Labels: labels, Value: tsdb.Float(2)},
				{Time: now.Add(-30 * time.Second).UnixMilli(), Metric: metric, Labels: labels, Value: tsdb.Float(3)},
				{Time: now.UnixMilli(), Metric: metric + "_mode", Labels: labels, State: tsdb.Text("away")},
			}))

			// A one-second step evaluates at each written second, so every
			// sample shows up even though the range repeats the latest one.
			req := tsdb.Request{
				Query: tsdb.Query{RefID: "A", Language: tsdb.LangPromQL, Text: metric, LegendFormat: "{{room}}"},
				Range: tsdb.Range{From: now.Add(-2 * time.Minute), To: now.Add(time.Second)}, Interval: time.Second,
			}
			var res *tsdb.Result
			require.Eventually(t, func() bool {
				var err error
				res, err = d.Query(ctx, req)
				return err == nil && len(prometheusValues(res)) == 3
			}, 30*time.Second, 500*time.Millisecond, "written samples must become queryable")
			require.Len(t, res.Series, 1)
			assert.Equal(t, "kitchen", res.Series[0].Name, "the legend format names the series")
			assert.Equal(t, "A", res.Series[0].RefID)
			assert.Equal(t, labels, res.Series[0].Labels, "labels come back without __name__")
			assert.Equal(t, map[float64]bool{1: true, 2: true, 3: true}, prometheusValues(res))

			req.Instant = true
			req.LegendFormat = ""
			res, err := d.Query(ctx, req)
			require.NoError(t, err)
			require.Len(t, res.Series, 1)
			assert.Equal(t, []any{3.0}, res.Series[0].Fields[1].Values, "an instant query reads the latest sample")
			assert.Equal(t, fmt.Sprintf(`%s{room="kitchen", run="%s"}`, metric, run), res.Series[0].Name)

			match := fmt.Sprintf(`{__name__=%q}`, metric)
			values, err := d.Metadata(ctx, tsdb.MetaRequest{Kind: tsdb.MetaLabelValues, Name: "run", Match: match})
			require.NoError(t, err)
			assert.Equal(t, []string{run}, values)
			names, err := d.Metadata(ctx, tsdb.MetaRequest{Kind: tsdb.MetaMetrics, Match: match})
			require.NoError(t, err)
			assert.Equal(t, []string{metric}, names, "the text-only point is not written")
			keys, err := d.Metadata(ctx, tsdb.MetaRequest{Kind: tsdb.MetaLabels, Match: match})
			require.NoError(t, err)
			assert.Equal(t, []string{"__name__", "room", "run"}, keys)

			_, err = d.Query(ctx, tsdb.Request{Query: tsdb.Query{Language: tsdb.LangPromQL, Text: "sum("},
				Range: req.Range, Interval: time.Second})
			var e *tsdb.Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, tsdb.ClassRejected, e.Class, "a PromQL syntax error is the query's fault")
		})
	}
}
