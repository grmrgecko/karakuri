// Package dashchart runs the queries of a dashboard's chart placements. A
// viewer names a placement, never a query: the queries come from the layout
// the viewer is served, variable values are substituted only when they are
// options, and results are shared between viewers through a short cache so
// a wall of screens costs the database one query per refresh.
package dashchart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	"github.com/grmrgecko/karakuri/pkg/energy"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
)

// Limits from spec 028.
const (
	MaxQueries     = 10
	minWidth       = 50
	maxWidth       = 4000
	minCacheTTL    = 5 * time.Second
	maxCacheTTL    = 5 * time.Minute
	errorCacheTTL  = 5 * time.Second
	maxCacheItems  = 512
	defaultRefresh = 30 * time.Second
)

// PackageName is the chart widget's package.
const PackageName = "chart"

// Query is one query of a chart placement, as its config stores it.
type Query struct {
	RefID         string            `json:"refId"`
	ConnectionID  string            `json:"connectionId"`
	Language      string            `json:"language"`
	Text          string            `json:"text"`
	HA            *tsdb.HAQuery     `json:"ha,omitempty"`
	Energy        *tsdb.EnergyQuery `json:"energy,omitempty"`
	LegendFormat  string            `json:"legendFormat"`
	MaxDataPoints int               `json:"maxDataPoints"`
	MinInterval   string            `json:"minInterval"`
	Instant       bool              `json:"instant"`
	Format        string            `json:"format"`
	Hide          bool              `json:"hide"`
}

// Config is the part of a chart placement's config the server reads.
type Config struct {
	Queries       []Query `json:"queries"`
	TimeRange     string  `json:"timeRange"`
	RelativeTime  string  `json:"relativeTime"`
	TimeShift     string  `json:"timeShift"`
	Refresh       string  `json:"refresh"`
	MaxDataPoints int     `json:"maxDataPoints"`
}

// Request is what a viewer asks for.
type Request struct {
	// Values are the viewer's variable values by name.
	Values map[string]string
	// From and To override the chart's range, for a zoom on a chart whose
	// range is fixed.
	From, To string
	// Width is the drawing width in pixels; it sets the default maximum
	// data points.
	Width int
	// Refs limits the run to these reference IDs; empty runs every query.
	Refs []string
}

// ErrorBody is a failed query's classified error.
type ErrorBody struct {
	Class   string `json:"class" description:"unavailable, rejected, or unsupported."`
	Message string `json:"message" description:"What the source said."`
}

// QueryResult is one query's answer.
type QueryResult struct {
	RefID  string       `json:"ref_id" description:"Query reference ID."`
	Result *tsdb.Result `json:"result,omitempty" description:"Series read."`
	Error  *ErrorBody   `json:"error,omitempty" description:"Why the query failed."`
	Cached bool         `json:"cached,omitempty" description:"Whether the result came from the shared cache."`
}

// RangeBody is the resolved range in Unix milliseconds.
type RangeBody struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Response is a chart's data.
type Response struct {
	Results    []QueryResult `json:"results"`
	Range      RangeBody     `json:"range"`
	IntervalMs int64         `json:"interval_ms"`
}

// ValueError is a viewer value that is not an option of its variable.
type ValueError struct{ Message string }

func (e *ValueError) Error() string { return e.Message }

// ParseConfig reads a placement's chart config.
func ParseConfig(raw string) (*Config, error) {
	var c Config
	if strings.TrimSpace(raw) == "" {
		return &c, nil
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("chart configuration is not valid JSON: %w", err)
	}
	if len(c.Queries) > MaxQueries {
		c.Queries = c.Queries[:MaxQueries]
	}
	return &c, nil
}

// Values resolves a viewer's variable values against the served variables.
// Missing variables take their defaults; a value that is not an option, or
// for a time range does not parse, is refused. Interval auto values become
// durations.
func Values(served []models.DashboardVariable, requested map[string]string) (dashscope.VariableValues, error) {
	resolved, followed := dashvars.ResolveFor(served, requested)
	values := dashscope.DefaultValues(resolved)
	display := &models.DashboardDisplay{Variables: resolved}
	for name, f := range followed {
		if v, ok := display.FindVariable(name); ok {
			if o, ok := v.Resolve(f); ok {
				values[name] = o
			}
		}
	}
	for name, ref := range requested {
		v, ok := display.FindVariable(name)
		if !ok {
			continue
		}
		o, ok := v.Resolve(ref)
		if !ok {
			return nil, &ValueError{Message: fmt.Sprintf("%q is not an option of variable %q", ref, name)}
		}
		values[name] = o
	}
	for _, v := range resolved {
		if o, ok := values[v.Name]; ok {
			values[v.Name] = dashvars.EffectiveValue(v, o, values)
		}
	}
	return values, nil
}

// Range resolves the chart's range ends as query strings. Precedence: the
// request's override, the configured range (often a ${name} reference), then
// the last 6 hours. A relative time replaces the configured range with the
// last span ending now, and yields to an override, which is the span a
// viewer zoomed to. A time shift moves both ends back.
func Range(c *Config, values dashscope.VariableValues, overrideFrom, overrideTo string) (string, string, error) {
	from, to := tsdb.DefaultFrom, tsdb.DefaultTo
	if tr := strings.TrimSpace(dashscope.Substitute(c.TimeRange, values)); tr != "" {
		if f, t, ok := tsdb.SplitRangeValue(tr); ok {
			from, to = f, t
		}
	}
	rel := strings.TrimSpace(dashscope.Substitute(c.RelativeTime, values))
	var relative time.Duration
	if rel != "" {
		d, err := tsdb.ParseDuration(rel)
		if err != nil || d <= 0 {
			return "", "", fmt.Errorf("relativeTime %q is not a duration", rel)
		}
		relative = d
	}
	switch {
	case overrideFrom != "" || overrideTo != "":
		if _, err := tsdb.ParseRange(overrideFrom, overrideTo, time.Now()); err != nil {
			return "", "", err
		}
		// An override may name one end. The other takes its default here, so
		// a time shift below moves it too.
		from, to = strings.TrimSpace(overrideFrom), strings.TrimSpace(overrideTo)
		if from == "" {
			from = tsdb.DefaultFrom
		}
		if to == "" {
			to = tsdb.DefaultTo
		}
	case relative > 0:
		from, to = "now-"+tsdb.FormatDuration(relative), "now"
	}
	if shift := strings.TrimSpace(dashscope.Substitute(c.TimeShift, values)); shift != "" {
		d, err := tsdb.ParseDuration(shift)
		if err != nil || d <= 0 {
			return "", "", fmt.Errorf("timeShift %q is not a duration", shift)
		}
		from, to = shiftEnd(from, d), shiftEnd(to, d)
	}
	if _, err := tsdb.ParseRange(from, to, time.Now()); err != nil {
		return "", "", err
	}
	return from, to, nil
}

// shiftEnd moves one range end back: a relative end gains an offset, so it
// stays relative and cacheable; an absolute end is resolved and moved.
func shiftEnd(end string, d time.Duration) string {
	if strings.HasPrefix(end, "now") {
		return end + "-" + tsdb.FormatDuration(d)
	}
	t, err := tsdb.ParseTime(end, time.Now(), false)
	if err != nil {
		return end
	}
	return fmt.Sprint(t.Add(-d).UnixMilli())
}

// RefreshInterval returns the chart's refresh, at least 5 seconds; zero
// means it loads once.
func RefreshInterval(c *Config, values dashscope.VariableValues) time.Duration {
	r := strings.TrimSpace(dashscope.Substitute(c.Refresh, values))
	if r == "" {
		return defaultRefresh
	}
	if r == "off" {
		return 0
	}
	d, err := tsdb.ParseDuration(r)
	if err != nil {
		return defaultRefresh
	}
	return max(d, minCacheTTL)
}

// Expand builds the tsdb query for one chart query: connection substituted,
// the range applied, and the maximum data points defaulted to the drawing
// width. The returned function substitutes variables into the query text;
// it runs after macro expansion, so a value that looks like a macro is not
// expanded into unescaped text.
func Expand(q Query, values dashscope.VariableValues, from, to string, width, chartMax int) (string, tsdb.Query, func(string) string) {
	if width <= 0 {
		width = 1000
	}
	width = min(max(width, minWidth), maxWidth)
	maxPoints := q.MaxDataPoints
	if maxPoints <= 0 {
		maxPoints = chartMax
	}
	if maxPoints <= 0 {
		maxPoints = width
	}
	tq := tsdb.Query{
		RefID:         q.RefID,
		Language:      q.Language,
		Text:          q.Text,
		From:          from,
		To:            to,
		MaxDataPoints: maxPoints,
		MinInterval:   dashscope.Substitute(q.MinInterval, values),
		Format:        q.Format,
		LegendFormat:  dashscope.Substitute(q.LegendFormat, values),
		Instant:       q.Instant,
	}
	if q.HA != nil {
		ha := *q.HA
		ha.Entities = substituteList(ha.Entities, values)
		ha.StatisticIDs = substituteList(ha.StatisticIDs, values)
		ha.Attribute = dashscope.Substitute(ha.Attribute, values)
		tq.HA = &ha
	}
	if q.Energy != nil {
		e := *q.Energy
		e.Targets = substituteList(e.Targets, values)
		tq.Energy = &e
	}
	post := func(text string) string { return dashvars.SubstituteQuery(text, q.Language, values) }
	return strings.TrimSpace(dashscope.Substitute(q.ConnectionID, values)), tq, post
}

func substituteList(in []string, values dashscope.VariableValues) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = dashscope.Substitute(s, values)
	}
	return out
}

// Run executes the chart's queries concurrently through the cache.
func Run(ctx context.Context, c *Config, values dashscope.VariableValues, req Request) (*Response, error) {
	from, to, err := Range(c, values, req.From, req.To)
	if err != nil {
		return nil, &ValueError{Message: err.Error()}
	}
	refresh := RefreshInterval(c, values)
	want := map[string]bool{}
	for _, r := range req.Refs {
		want[r] = true
	}
	resp := &Response{Results: []QueryResult{}}
	var run []Query
	for i, q := range c.Queries {
		if q.RefID == "" {
			q.RefID = string(rune('A' + i))
		}
		if len(want) > 0 && !want[q.RefID] {
			continue
		}
		run = append(run, q)
	}
	clock, weekStart := rangeClock(c.Queries, values)
	if r, err := tsdb.ParseRangeWeek(from, to, clock, weekStart); err == nil {
		resp.Range = RangeBody{From: r.From.UnixMilli(), To: r.To.UnixMilli()}
	}
	resp.Results = make([]QueryResult, len(run))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for idx, q := range run {
		resp.Results[idx].RefID = q.RefID
		wg.Add(1)
		go func(idx int, q Query) {
			defer wg.Done()
			connID, tq, post := Expand(q, values, from, to, req.Width, c.MaxDataPoints)
			res, cached, err := cachedQuery(ctx, connID, tq, refresh, post)
			mu.Lock()
			defer mu.Unlock()
			out := &resp.Results[idx]
			out.Cached = cached
			if err != nil {
				out.Error = errorBody(err)
				return
			}
			out.Result = res
			if res.IntervalMs > resp.IntervalMs {
				resp.IntervalMs = res.IntervalMs
			}
		}(idx, q)
	}
	wg.Wait()
	return resp, nil
}

// rangeClock is the time the reported range is resolved against and the ISO
// weekday its weeks start on. The energy connection rounds calendar ranges
// in the energy time zone and on the energy week start, so a chart that
// reads nothing else reports its range there too, and the axis the widget
// draws over it matches the data. A chart mixing connections keeps the
// server's zone and Monday weeks, which its other queries ran in.
func rangeClock(queries []Query, values dashscope.VariableValues) (time.Time, int) {
	now := time.Now()
	if len(queries) == 0 {
		return now, 1
	}
	for _, q := range queries {
		if strings.TrimSpace(dashscope.Substitute(q.ConnectionID, values)) != tsdb.EnergyConnection {
			return now, 1
		}
	}
	return now.In(energy.Location()), energy.CurrentSettings().WeekStart
}

// Preview runs one unsaved query, bypassing the cache.
func Preview(ctx context.Context, q Query, values dashscope.VariableValues, from, to string, width int) (*tsdb.Result, error) {
	if from == "" && to == "" {
		from, to = tsdb.DefaultFrom, tsdb.DefaultTo
	}
	connID, tq, post := Expand(q, values, from, to, width, 0)
	return tsdbops.QueryWith(ctx, connID, tq, post)
}

func errorBody(err error) *ErrorBody {
	var te *tsdb.Error
	if errors.As(err, &te) {
		return &ErrorBody{Class: te.Class, Message: te.Error()}
	}
	var ae *apperrors.AppError
	if errors.As(err, &ae) {
		class := tsdb.ClassRejected
		if ae.StatusCode == 502 || ae.StatusCode == 503 {
			class = tsdb.ClassUnavailable
		}
		return &ErrorBody{Class: class, Message: ae.Error()}
	}
	return &ErrorBody{Class: tsdb.ClassUnavailable, Message: err.Error()}
}

// cache holds recent results by prepared query key.
type cacheEntry struct {
	at      time.Time
	ttl     time.Duration
	result  *tsdb.Result
	err     error
	pending chan struct{}
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*cacheEntry{}
)

// Querier runs a prepared query. A variable so tests can count database
// calls.
var Querier = tsdbops.Run

// cachedQuery runs a chart query through the shared cache.
func cachedQuery(ctx context.Context, connID string, q tsdb.Query, refresh time.Duration, post func(string) string) (*tsdb.Result, bool, error) {
	res, cached, err := sharedQuery(ctx, connID, q, refresh, post)
	if res == nil || err != nil {
		return res, cached, err
	}
	// A cached result is shared by every chart that asked for the same
	// query, so each caller gets its own copy naming its own query.
	out := *res
	out.Series = make([]tsdb.Series, len(res.Series))
	for i, series := range res.Series {
		series.RefID = q.RefID
		out.Series[i] = series
	}
	return &out, cached, nil
}

// sharedQuery answers from the cache, or runs the query once for every
// caller asking for the same key at the same time.
func sharedQuery(ctx context.Context, connID string, q tsdb.Query, refresh time.Duration, post func(string) string) (*tsdb.Result, bool, error) {
	src, err := tsdbops.Resolve(connID)
	if err != nil {
		return nil, false, err
	}
	p, err := tsdbops.PrepareWith(src, q, time.Now(), post)
	if err != nil {
		return nil, false, err
	}
	ttl := refresh
	if ttl <= 0 || p.Request.Interval < ttl {
		ttl = p.Request.Interval
	}
	ttl = min(max(ttl, minCacheTTL), maxCacheTTL)

	cacheMu.Lock()
	if e, ok := cache[p.Key]; ok {
		if e.pending != nil {
			ch := e.pending
			cacheMu.Unlock()
			select {
			case <-ch:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
			// The entry waited on was filled before its channel closed;
			// reading it rather than the map again means an eviction in
			// between cannot hand back an empty answer.
			return e.result, true, e.err
		}
		if time.Since(e.at) < e.ttl {
			cacheMu.Unlock()
			return e.result, true, e.err
		}
	}
	entry := &cacheEntry{pending: make(chan struct{})}
	cache[p.Key] = entry
	if len(cache) > maxCacheItems {
		evictLocked()
	}
	cacheMu.Unlock()

	// The query outlives a viewer who gives up, because others may be
	// waiting on it.
	res, err := Querier(context.WithoutCancel(ctx), src, p)

	cacheMu.Lock()
	entry.at, entry.result, entry.err, entry.ttl = time.Now(), res, err, ttl
	if err != nil {
		entry.ttl = errorCacheTTL
	}
	close(entry.pending)
	entry.pending = nil
	cacheMu.Unlock()
	return res, false, err
}

// evictLocked drops expired entries, then the oldest, until the cache fits.
func evictLocked() {
	now := time.Now()
	for k, e := range cache {
		if e.pending == nil && now.Sub(e.at) >= e.ttl {
			delete(cache, k)
		}
	}
	for len(cache) > maxCacheItems {
		var oldestKey string
		var oldest time.Time
		for k, e := range cache {
			if e.pending != nil {
				continue
			}
			if oldestKey == "" || e.at.Before(oldest) {
				oldestKey, oldest = k, e.at
			}
		}
		if oldestKey == "" {
			return
		}
		delete(cache, oldestKey)
	}
}
