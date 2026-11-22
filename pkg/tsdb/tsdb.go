// Package tsdb is the shared vocabulary of Karakuri's time-series
// connections: the query a flow, MCP caller, or chart sends, the series
// result every source returns, time ranges and their macros, and the sink a
// writable connection exposes to the history writer. Drivers for each
// database implement Querier, Sink, or both; nothing here speaks a wire
// protocol.
package tsdb

import (
	"context"
	"time"
)

// Query languages. The Home Assistant pair is not a text language; the
// selection lives in HA instead of Text.
const (
	LangPromQL       = "promql"
	LangInfluxQL     = "influxql"
	LangFlux         = "flux"
	LangSQL          = "sql"
	LangHAHistory    = "ha_history"
	LangHAStatistics = "ha_statistics"
	// LangEnergy reads karakuri's energy rollups on connection EnergyConnection.
	LangEnergy = "energy"
)

// EnergyConnection is the built-in connection ID energy queries name.
const EnergyConnection = "energy"

// Result formats.
const (
	FormatTimeSeries = "time_series"
	FormatTable      = "table"
)

// Query is one request against a time-series source, as nodes, MCP, and
// charts store it. Text holds macros and is expanded before a driver sees it.
type Query struct {
	RefID         string       `json:"ref_id,omitempty" description:"Reference ID distinguishing queries of one chart, such as A."`
	Language      string       `json:"language" description:"promql, influxql, flux, sql, ha_history, ha_statistics, or energy."`
	Text          string       `json:"text,omitempty" description:"Query text in the language; macros such as $__interval are expanded."`
	HA            *HAQuery     `json:"ha,omitempty" description:"Home Assistant selection for ha_history and ha_statistics."`
	Energy        *EnergyQuery `json:"energy,omitempty" description:"Energy selection for the energy language."`
	From          string       `json:"from,omitempty" description:"Range start: now-6h, now/d, RFC 3339, or Unix milliseconds. Default now-6h."`
	To            string       `json:"to,omitempty" description:"Range end, in the same forms. Default now."`
	MaxDataPoints int          `json:"max_data_points,omitempty" description:"Most points per series; sets $__interval. Default 1000."`
	MinInterval   string       `json:"min_interval,omitempty" description:"Lower bound for $__interval, such as 15s."`
	Format        string       `json:"format,omitempty" description:"time_series (default) or table."`
	LegendFormat  string       `json:"legend_format,omitempty" description:"Series name template, such as {{instance}}."`
	Instant       bool         `json:"instant,omitempty" description:"PromQL only: evaluate once at the range end."`
}

// HAQuery selects Home Assistant recorder data.
type HAQuery struct {
	Entities     []string `json:"entities,omitempty" description:"Entity IDs for history, 1 to 50."`
	Attribute    string   `json:"attribute,omitempty" description:"Attribute to read instead of the state, for history."`
	StatisticIDs []string `json:"statistic_ids,omitempty" description:"Statistic IDs for statistics."`
	Period       string   `json:"period,omitempty" description:"5minute, hour (default), day, week, or month."`
	Types        []string `json:"types,omitempty" description:"Statistic types: mean, min, max, sum, state, change. Default mean."`
}

// EnergyQuery selects energy rollups.
type EnergyQuery struct {
	Targets []string `json:"targets,omitempty" description:"house, untracked, solar, group:<id>, or source:<id>; default house."`
	Measure string   `json:"measure,omitempty" description:"usage (default), cost, export_credit, daily_charge, total_cost, import, export, production, self_consumption, or solar_share."`
	Period  string   `json:"period,omitempty" description:"hour, day, week, month, or auto from the interval (default)."`
}

// Request is a query ready for a driver: macros expanded, range resolved,
// and interval chosen.
type Request struct {
	Query
	Range    Range
	Interval time.Duration
}

// Field types.
const (
	FieldTime    = "time"
	FieldNumber  = "number"
	FieldString  = "string"
	FieldBoolean = "boolean"
)

// Field is one column of a series. Time values are Unix milliseconds; number
// values are float64 or nil for a gap.
type Field struct {
	Name   string            `json:"name" description:"Field name."`
	Type   string            `json:"type" description:"time, number, string, or boolean."`
	Labels map[string]string `json:"labels,omitempty" description:"Labels of this field when they differ from the series."`
	Unit   string            `json:"unit,omitempty" description:"Unit the source reports."`
	Values []any             `json:"values" description:"Column values; times are Unix milliseconds."`
}

// Series is one frame of a result: a time series when its first field is
// time, otherwise a table.
type Series struct {
	Name   string            `json:"name" description:"Series name."`
	RefID  string            `json:"ref_id,omitempty" description:"Query reference ID that produced it."`
	Labels map[string]string `json:"labels,omitempty" description:"Labels identifying the series."`
	Unit   string            `json:"unit,omitempty" description:"Unit the source reports."`
	Fields []Field           `json:"fields" description:"Columns."`
}

// Len returns the number of rows.
func (s Series) Len() int {
	if len(s.Fields) == 0 {
		return 0
	}
	return len(s.Fields[0].Values)
}

// Result is what every query returns.
type Result struct {
	Series        []Series `json:"series" description:"Series or tables read."`
	Truncated     bool     `json:"truncated,omitempty" description:"Whether the value limit cut the result short."`
	ExecutedQuery string   `json:"executed_query,omitempty" description:"Query text after macro expansion."`
	IntervalMs    int64    `json:"interval_ms,omitempty" description:"Interval used for $__interval, in milliseconds."`
}

// TimeSeries builds a two-field series of times and numbers.
func TimeSeries(name string, labels map[string]string, times []int64, values []any) Series {
	t := make([]any, len(times))
	for i, v := range times {
		t[i] = v
	}
	return Series{
		Name:   name,
		Labels: labels,
		Fields: []Field{
			{Name: "time", Type: FieldTime, Values: t},
			{Name: "value", Type: FieldNumber, Labels: labels, Values: values},
		},
	}
}

// Metadata lookup kinds.
const (
	MetaMetrics      = "metrics"
	MetaLabels       = "labels"
	MetaLabelValues  = "label_values"
	MetaDatabases    = "databases"
	MetaMeasurements = "measurements"
	MetaTagKeys      = "tag_keys"
	MetaTagValues    = "tag_values"
	MetaFieldKeys    = "field_keys"
	MetaSchemas      = "schemas"
	MetaTables       = "tables"
	MetaColumns      = "columns"
	MetaEntities     = "entities"
	MetaStatisticIDs = "statistic_ids"
	MetaTargets      = "targets"
)

// MetaRequest asks a source for names an editor can offer.
type MetaRequest struct {
	Kind        string `json:"kind" description:"metrics, labels, label_values, databases, measurements, tag_keys, tag_values, field_keys, schemas, tables, columns, entities, or statistic_ids."`
	Name        string `json:"name,omitempty" description:"Label or tag key for label_values and tag_values."`
	Match       string `json:"match,omitempty" description:"Series selector narrowing Prometheus lookups."`
	Measurement string `json:"measurement,omitempty" description:"Measurement for InfluxDB tag and field lookups."`
	Schema      string `json:"schema,omitempty" description:"Schema for PostgreSQL table lookups."`
	Table       string `json:"table,omitempty" description:"Table for column lookups."`
}

// MaxMetaValues bounds a metadata answer.
const MaxMetaValues = 1000

// Querier is a source that answers queries. Errors are *Error.
type Querier interface {
	Query(ctx context.Context, req Request) (*Result, error)
	Metadata(ctx context.Context, req MetaRequest) ([]string, error)
}

// Point is one sample on its way to a sink. Exactly one of Value and State is
// set: Value for numbers, State for text a text-capable sink stores.
type Point struct {
	Time   int64             `json:"t"`
	Metric string            `json:"m"`
	Labels map[string]string `json:"l,omitempty"`
	Value  *float64          `json:"v,omitempty"`
	State  *string           `json:"s,omitempty"`
}

// SinkInfo describes how a writable connection wants points.
type SinkInfo struct {
	// Writable is false when writing is turned off in the connection.
	Writable bool
	// NumericOnly sinks store numbers only; text states are dropped and
	// selects become state-sets.
	NumericOnly bool
	// Heartbeat re-sends each series' last value at this interval; zero is
	// off.
	Heartbeat time.Duration
	// BufferBytes caps the on-disk spool.
	BufferBytes int64
}

// Sink is a connection that accepts points. Write returns *Error so the
// writer can tell a retryable failure from a rejected batch.
type Sink interface {
	Write(ctx context.Context, points []Point) error
	SinkInfo() SinkInfo
}

// TimeoutSource reports a connection's query timeout.
type TimeoutSource interface {
	QueryTimeout() time.Duration
}

// ScrapeSource reports the scrape interval $__rate_interval is built on.
type ScrapeSource interface {
	ScrapeInterval() time.Duration
}

// Languages lists the query languages a source accepts.
type Languages interface {
	Languages() []string
}

// Float returns a pointer to v, for building points.
func Float(v float64) *float64 { return &v }

// Text returns a pointer to s, for building points.
func Text(s string) *string { return &s }
