package prometheus

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/klauspost/compress/snappy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protowire"
)

// newTestDriver builds a driver against a local server.
func newTestDriver(t *testing.T, cfg map[string]any) *Driver {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	d, err := New(integrations.ConnModel{ID: uuid.New(), Kind: Kind, Config: raw}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	return d.(*Driver)
}

// wireField is one decoded protobuf field.
type wireField struct {
	num protowire.Number
	typ protowire.Type
	raw []byte
	u64 uint64
}

// fields decodes one protobuf message into its fields in wire order.
func fields(t *testing.T, b []byte) []wireField {
	t.Helper()
	var out []wireField
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		require.GreaterOrEqual(t, n, 0, "the message must hold valid tags")
		b = b[n:]
		f := wireField{num: num, typ: typ}
		switch typ {
		case protowire.BytesType:
			f.raw, n = protowire.ConsumeBytes(b)
		case protowire.Fixed64Type:
			f.u64, n = protowire.ConsumeFixed64(b)
		case protowire.VarintType:
			f.u64, n = protowire.ConsumeVarint(b)
		default:
			t.Fatalf("unexpected wire type %d", typ)
		}
		require.GreaterOrEqual(t, n, 0, "field %d must decode", num)
		b = b[n:]
		out = append(out, f)
	}
	return out
}

// TestRemoteWriteRequest locks the remote write 1.0 wire contract Prometheus
// and VictoriaMetrics decode: a snappy block (not framed) body with the
// version 0.1.0 headers, WriteRequest.timeseries = 1, TimeSeries.labels = 1
// and samples = 2, Label name = 1 and value = 2, Sample value = 1 (double)
// and timestamp = 2 (int64 ms). Prometheus rejects a batch whose labels are
// unsorted, whose samples go back in time, or that repeats a timestamp, so
// those are normalized here; text-only points are dropped.
func TestRemoteWriteRequest(t *testing.T) {
	var header http.Header
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	d := newTestDriver(t, map[string]any{
		"url": srv.URL, "write_enabled": true,
		"headers": map[string]string{"X-Scope-OrgID": "tenant"},
	})
	labels := map[string]string{"room": "kitchen", "entity.id": "sensor.t", "empty": ""}
	err := d.Write(context.Background(), []tsdb.Point{
		{Time: 2000, Metric: "home.temp", Labels: labels, Value: tsdb.Float(21)},
		{Time: 1000, Metric: "home.temp", Labels: labels, Value: tsdb.Float(20)},
		{Time: 2000, Metric: "home.temp", Labels: labels, Value: tsdb.Float(21.5)},
		{Time: 1000, Metric: "home.mode", State: tsdb.Text("away")},
	})
	require.NoError(t, err)

	assert.Equal(t, "snappy", header.Get("Content-Encoding"))
	assert.Equal(t, "application/x-protobuf", header.Get("Content-Type"))
	assert.Equal(t, "0.1.0", header.Get("X-Prometheus-Remote-Write-Version"))
	assert.Equal(t, "tenant", header.Get("X-Scope-OrgID"), "configured headers ride every request")

	raw, err := snappy.Decode(nil, body)
	require.NoError(t, err, "the body must be a snappy block")
	top := fields(t, raw)
	require.Len(t, top, 1, "one label set makes one TimeSeries; the text point is dropped")
	assert.Equal(t, protowire.Number(1), top[0].num)

	var gotLabels [][2]string
	var gotSamples [][2]float64
	for _, f := range fields(t, top[0].raw) {
		require.Equal(t, protowire.BytesType, f.typ)
		inner := fields(t, f.raw)
		require.Len(t, inner, 2)
		switch f.num {
		case 1:
			assert.Equal(t, protowire.Number(1), inner[0].num, "Label.name is field 1")
			assert.Equal(t, protowire.Number(2), inner[1].num, "Label.value is field 2")
			gotLabels = append(gotLabels, [2]string{string(inner[0].raw), string(inner[1].raw)})
		case 2:
			assert.Equal(t, protowire.Fixed64Type, inner[0].typ, "Sample.value is a double")
			assert.Equal(t, protowire.VarintType, inner[1].typ, "Sample.timestamp is an int64")
			gotSamples = append(gotSamples, [2]float64{math.Float64frombits(inner[0].u64), float64(inner[1].u64)})
		default:
			t.Fatalf("unexpected TimeSeries field %d", f.num)
		}
	}
	assert.Equal(t, [][2]string{
		{"__name__", "home_temp"}, {"entity_id", "sensor.t"}, {"room", "kitchen"},
	}, gotLabels, "labels are sanitized, sorted, and empty values omitted")
	assert.Equal(t, [][2]float64{{20, 1000}, {21.5, 2000}}, gotSamples,
		"samples are time-ordered and a repeated timestamp keeps the last value")
}

// TestRemoteWriteStatusClass pins how a remote write failure reaches the
// history writer, which discards a rejected batch and keeps an unavailable
// one buffered. A revoked token (401, 403) or a receiver that is not enabled
// (Prometheus answers 404 without --web.enable-remote-write-receiver) is the
// destination's state, so the batch must stay; a 400 is the batch itself.
func TestRemoteWriteStatusClass(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{status: http.StatusBadRequest, want: tsdb.ClassRejected},
		{status: http.StatusUnauthorized, want: tsdb.ClassUnavailable},
		{status: http.StatusForbidden, want: tsdb.ClassUnavailable},
		{status: http.StatusNotFound, want: tsdb.ClassUnavailable},
		{status: http.StatusServiceUnavailable, want: tsdb.ClassUnavailable},
	}
	for _, tc := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		d := newTestDriver(t, map[string]any{"url": srv.URL, "write_enabled": true})
		err := d.Write(context.Background(), []tsdb.Point{{Time: 1000, Metric: "m", Value: tsdb.Float(1)}})
		srv.Close()
		require.Error(t, err, "HTTP %d must fail the write", tc.status)
		assert.Equal(t, tc.want, tsdb.Classify(err).Class, "class for HTTP %d", tc.status)
	}
}

// TestQueryDecode reads responses shaped like Prometheus 2.x/3.x
// /api/v1/query_range and /api/v1/query output: times are fractional Unix
// seconds, values strings where "NaN" and "+Inf" stand for non-finite
// samples, and failures carry status "error" with errorType bad_data (400)
// or unavailable (503).
func TestQueryDecode(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		instant bool
		legend  string
		want    []tsdb.Series
		class   string
	}{
		{
			name:   "matrix",
			status: 200,
			body: `{"status":"success","data":{"resultType":"matrix","result":[
				{"metric":{"__name__":"up","job":"prometheus","instance":"localhost:9090"},
				 "values":[[1730000000,"1"],[1730000015.5,"NaN"],[1730000030,"+Inf"]]}]}}`,
			want: []tsdb.Series{tsdb.TimeSeries(`up{instance="localhost:9090", job="prometheus"}`,
				map[string]string{"job": "prometheus", "instance": "localhost:9090"},
				[]int64{1730000000000, 1730000015500, 1730000030000}, []any{1.0, nil, nil})},
		},
		{
			name:    "vector with legend",
			status:  200,
			instant: true,
			legend:  "{{job}} on {{instance}}",
			body: `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"job":"node","instance":"a"},"value":[1730000000.123,"42.5"]}]}}`,
			want: []tsdb.Series{tsdb.TimeSeries("node on a",
				map[string]string{"job": "node", "instance": "a"},
				[]int64{1730000000123}, []any{42.5})},
		},
		{
			name:   "bad query",
			status: 400,
			body:   `{"status":"error","errorType":"bad_data","error":"1:4: parse error: unexpected end of input"}`,
			class:  tsdb.ClassRejected,
		},
		{
			name:   "overloaded",
			status: 503,
			body:   `{"status":"error","errorType":"unavailable","error":"too many concurrent queries"}`,
			class:  tsdb.ClassUnavailable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			d := newTestDriver(t, map[string]any{"url": srv.URL})
			end := time.Unix(1730000030, 0)
			res, err := d.Query(context.Background(), tsdb.Request{
				Query: tsdb.Query{RefID: "A", Language: tsdb.LangPromQL, Text: "up", Instant: tc.instant, LegendFormat: tc.legend},
				Range: tsdb.Range{From: end.Add(-time.Hour), To: end}, Interval: 15 * time.Second,
			})
			if tc.class != "" {
				var e *tsdb.Error
				require.ErrorAs(t, err, &e)
				assert.Equal(t, tc.class, e.Class, "%s must classify as %s", tc.name, tc.class)
				return
			}
			require.NoError(t, err)
			for i := range tc.want {
				tc.want[i].RefID = "A"
			}
			assert.Equal(t, tc.want, res.Series)
		})
	}
}
