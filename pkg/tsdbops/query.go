// Package tsdbops is the service layer of the time-series connections:
// queries and metadata lookups against any readable source, history forward
// management, and writer status. REST, MCP, flow nodes, dashboard charts, and
// dashboard variables all call it, so they share one set of rules.
package tsdbops

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/energy"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// Source is a readable connection resolved for querying.
type Source struct {
	Connection *models.Connection
	Querier    tsdb.Querier
}

// Resolve finds a connection's querier. Home Assistant connections are read
// through their running driver; other kinds must implement tsdb.Querier.
func Resolve(connID string) (*Source, error) {
	if connID == tsdb.EnergyConnection {
		return &Source{Connection: &models.Connection{Name: "Energy", Kind: tsdb.EnergyConnection}, Querier: energy.Querier{}}, nil
	}
	c, err := connops.Load(connID)
	if err != nil {
		return nil, err
	}
	if c.Kind == homeassistant.Kind {
		d, err := connops.HADriver(connID)
		if err != nil {
			return nil, err
		}
		return &Source{Connection: c, Querier: haQuerier{d}}, nil
	}
	mgr := connops.Manager()
	if mgr == nil {
		return nil, apperrors.ErrBadGateway.WithMessage("connection manager is not running")
	}
	d, ok := mgr.Get(c.ID)
	if !ok {
		if !tsdb.IsReadable(c.Kind) && !tsdb.IsWritable(c.Kind) {
			return nil, apperrors.ErrValidation.WithMessage("connection is not a time-series connection")
		}
		return nil, apperrors.ErrBadGateway.WithMessage("connection is not running; enable it first")
	}
	q, ok := d.(tsdb.Querier)
	if !ok {
		return nil, &tsdb.Error{Class: tsdb.ClassUnsupported, Message: fmt.Sprintf("%s connections do not answer queries", c.Kind)}
	}
	return &Source{Connection: c, Querier: q}, nil
}

// Prepared is a query ready to run, and the key that identifies its result.
type Prepared struct {
	Request tsdb.Request
	// Key identifies the expanded query for caching: the same key means the
	// same request to the database.
	Key string
}

// Prepare resolves a query's range and interval and expands its macros. now
// is a parameter so a cache can align relative ranges to the interval.
func Prepare(src *Source, q tsdb.Query, now time.Time) (*Prepared, error) {
	return PrepareWith(src, q, now, nil)
}

// PrepareWith is Prepare with a substitution applied to the query text
// after its macros are expanded, so a variable value that looks like a
// macro is inserted as written rather than expanded.
func PrepareWith(src *Source, q tsdb.Query, now time.Time, post func(string) string) (*Prepared, error) {
	if q.Language == "" {
		return nil, apperrors.ErrValidation.WithMessage("query language is required")
	}
	if langs, ok := src.Querier.(tsdb.Languages); ok && !slices.Contains(langs.Languages(), q.Language) {
		return nil, &tsdb.Error{Class: tsdb.ClassUnsupported,
			Message: fmt.Sprintf("this connection does not accept %s; it accepts %s", q.Language, strings.Join(langs.Languages(), ", "))}
	}
	// Energy days, weeks, and months start in the energy time zone and
	// weeks on the energy week start, so a calendar range on that connection
	// is rounded there rather than in the caller's zone.
	weekStart := 1
	if src.Connection != nil && src.Connection.Kind == tsdb.EnergyConnection {
		now = now.In(energy.Location())
		weekStart = energy.CurrentSettings().WeekStart
	}
	r, err := tsdb.ParseRangeWeek(q.From, q.To, now, weekStart)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage(err.Error())
	}
	var minInterval time.Duration
	if q.MinInterval != "" {
		if minInterval, err = tsdb.ParseDuration(q.MinInterval); err != nil {
			return nil, apperrors.ErrValidation.WithMessage("min_interval: " + err.Error())
		}
	}
	if q.MaxDataPoints <= 0 {
		q.MaxDataPoints = tsdb.DefaultMaxDataPoints
	}
	if q.MaxDataPoints > 11000 {
		q.MaxDataPoints = 11000
	}
	interval := tsdb.CalculateInterval(r, q.MaxDataPoints, minInterval)
	// Align the ends that slide with the clock to the interval so every
	// caller asking within one interval sends the database the same request.
	// The end rounds up, so the newest data and an instant query's
	// evaluation time are never cut off; a few seconds past now return
	// nothing extra.
	//
	// An end rounded to a calendar unit ("now/M") and an absolute end are
	// already the same for every caller, and they are boundaries in the
	// caller's zone. Moving one on the UTC grid would pull an interval wider
	// than the zone offset into the neighbouring day or month.
	step := interval
	if step < time.Second {
		step = time.Second
	}
	if slides(q.To, tsdb.DefaultTo) {
		if t := r.To.Truncate(step); t.Before(r.To) {
			r.To = t.Add(step)
		}
	}
	if slides(q.From, tsdb.DefaultFrom) {
		r.From = r.From.Truncate(step)
	}
	scrape := tsdb.DefaultScrapeInterval
	if s, ok := src.Querier.(tsdb.ScrapeSource); ok && s.ScrapeInterval() > 0 {
		scrape = s.ScrapeInterval()
	}
	expanded := q
	expanded.Text = tsdb.ExpandMacros(q.Text, q.Language, r, interval, tsdb.RateInterval(interval, scrape))
	if post != nil {
		expanded.Text = post(expanded.Text)
	}
	req := tsdb.Request{Query: expanded, Range: r, Interval: interval}
	key := fmt.Sprintf("%s|%s|%s|%d|%d|%d|%d|%v|%s|%s", src.Connection.ID, q.Language, expanded.Text,
		r.From.UnixMilli(), r.To.UnixMilli(), interval.Milliseconds(), q.MaxDataPoints, q.Instant, q.Format, q.LegendFormat)
	if q.Energy != nil {
		key += fmt.Sprintf("|%v|%s|%s", q.Energy.Targets, q.Energy.Measure, q.Energy.Period)
	}
	if q.HA != nil {
		key += fmt.Sprintf("|%v|%s|%v|%s|%v", q.HA.Entities, q.HA.Attribute, q.HA.StatisticIDs, q.HA.Period, q.HA.Types)
	}
	return &Prepared{Request: req, Key: key}, nil
}

// slides reports whether a range end moves with the clock: relative to now
// and not rounded to a calendar unit. An empty end takes its default.
func slides(end, def string) bool {
	end = strings.TrimSpace(end)
	if end == "" {
		end = def
	}
	return strings.HasPrefix(end, "now") && !strings.Contains(end, "/")
}

// Run executes a prepared query under the connection's timeout and bounds
// its result.
func Run(ctx context.Context, src *Source, p *Prepared) (*tsdb.Result, error) {
	timeout := tsdb.DefaultTimeout
	if t, ok := src.Querier.(tsdb.TimeoutSource); ok && t.QueryTimeout() > 0 {
		timeout = t.QueryTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := src.Querier.Query(ctx, p.Request)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &tsdb.Error{Class: tsdb.ClassUnavailable, Message: fmt.Sprintf("query timed out after %s", timeout)}
		}
		return nil, tsdb.Classify(err)
	}
	if res.Series == nil {
		res.Series = []tsdb.Series{}
	}
	for i := range res.Series {
		if res.Series[i].RefID == "" {
			res.Series[i].RefID = p.Request.RefID
		}
	}
	tsdb.Limit(res)
	res.ExecutedQuery = p.Request.Text
	res.IntervalMs = p.Request.Interval.Milliseconds()
	return res, nil
}

// Query resolves, prepares, and runs one query.
func Query(ctx context.Context, connID string, q tsdb.Query) (*tsdb.Result, error) {
	return QueryWith(ctx, connID, q, nil)
}

// QueryWith is Query with a substitution applied after macro expansion.
func QueryWith(ctx context.Context, connID string, q tsdb.Query, post func(string) string) (*tsdb.Result, error) {
	src, err := Resolve(connID)
	if err != nil {
		return nil, err
	}
	p, err := PrepareWith(src, q, time.Now(), post)
	if err != nil {
		return nil, err
	}
	return Run(ctx, src, p)
}

// Metadata answers one lookup.
func Metadata(ctx context.Context, connID string, req tsdb.MetaRequest) ([]string, error) {
	src, err := Resolve(connID)
	if err != nil {
		return nil, err
	}
	timeout := tsdb.DefaultTimeout
	if t, ok := src.Querier.(tsdb.TimeoutSource); ok && t.QueryTimeout() > 0 {
		timeout = t.QueryTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	values, err := src.Querier.Metadata(ctx, req)
	if err != nil {
		return nil, tsdb.Classify(err)
	}
	if len(values) > tsdb.MaxMetaValues {
		values = values[:tsdb.MaxMetaValues]
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

// AppError turns a query failure into the API error REST and MCP report:
// unavailable sources are 502, refused or unsupported queries 422.
func AppError(err error) error {
	if e, ok := err.(*tsdb.Error); ok {
		if e.Class == tsdb.ClassUnavailable {
			return apperrors.ErrBadGateway.WithMessage(e.Error())
		}
		return apperrors.ErrUnprocessable.WithMessage(e.Error())
	}
	return err
}
