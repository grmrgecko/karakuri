package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// maxPoints is the most points Prometheus returns per series of a range
// query; it rejects a step that would produce more.
const maxPoints = 11000

// envelope is the wrapper of every Prometheus HTTP API response.
type envelope struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
}

// get calls an API endpoint and returns the data of a successful response.
// Error responses carry status 400 or 422 for bad queries and 503 or 504 for
// overload and timeouts, which tsdb.Do classifies.
func get(ctx context.Context, client *http.Client, cfg Config, path string, params url.Values) (json.RawMessage, error) {
	u := tsdb.JoinURL(cfg.URL, path)
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, tsdb.Rejectedf("build request: %v", err)
	}
	authorize(req, cfg)
	body, err := tsdb.Do(client, req)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, tsdb.Unavailablef("%s did not return Prometheus API JSON", path)
	}
	if env.Status != "success" {
		if env.ErrorType == "timeout" || env.ErrorType == "unavailable" {
			return nil, tsdb.Unavailablef("%s", env.Error)
		}
		return nil, tsdb.Rejectedf("%s: %s", env.ErrorType, env.Error)
	}
	return env.Data, nil
}

// Step returns the range query resolution for an interval: at least one
// second, and coarse enough that the span fits Prometheus's point limit.
func Step(span, interval time.Duration) time.Duration {
	step := interval
	if step < time.Second {
		step = time.Second
	}
	// The minimum is rounded up to whole milliseconds, the precision the
	// step is sent with.
	minStep := time.Duration(math.Ceil(float64(span)/maxPoints/float64(time.Millisecond))) * time.Millisecond
	if step < minStep {
		step = minStep
	}
	return step
}

// unixSeconds formats a time the way the API accepts, in seconds with
// millisecond precision.
func unixSeconds(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', -1, 64)
}

// Query evaluates PromQL over the request range, or once at its end for an
// instant query.
func (d *Driver) Query(ctx context.Context, req tsdb.Request) (*tsdb.Result, error) {
	if req.Language != "" && req.Language != tsdb.LangPromQL {
		return nil, tsdb.Unsupportedf("prometheus connections query promql, not %s", req.Language)
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, tsdb.Rejectedf("the query is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, d.QueryTimeout())
	defer cancel()
	params := url.Values{"query": {req.Text}}
	path := "/api/v1/query"
	if req.Instant {
		params.Set("time", unixSeconds(req.Range.To))
	} else {
		path = "/api/v1/query_range"
		params.Set("start", unixSeconds(req.Range.From))
		params.Set("end", unixSeconds(req.Range.To))
		params.Set("step", strconv.FormatFloat(Step(req.Range.Span(), req.Interval).Seconds(), 'f', -1, 64))
	}
	data, err := get(ctx, d.client, d.cfg, path, params)
	if err != nil {
		return nil, err
	}
	series, err := decodeResult(data, req)
	if err != nil {
		return nil, err
	}
	return &tsdb.Result{Series: series, ExecutedQuery: req.Text}, nil
}

// sample is one [time, "value"] pair. Times are Unix seconds, possibly
// fractional; values are strings so NaN and the infinities survive JSON.
type sample struct {
	T float64
	V string
}

func (s *sample) UnmarshalJSON(b []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if len(pair) != 2 {
		return fmt.Errorf("sample has %d elements, want 2", len(pair))
	}
	if err := json.Unmarshal(pair[0], &s.T); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &s.V)
}

func (s sample) millis() int64 { return int64(math.Round(s.T * 1000)) }

// number turns a sample value into a float64, or nil for NaN and the
// infinities, which charts and JSON cannot carry.
func (s sample) number() any {
	f, err := strconv.ParseFloat(s.V, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

// promSeries is one matrix or vector element. Native histogram samples are
// not read.
type promSeries struct {
	Metric map[string]string `json:"metric"`
	Values []sample          `json:"values"`
	Value  *sample           `json:"value"`
}

// decodeResult turns the data of a query response into series.
func decodeResult(data json.RawMessage, req tsdb.Request) ([]tsdb.Series, error) {
	var d struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, tsdb.Unavailablef("decode query result: %v", err)
	}
	var list []promSeries
	switch d.ResultType {
	case "matrix", "vector":
		if err := json.Unmarshal(d.Result, &list); err != nil {
			return nil, tsdb.Unavailablef("decode %s: %v", d.ResultType, err)
		}
	case "scalar":
		var s sample
		if err := json.Unmarshal(d.Result, &s); err != nil {
			return nil, tsdb.Unavailablef("decode scalar: %v", err)
		}
		list = []promSeries{{Value: &s}}
	case "string":
		var s sample
		if err := json.Unmarshal(d.Result, &s); err != nil {
			return nil, tsdb.Unavailablef("decode string: %v", err)
		}
		name := req.LegendFormat
		if name == "" {
			name = "string"
		}
		return []tsdb.Series{{
			Name:  name,
			RefID: req.RefID,
			Fields: []tsdb.Field{
				{Name: "time", Type: tsdb.FieldTime, Values: []any{s.millis()}},
				{Name: "value", Type: tsdb.FieldString, Values: []any{s.V}},
			},
		}}, nil
	default:
		return nil, tsdb.Unavailablef("unknown result type %q", d.ResultType)
	}
	if req.Format == tsdb.FormatTable {
		return []tsdb.Series{table(list, req.RefID)}, nil
	}
	out := make([]tsdb.Series, 0, len(list))
	for _, ps := range list {
		samples := ps.Values
		if ps.Value != nil {
			samples = []sample{*ps.Value}
		}
		times := make([]int64, len(samples))
		values := make([]any, len(samples))
		for i, s := range samples {
			times[i] = s.millis()
			values[i] = s.number()
		}
		labels := withoutName(ps.Metric)
		s := tsdb.TimeSeries(seriesName(ps.Metric, req.LegendFormat), labels, times, values)
		s.RefID = req.RefID
		out = append(out, s)
	}
	return out, nil
}

// withoutName returns the labels other than __name__, or nil when there are
// none.
func withoutName(metric map[string]string) map[string]string {
	var labels map[string]string
	for k, v := range metric {
		if k == "__name__" {
			continue
		}
		if labels == nil {
			labels = make(map[string]string, len(metric))
		}
		labels[k] = v
	}
	return labels
}

// seriesName names a series by legend format, or in PromQL selector form:
// metric{label="value", ...}, with the metric name outside the braces.
func seriesName(metric map[string]string, legend string) string {
	if legend != "" {
		return tsdb.ApplyLegend(legend, metric)
	}
	name := metric["__name__"]
	keys := make([]string, 0, len(metric))
	for k := range metric {
		if k != "__name__" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		if name == "" {
			return "value"
		}
		return name
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(metric[k])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// table lays every sample out as a row of time, the labels of all series,
// and value.
func table(list []promSeries, refID string) tsdb.Series {
	keySet := map[string]bool{}
	for _, ps := range list {
		for k := range ps.Metric {
			keySet[k] = true
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := make([]tsdb.Field, 0, len(keys)+2)
	fields = append(fields, tsdb.Field{Name: "time", Type: tsdb.FieldTime, Values: []any{}})
	for _, k := range keys {
		fields = append(fields, tsdb.Field{Name: k, Type: tsdb.FieldString, Values: []any{}})
	}
	fields = append(fields, tsdb.Field{Name: "value", Type: tsdb.FieldNumber, Values: []any{}})
	for _, ps := range list {
		samples := ps.Values
		if ps.Value != nil {
			samples = []sample{*ps.Value}
		}
		for _, s := range samples {
			fields[0].Values = append(fields[0].Values, s.millis())
			for i, k := range keys {
				fields[i+1].Values = append(fields[i+1].Values, ps.Metric[k])
			}
			last := len(fields) - 1
			fields[last].Values = append(fields[last].Values, s.number())
		}
	}
	return tsdb.Series{Name: refID, RefID: refID, Fields: fields}
}

// Metadata lists metric names, label names, or one label's values, narrowed
// by a series selector when the request has one.
func (d *Driver) Metadata(ctx context.Context, req tsdb.MetaRequest) ([]string, error) {
	var path string
	switch req.Kind {
	case tsdb.MetaMetrics:
		path = "/api/v1/label/__name__/values"
	case tsdb.MetaLabels:
		path = "/api/v1/labels"
	case tsdb.MetaLabelValues:
		if req.Name == "" {
			return nil, tsdb.Rejectedf("label_values needs a label name")
		}
		path = "/api/v1/label/" + url.PathEscape(req.Name) + "/values"
	default:
		return nil, tsdb.Unsupportedf("prometheus connections have no %s lookup", req.Kind)
	}
	params := url.Values{}
	if req.Match != "" {
		params.Set("match[]", req.Match)
	}
	ctx, cancel := context.WithTimeout(ctx, d.QueryTimeout())
	defer cancel()
	data, err := get(ctx, d.client, d.cfg, path, params)
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, tsdb.Unavailablef("decode %s: %v", req.Kind, err)
	}
	sort.Strings(names)
	if len(names) > tsdb.MaxMetaValues {
		names = names[:tsdb.MaxMetaValues]
	}
	return names, nil
}
