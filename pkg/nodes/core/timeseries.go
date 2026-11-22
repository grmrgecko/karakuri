package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
)

// TimeSeriesQueryNode reads a time-series database or Home Assistant's
// recorder.
type TimeSeriesQueryNode struct{}

var tsTextLangs = []any{tsdb.LangPromQL, tsdb.LangInfluxQL, tsdb.LangFlux, tsdb.LangSQL}
var tsHALangs = []any{tsdb.LangHAHistory, tsdb.LangHAStatistics}

func (TimeSeriesQueryNode) Meta() engine.NodeMeta {
	reductions := []engine.Option{{Value: "", Label: "None"}}
	for _, r := range tsdb.Reductions {
		reductions = append(reductions, engine.Option{Value: r, Label: r})
	}
	return engine.NodeMeta{
		Type: "timeseries-query", Label: "Time series query", Folder: flow.FolderStorage, Icon: "las la-chart-line",
		Description:    "Reads Prometheus, InfluxDB, PostgreSQL, or Home Assistant history over a time range",
		ConnectionKind: tsdb.ReadableKinds,
		Inputs:         []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:        []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}, flow.ErrorPort()},
		Retryable:      true,
		DefaultRetry:   &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 2000, Backoff: 2, MaxDelayMs: 30000},
		ConfigSpec: []engine.FieldSpec{
			{ID: "connection", Label: "Connection", Type: flow.FieldConnection, ConnectionKind: tsdb.ReadableKinds, Required: true},
			{ID: "language", Label: "Query language", Type: flow.FieldSelect, Required: true, Default: tsdb.LangPromQL, Options: []engine.Option{
				{Value: tsdb.LangPromQL, Label: "PromQL"}, {Value: tsdb.LangInfluxQL, Label: "InfluxQL"}, {Value: tsdb.LangFlux, Label: "Flux"},
				{Value: tsdb.LangSQL, Label: "SQL"}, {Value: tsdb.LangHAHistory, Label: "Home Assistant history"}, {Value: tsdb.LangHAStatistics, Label: "Home Assistant statistics"},
			}, Help: "Prometheus takes PromQL; InfluxDB InfluxQL on every version, Flux on 2, and SQL on 3; PostgreSQL SQL."},
			{ID: "query", Label: "Query", Type: flow.FieldText, LanguageField: "language", ShowIf: &flow.ShowIf{Field: "language", Equals: tsTextLangs},
				Placeholder: "avg_over_time(temperature[$__interval])", Help: "Macros such as $__interval, $__timeFilter(time), and $timeFilter are expanded; {{ }} expressions read the message."},
			{ID: "entities", Label: "Entities", Type: flow.FieldJSON, Control: "list", ShowIf: &flow.ShowIf{Field: "language", Equals: tsdb.LangHAHistory}, Help: "1 to 50 entity IDs."},
			{ID: "attribute", Label: "Attribute", Type: flow.FieldString, ShowIf: &flow.ShowIf{Field: "language", Equals: tsdb.LangHAHistory}, Help: "Read this attribute instead of the state."},
			{ID: "statistic_ids", Label: "Statistic IDs", Type: flow.FieldJSON, Control: "list", ShowIf: &flow.ShowIf{Field: "language", Equals: tsdb.LangHAStatistics}, Help: "Usually entity IDs of sensors with a state class."},
			{ID: "period", Label: "Period", Type: flow.FieldSelect, Default: "hour", ShowIf: &flow.ShowIf{Field: "language", Equals: tsdb.LangHAStatistics}, Options: []engine.Option{
				{Value: "5minute", Label: "5 minutes"}, {Value: "hour", Label: "Hour"}, {Value: "day", Label: "Day"}, {Value: "week", Label: "Week"}, {Value: "month", Label: "Month"},
			}},
			{ID: "statistic_types", Label: "Statistic types", Type: flow.FieldJSON, Control: "list", Default: []any{"mean"}, ShowIf: &flow.ShowIf{Field: "language", Equals: tsdb.LangHAStatistics}, Help: "mean, min, max, sum, state, or change."},
			{ID: "from", Label: "From", Type: flow.FieldString, Default: "now-1h", Help: "now-1h, now/d, RFC 3339, or Unix milliseconds."},
			{ID: "to", Label: "To", Type: flow.FieldString, Default: "now"},
			{ID: "max_data_points", Label: "Maximum data points", Type: flow.FieldNumber, Default: 1000, Help: "Sets $__interval: the range divided by this, rounded."},
			{ID: "min_interval", Label: "Minimum interval", Type: flow.FieldString, Placeholder: "15s"},
			{ID: "instant", Label: "Instant query", Type: flow.FieldBoolean, ShowIf: &flow.ShowIf{Field: "language", Equals: tsdb.LangPromQL}, Help: "Evaluate once at the range end."},
			{ID: "reduce", Label: "Reduce to a value", Type: flow.FieldSelect, Options: reductions, Help: "Reduces the first series to one number, output as value."},
		},
	}
}

// Execute runs the query. Unreachable databases fail retryably; refused
// queries fail without retrying.
func (TimeSeriesQueryNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		Connection     string   `json:"connection"`
		Language       string   `json:"language"`
		Query          string   `json:"query"`
		Entities       []string `json:"entities"`
		Attribute      string   `json:"attribute"`
		StatisticIDs   []string `json:"statistic_ids"`
		Period         string   `json:"period"`
		StatisticTypes []string `json:"statistic_types"`
		From           string   `json:"from"`
		To             string   `json:"to"`
		MaxDataPoints  float64  `json:"max_data_points"`
		MinInterval    string   `json:"min_interval"`
		Instant        bool     `json:"instant"`
		Reduce         string   `json:"reduce"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, nonRetryable(err)
	}
	msg := inbound(ec, inputs)
	q := tsdb.Query{
		Language: cfg.Language, MaxDataPoints: int(cfg.MaxDataPoints), MinInterval: cfg.MinInterval, Instant: cfg.Instant,
	}
	var err error
	for _, f := range []struct {
		dst *string
		src string
	}{{&q.Text, cfg.Query}, {&q.From, cfg.From}, {&q.To, cfg.To}} {
		if *f.dst, err = ec.Interpolate(f.src, msg); err != nil {
			return nil, nonRetryable(err)
		}
	}
	if cfg.Language == tsdb.LangHAHistory || cfg.Language == tsdb.LangHAStatistics {
		q.Text = ""
		q.HA = &tsdb.HAQuery{Entities: cfg.Entities, Attribute: cfg.Attribute, StatisticIDs: cfg.StatisticIDs, Period: cfg.Period, Types: cfg.StatisticTypes}
	}
	res, err := tsdbops.Query(ctx, cfg.Connection, q)
	if err != nil {
		return nil, tsNodeError(err)
	}
	payload := map[string]any{
		"series":      res.Series,
		"truncated":   res.Truncated,
		"interval_ms": res.IntervalMs,
	}
	if cfg.Reduce != "" {
		v, err := tsdb.Reduce(res, cfg.Reduce)
		if err != nil {
			return nil, nonRetryable(err)
		}
		if v != nil {
			payload["value"] = *v
		} else {
			payload["value"] = nil
		}
	}
	out := passThrough(msg)
	out.Payload = payload
	return engine.One("out", out), nil
}

// TimeSeriesWriteNode writes points to a time-series database through the
// connection's history writer.
type TimeSeriesWriteNode struct{}

func (TimeSeriesWriteNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "timeseries-write", Label: "Time series write", Folder: flow.FolderStorage, Icon: "las la-chart-area",
		Description:    "Writes points to Prometheus, InfluxDB, or PostgreSQL, buffered while the database is down",
		ConnectionKind: tsdb.WritableKinds,
		Inputs:         []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:        []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()},
		Retryable:      true,
		DefaultRetry:   &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 2000, Backoff: 2, MaxDelayMs: 30000},
		ConfigSpec: []engine.FieldSpec{
			{ID: "connection", Label: "Connection", Type: flow.FieldConnection, ConnectionKind: tsdb.WritableKinds, Required: true},
			{ID: "metric", Label: "Metric", Type: flow.FieldString, Placeholder: "energy_cost", Help: "Metric or measurement name, with {{ }} expressions. Ignored when the payload carries points."},
			{ID: "labels", Label: "Labels", Type: flow.FieldJSON, Control: "map", Help: "Label values accept {{ }} expressions."},
			{ID: "value", Label: "Value", Type: flow.FieldExpression, Default: "payload", Help: "A number, a boolean, or text. Text is stored only by InfluxDB and PostgreSQL."},
			{ID: "timestamp", Label: "Timestamp", Type: flow.FieldExpression, Help: "Optional: Unix milliseconds or an RFC 3339 time. Empty uses now."},
			{ID: "wait", Label: "Wait for delivery", Type: flow.FieldBoolean, Help: "Succeed only once the database accepts the points, and fail instead of buffering."},
		},
	}
}

// Execute queues or delivers the points. A payload with a points array
// writes those; otherwise one point is built from the config.
func (TimeSeriesWriteNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		Connection string            `json:"connection"`
		Metric     string            `json:"metric"`
		Labels     map[string]string `json:"labels"`
		Value      string            `json:"value"`
		Timestamp  string            `json:"timestamp"`
		Wait       bool              `json:"wait"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, nonRetryable(err)
	}
	msg := inbound(ec, inputs)
	var points []tsdb.Point
	if obj, ok := msg.Payload.(map[string]any); ok {
		if list, ok := obj["points"].([]any); ok {
			// An empty array is still the payload's own batch. Left nil it
			// would fall through to the single point built from the config.
			points = make([]tsdb.Point, 0, len(list))
			for i, raw := range list {
				p, err := pointFrom(raw)
				if err != nil {
					return nil, nonRetryable(fmt.Errorf("points[%d]: %w", i, err))
				}
				points = append(points, p)
			}
		}
	}
	if points == nil {
		metric, err := ec.Interpolate(cfg.Metric, msg)
		if err != nil {
			return nil, nonRetryable(err)
		}
		if strings.TrimSpace(metric) == "" {
			return nil, nonRetryable(errors.New("metric is required"))
		}
		labels := map[string]string{}
		for k, v := range cfg.Labels {
			if labels[k], err = ec.Interpolate(v, msg); err != nil {
				return nil, nonRetryable(err)
			}
		}
		expr := cfg.Value
		if strings.TrimSpace(expr) == "" {
			expr = "payload"
		}
		value, err := ec.Eval(expr, msg)
		if err != nil {
			return nil, nonRetryable(err)
		}
		var at any
		if strings.TrimSpace(cfg.Timestamp) != "" {
			if at, err = ec.Eval(cfg.Timestamp, msg); err != nil {
				return nil, nonRetryable(err)
			}
		}
		p, err := pointFrom(map[string]any{"metric": metric, "labels": toAnyMap(labels), "value": value, "time": at})
		if err != nil {
			return nil, nonRetryable(err)
		}
		points = []tsdb.Point{p}
	}
	if len(points) == 0 {
		return nil, nonRetryable(errors.New("no points to write"))
	}
	w, _, err := tsdbops.Writer(cfg.Connection)
	if err != nil {
		return nil, tsNodeError(err)
	}
	if cfg.Wait {
		if err := w.Deliver(ctx, points); err != nil {
			return nil, tsNodeError(err)
		}
	} else {
		w.Enqueue(points)
	}
	out := passThrough(msg)
	if out.Meta == nil {
		out.Meta = map[string]any{}
	}
	out.Meta["written"] = len(points)
	return engine.One("out", out), nil
}

// pointFrom reads one point object: metric, labels, value (number, boolean,
// or text) or state, and time (Unix milliseconds or RFC 3339).
func pointFrom(raw any) (tsdb.Point, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return tsdb.Point{}, errors.New("a point must be an object")
	}
	metric, _ := obj["metric"].(string)
	if strings.TrimSpace(metric) == "" {
		return tsdb.Point{}, errors.New("a point needs a metric")
	}
	p := tsdb.Point{Metric: metric, Time: time.Now().UnixMilli()}
	if labels, ok := obj["labels"].(map[string]any); ok && len(labels) > 0 {
		p.Labels = make(map[string]string, len(labels))
		for k, v := range labels {
			p.Labels[k] = fmt.Sprint(v)
		}
	}
	value := obj["value"]
	if value == nil {
		value = obj["state"]
	}
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return tsdb.Point{}, errors.New("value must be a finite number")
		}
		p.Value = tsdb.Float(v)
	case int:
		p.Value = tsdb.Float(float64(v))
	case int64:
		p.Value = tsdb.Float(float64(v))
	case bool:
		if v {
			p.Value = tsdb.Float(1)
		} else {
			p.Value = tsdb.Float(0)
		}
	case string:
		p.State = tsdb.Text(v)
	case nil:
		return tsdb.Point{}, errors.New("a point needs a value")
	default:
		return tsdb.Point{}, fmt.Errorf("value must be a number, boolean, or text, not %T", value)
	}
	switch t := obj["time"].(type) {
	case nil:
	case float64:
		p.Time = int64(t)
	case int64:
		p.Time = t
	case int:
		p.Time = int64(t)
	case string:
		if ms, err := strconv.ParseInt(t, 10, 64); err == nil {
			p.Time = ms
		} else if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			p.Time = parsed.UnixMilli()
		} else {
			return tsdb.Point{}, fmt.Errorf("time %q is not Unix milliseconds or RFC 3339", t)
		}
	default:
		return tsdb.Point{}, fmt.Errorf("time must be Unix milliseconds or RFC 3339, not %T", t)
	}
	return p, nil
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func nonRetryable(err error) error {
	return &nodeapi.ExecutionError{Message: err.Error(), Cause: err, Retryable: false}
}

// tsNodeError maps a time-series failure to a node failure: an unavailable
// source or a connection that is not running may clear, anything else will
// not.
func tsNodeError(err error) error {
	var te *tsdb.Error
	if errors.As(err, &te) {
		return &nodeapi.ExecutionError{Message: te.Error(), Cause: err, Retryable: te.Retryable()}
	}
	var ae *apperrors.AppError
	if errors.As(err, &ae) {
		retry := ae.StatusCode == 502 || ae.StatusCode == 503
		return &nodeapi.ExecutionError{Message: ae.Error(), Cause: err, Retryable: retry}
	}
	return &nodeapi.ExecutionError{Message: err.Error(), Cause: err, Retryable: true}
}
