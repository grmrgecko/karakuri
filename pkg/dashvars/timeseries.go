package dashvars

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
)

// tsCacheTTL bounds how stale a time-series query's options may be, the same
// window Home Assistant queries get from the driver's state cache.
const tsCacheTTL = 30 * time.Second

// IsTimeSeriesSource reports whether a query source reads a time-series
// connection rather than Home Assistant states.
func IsTimeSeriesSource(source string) bool {
	switch source {
	case models.QueryPrometheusLabelValues, models.QueryPrometheusMetrics, models.QueryPrometheusQuery,
		models.QueryInflux, models.QuerySQL:
		return true
	}
	return false
}

// queryRefRe matches a reference with any of the formats substitution
// understands. It matches frontend/src/lib/variables.ts.
var queryRefRe = regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_]{0,31})(?::(text|raw|regex|sqlstring|from|to))?\}`)

// QueryReferences returns the variable names a query's text and selector
// refer to.
func QueryReferences(q *models.VariableQuery) map[string]bool {
	refs := map[string]bool{}
	if q == nil {
		return refs
	}
	for _, s := range []string{q.Text, q.Match, q.Label} {
		for _, m := range queryRefRe.FindAllStringSubmatch(s, -1) {
			refs[m[1]] = true
		}
	}
	return refs
}

// SubstituteQuery replaces variable references in time-series query text.
// A plain ${name} is escaped for the language's string literals so a value
// cannot change the query's structure; ${name:raw} inserts it unchanged,
// ${name:regex} escapes regular-expression metacharacters, ${name:sqlstring}
// quotes it as a SQL string, ${name:text} inserts the label escaped, and
// ${name:from} and ${name:to} insert a time range's ends. Unknown names are
// left as written.
func SubstituteQuery(text, lang string, values map[string]models.VariableOption) string {
	if !strings.Contains(text, "${") {
		return text
	}
	return queryRefRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := queryRefRe.FindStringSubmatch(m)
		o, ok := values[parts[1]]
		if !ok {
			return m
		}
		switch parts[2] {
		case "raw":
			return o.Value
		case "regex":
			return tsdb.RegexEscape(lang, o.Value)
		case "sqlstring":
			return tsdb.SQLString(o.Value)
		case "text":
			return tsdb.EscapeValue(lang, o.Text())
		case "from", "to":
			from, to, ok := tsdb.SplitRangeValue(o.Value)
			if !ok {
				return m
			}
			if parts[2] == "from" {
				return tsdb.EscapeValue(lang, from)
			}
			return tsdb.EscapeValue(lang, to)
		}
		return tsdb.EscapeValue(lang, o.Value)
	})
}

type tsCacheEntry struct {
	at     time.Time
	result Result
}

var (
	tsCacheMu sync.Mutex
	tsCache   = map[string]tsCacheEntry{}
)

// TimeSeriesOptions is how a query variable over a time-series connection
// gets its options. It is a variable so tests can supply them.
var TimeSeriesOptions = timeSeriesOptions

// timeSeriesOptions runs one query variable's query with the given values of
// the variables it references and the range its time macros use.
func timeSeriesOptions(ctx context.Context, q *models.VariableQuery, values map[string]models.VariableOption, rangeValue string) Result {
	lang := tsdb.LangPromQL
	switch q.Source {
	case models.QueryInflux:
		lang = q.Language
		if lang == "" {
			lang = tsdb.LangInfluxQL
		}
	case models.QuerySQL:
		lang = tsdb.LangSQL
	}
	// The query text is substituted after macro expansion (see QueryWith);
	// this copy only keys the cache.
	text := SubstituteQuery(q.Text, lang, values)
	post := func(t string) string { return SubstituteQuery(t, lang, values) }
	match := SubstituteQuery(q.Match, tsdb.LangPromQL, values)
	from, to, ok := tsdb.SplitRangeValue(rangeValue)
	if !ok {
		from, to = tsdb.DefaultFrom, tsdb.DefaultTo
	}
	key := optionCacheKey(q, lang, match, text, from, to)
	tsCacheMu.Lock()
	if e, ok := tsCache[key]; ok && time.Since(e.at) < tsCacheTTL {
		tsCacheMu.Unlock()
		return e.result
	}
	tsCacheMu.Unlock()

	var items []models.VariableOption
	var err error
	switch q.Source {
	case models.QueryPrometheusLabelValues:
		if q.Label == "" {
			return Result{Err: fmt.Errorf("a label values query needs a label")}
		}
		var names []string
		names, err = tsdbops.Metadata(ctx, q.ConnectionID, tsdb.MetaRequest{Kind: tsdb.MetaLabelValues, Name: q.Label, Match: match})
		for _, n := range names {
			items = append(items, models.VariableOption{Value: n})
		}
	case models.QueryPrometheusMetrics:
		var names []string
		names, err = tsdbops.Metadata(ctx, q.ConnectionID, tsdb.MetaRequest{Kind: tsdb.MetaMetrics, Match: match})
		for _, n := range names {
			items = append(items, models.VariableOption{Value: n})
		}
	case models.QueryPrometheusQuery:
		var res *tsdb.Result
		res, err = tsdbops.QueryWith(ctx, q.ConnectionID, tsdb.Query{Language: tsdb.LangPromQL, Text: q.Text, From: from, To: to, Instant: true}, post)
		if err == nil {
			for _, s := range res.Series {
				v := s.Name
				if q.Label != "" {
					v = s.Labels[q.Label]
				}
				if v != "" {
					items = append(items, models.VariableOption{Value: v})
				}
			}
		}
	case models.QueryInflux, models.QuerySQL:
		var res *tsdb.Result
		res, err = tsdbops.QueryWith(ctx, q.ConnectionID, tsdb.Query{Language: lang, Text: q.Text, From: from, To: to, Format: tsdb.FormatTable}, post)
		if err == nil {
			items = tableOptions(res, q.Source == models.QuerySQL)
		}
	default:
		return Result{Err: fmt.Errorf("unknown query source %q", q.Source)}
	}
	if err != nil {
		r := Result{Err: err}
		cacheResult(key, r)
		return r
	}

	var re *regexp.Regexp
	if q.Regex != "" {
		if re, err = regexp.Compile(q.Regex); err != nil {
			return Result{Err: fmt.Errorf("query regex: %w", err)}
		}
	}
	var r Result
	for _, it := range items {
		if o, ok := extract(re, it.Value, it.Label); ok {
			r.Options = append(r.Options, o)
		}
	}
	r.Options = dedupe(r.Options)
	sortOptions(r.Options, q.Sort)
	if len(r.Options) > MaxQueryOptions {
		r.Options = r.Options[:MaxQueryOptions]
		r.Truncated = true
	}
	cacheResult(key, r)
	return r
}

// optionCacheKey identifies one variable's cached options. The cache holds
// them after the regex and sort are applied, so both are part of the key.
func optionCacheKey(q *models.VariableQuery, lang, match, text, from, to string) string {
	return strings.Join([]string{q.ConnectionID, q.Source, q.Label, match, text, lang, from, to, q.Regex, q.Sort}, "\x00")
}

func cacheResult(key string, r Result) {
	tsCacheMu.Lock()
	defer tsCacheMu.Unlock()
	if len(tsCache) > 1024 {
		tsCache = map[string]tsCacheEntry{}
	}
	tsCache[key] = tsCacheEntry{at: time.Now(), result: r}
}

// tableOptions reads options from a table result: the __value and __text
// columns when present; for InfluxQL's SHOW TAG VALUES, whose rows are
// key and value, the value column; otherwise the first column that is not
// a time, with the next one as the label for SQL.
func tableOptions(res *tsdb.Result, sql bool) []models.VariableOption {
	var out []models.VariableOption
	for _, s := range res.Series {
		valueIdx, textIdx, keyIdx, plainValueIdx := -1, -1, -1, -1
		for i, f := range s.Fields {
			switch f.Name {
			case "__value":
				valueIdx = i
			case "__text":
				textIdx = i
			case "key":
				keyIdx = i
			case "value":
				plainValueIdx = i
			}
		}
		if valueIdx < 0 && keyIdx >= 0 && plainValueIdx >= 0 {
			valueIdx = plainValueIdx
		}
		if valueIdx < 0 {
			for i, f := range s.Fields {
				if f.Type == tsdb.FieldTime {
					continue
				}
				if valueIdx < 0 {
					valueIdx = i
				} else if sql && textIdx < 0 {
					textIdx = i
					break
				}
			}
		}
		if valueIdx < 0 {
			continue
		}
		for row := 0; row < s.Len(); row++ {
			v := s.Fields[valueIdx].Values[row]
			if v == nil {
				continue
			}
			o := models.VariableOption{Value: cellText(v)}
			if textIdx >= 0 && s.Fields[textIdx].Values[row] != nil {
				o.Label = cellText(s.Fields[textIdx].Values[row])
			}
			out = append(out, o)
		}
	}
	return out
}

// cellText writes a table cell as option text. A number is written as a
// plain decimal: integer columns arrive as float64, and the default
// formatting turns 1234567 into 1.234567e+06, which no query matches.
func cellText(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func sortOptions(options []models.VariableOption, how string) {
	switch how {
	case "asc", "desc":
		sort.SliceStable(options, func(i, j int) bool {
			a, b := strings.ToLower(options[i].Text()), strings.ToLower(options[j].Text())
			if how == "desc" {
				return a > b
			}
			return a < b
		})
	}
}
