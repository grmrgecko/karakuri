// Package dashvars resolves dashboard variables into the options they take.
// A custom variable lists its own; a constant has its one value; a query asks
// Home Assistant. Everything that decides from a variable's options, which is
// dashboard scope, live commands, renders, and validation, resolves through
// here so they agree on what a query returned.
package dashvars

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// MaxQueryOptions is how many options a query keeps after sorting, the same
// limit a custom variable's list has.
const MaxQueryOptions = 50

// resolveTimeout bounds a resolution that has to reach Home Assistant. The
// driver answers from its own 30-second state cache when it can.
const resolveTimeout = 5 * time.Second

// Result is one variable's resolved options and, for a followed select, the
// value its entity's state maps to.
type Result struct {
	Options   []models.VariableOption
	Truncated bool
	// Followed is the option value the select entity's state maps to, empty
	// when the variable does not follow or the state is not an option.
	Followed string
	// Mapping maps each select option as Home Assistant spells it to the
	// option value it produced, so a viewer can follow state changes.
	Mapping map[string]string
	Err     error
}

// States returns a Home Assistant connection's entity states. It is a
// variable so tests can resolve against captured states.
var States = func(ctx context.Context, connectionID string) ([]homeassistant.Entity, error) {
	driver, err := connops.HADriver(connectionID)
	if err != nil {
		return nil, err
	}
	return driver.StatesCached(ctx)
}

// Resolve returns copies of the variables with Options set for every type, and
// the followed values by variable name. A query that fails resolves to no
// options, which grants nothing and leaves references as written.
func Resolve(vars []models.DashboardVariable) ([]models.DashboardVariable, map[string]string) {
	resolved, followed, _ := ResolveFollowing(vars)
	return resolved, followed
}

// ResolveFollowing is Resolve that also returns, for each followed variable,
// how its entity's states map to option values. A time-series query that
// references earlier variables resolves to the union of its options over
// every combination of theirs, which is what dashboard scope grants.
func ResolveFollowing(vars []models.DashboardVariable) ([]models.DashboardVariable, map[string]string, map[string]map[string]string) {
	return resolve(vars, nil, true)
}

// ResolveFor resolves the variables as one viewer sees them: a time-series
// query that references earlier variables runs with that viewer's values of
// them. Values that are not options of their variable are ignored in favour
// of the default.
func ResolveFor(vars []models.DashboardVariable, values map[string]string) ([]models.DashboardVariable, map[string]string) {
	resolved, followed, _ := resolve(vars, values, false)
	return resolved, followed
}

// ResolveFollowingFor is ResolveFollowing as one viewer sees it, for
// viewers re-resolving a chained query after a pick.
func ResolveFollowingFor(vars []models.DashboardVariable, values map[string]string) ([]models.DashboardVariable, map[string]string, map[string]map[string]string) {
	return resolve(vars, values, false)
}

func resolve(vars []models.DashboardVariable, viewer map[string]string, union bool) ([]models.DashboardVariable, map[string]string, map[string]map[string]string) {
	if len(vars) == 0 {
		return nil, nil, nil
	}
	mappings := map[string]map[string]string{}
	out := make([]models.DashboardVariable, len(vars))
	followed := map[string]string{}
	current := map[string]models.VariableOption{}
	var ctx context.Context
	var cancel context.CancelFunc
	withCtx := func() context.Context {
		if ctx == nil {
			ctx, cancel = context.WithTimeout(context.Background(), resolveTimeout)
		}
		return ctx
	}
	for i, v := range vars {
		out[i] = v
		switch v.Kind() {
		case models.VariableTypeConstant:
			out[i].Options = []models.VariableOption{{Value: v.Value}}
		case models.VariableTypeTimeRange:
			if len(v.Options) == 0 {
				out[i].Options = append([]models.VariableOption(nil), models.DefaultTimePresets...)
			}
		case models.VariableTypeInterval:
			if v.Auto && !hasOption(v.Options, models.AutoInterval) {
				out[i].Options = append([]models.VariableOption{{Value: models.AutoInterval, Label: "Auto"}}, v.Options...)
			}
		case models.VariableTypeQuery:
			if v.Query != nil && IsTimeSeriesSource(v.Query.Source) {
				out[i].Options = timeSeriesVariable(withCtx(), v, out[:i], current, union)
				break
			}
			r := Query(withCtx(), v.Query)
			out[i].Options = r.Options
			if v.Follows() {
				if r.Followed != "" {
					followed[v.Name] = r.Followed
				}
				mappings[v.Name] = r.Mapping
			}
		}
		// The value later variables see: the viewer's when it is an option,
		// else a followed state, else the default.
		var option models.VariableOption
		ok := false
		if ref, has := viewer[v.Name]; has {
			option, ok = out[i].Resolve(ref)
		}
		if !ok {
			if f, has := followed[v.Name]; has {
				option, ok = out[i].Resolve(f)
			}
		}
		if !ok {
			option, ok = out[i].DefaultOption()
		}
		if ok {
			// An interval's auto value is a duration by the time a later
			// query reads it.
			current[v.Name] = EffectiveValue(out[i], option, current)
		}
	}
	if cancel != nil {
		cancel()
	}
	return out, followed, mappings
}

// timeSeriesVariable resolves a query variable over a time-series
// connection. Its time macros use the named range variable's value.
func timeSeriesVariable(ctx context.Context, v models.DashboardVariable, earlier []models.DashboardVariable, current map[string]models.VariableOption, union bool) []models.VariableOption {
	q := v.Query
	rangeValue := ""
	if q.RangeVariable != "" {
		rangeValue = current[q.RangeVariable].Value
	}
	refs := QueryReferences(q)
	var upstream []models.DashboardVariable
	for _, e := range earlier {
		if refs[e.Name] && e.Kind() != models.VariableTypeTimeRange {
			upstream = append(upstream, e)
		}
	}
	if !union || len(upstream) == 0 {
		return TimeSeriesOptions(ctx, q, current, rangeValue).Options
	}
	combos := []map[string]models.VariableOption{{}}
	for _, u := range upstream {
		if len(u.Options) == 0 {
			continue
		}
		if len(combos)*len(u.Options) > maxCombinations {
			return nil
		}
		next := make([]map[string]models.VariableOption, 0, len(combos)*len(u.Options))
		for _, c := range combos {
			for _, o := range u.Options {
				m := make(map[string]models.VariableOption, len(current))
				for k, val := range current {
					m[k] = val
				}
				for k, val := range c {
					m[k] = val
				}
				// An interval's auto option reaches the query as the
				// duration it stands for, as it does for a viewer.
				m[u.Name] = EffectiveValue(u, o, m)
				next = append(next, m)
			}
		}
		combos = next
	}
	var all []models.VariableOption
	for _, c := range combos {
		all = append(all, TimeSeriesOptions(ctx, q, c, rangeValue).Options...)
	}
	all = dedupe(all)
	sortOptions(all, q.Sort)
	if len(all) > MaxQueryOptions {
		all = all[:MaxQueryOptions]
	}
	return all
}

// maxCombinations matches dashscope.MaxVariableCombinations, which this
// package cannot import.
const maxCombinations = 256

func hasOption(options []models.VariableOption, value string) bool {
	for _, o := range options {
		if o.Value == value {
			return true
		}
	}
	return false
}

// EffectiveValue resolves an interval variable's auto option to a duration
// sized from its range variable's value; other values pass through.
func EffectiveValue(v models.DashboardVariable, option models.VariableOption, values map[string]models.VariableOption) models.VariableOption {
	if v.Kind() != models.VariableTypeInterval || option.Value != models.AutoInterval {
		return option
	}
	span := 6 * time.Hour
	if rv, ok := values[v.RangeVariable]; ok {
		if from, to, ok := tsdb.SplitRangeValue(rv.Value); ok {
			if r, err := tsdb.ParseRange(from, to, time.Now()); err == nil {
				span = r.Span()
			}
		}
	}
	var min time.Duration
	if v.MinInterval != "" {
		min, _ = tsdb.ParseDuration(v.MinInterval)
	}
	return models.VariableOption{Value: tsdb.FormatDuration(tsdb.AutoInterval(span, v.Steps, min)), Label: option.Label}
}

// Query resolves one query against its source.
func Query(ctx context.Context, q *models.VariableQuery) Result {
	if q == nil {
		return Result{Err: fmt.Errorf("query variable has no query")}
	}
	if IsTimeSeriesSource(q.Source) {
		return TimeSeriesOptions(ctx, q, nil, "")
	}
	states, err := States(ctx, q.ConnectionID)
	if err != nil {
		return Result{Err: err}
	}
	return FromStates(q, states)
}

// FromStates builds a query's options from entity states.
func FromStates(q *models.VariableQuery, states []homeassistant.Entity) Result {
	var re *regexp.Regexp
	if q.Regex != "" {
		var err error
		if re, err = regexp.Compile(q.Regex); err != nil {
			return Result{Err: fmt.Errorf("query regex: %w", err)}
		}
	}

	var r Result
	switch q.Source {
	case models.QueryHAEntities:
		match, err := patternRegexp(q.Pattern)
		if err != nil {
			return Result{Err: err}
		}
		for _, e := range states {
			if !match.MatchString(e.EntityID) {
				continue
			}
			name, _ := e.Attributes["friendly_name"].(string)
			if o, ok := extract(re, e.EntityID, name); ok {
				r.Options = append(r.Options, o)
			}
		}
	case models.QueryHASelect:
		entity, ok := findEntity(states, q.EntityID)
		if !ok {
			return Result{Err: fmt.Errorf("entity %s not found", q.EntityID)}
		}
		raw, _ := entity.Attributes["options"].([]any)
		for _, item := range raw {
			text, ok := item.(string)
			if !ok {
				continue
			}
			o, ok := extract(re, text, "")
			if !ok {
				continue
			}
			r.Options = append(r.Options, o)
			if r.Mapping == nil {
				r.Mapping = map[string]string{}
			}
			r.Mapping[text] = o.Value
			if text == entity.State {
				r.Followed = o.Value
			}
		}
	default:
		return Result{Err: fmt.Errorf("query source must be %s or %s", models.QueryHAEntities, models.QueryHASelect)}
	}

	r.Options = dedupe(r.Options)
	switch q.Sort {
	case "asc", "desc":
		sort.SliceStable(r.Options, func(i, j int) bool {
			a, b := strings.ToLower(r.Options[i].Text()), strings.ToLower(r.Options[j].Text())
			if q.Sort == "desc" {
				return a > b
			}
			return a < b
		})
	}
	if len(r.Options) > MaxQueryOptions {
		r.Options = r.Options[:MaxQueryOptions]
		r.Truncated = true
	}
	return r
}

// patternRegexp turns an entity ID pattern, where * is any run of characters,
// into an anchored regexp.
func patternRegexp(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("an entity query needs a pattern")
	}
	quoted := strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, `.*`)
	return regexp.Compile("^" + quoted + "$")
}

// extract applies a query's regex to one item. A value group, or else the
// first group, or else the whole match becomes the value; a text group becomes
// the label. Without a regex the item is the value and label the label.
func extract(re *regexp.Regexp, item, label string) (models.VariableOption, bool) {
	if re == nil {
		return models.VariableOption{Value: item, Label: label}, true
	}
	m := re.FindStringSubmatch(item)
	if m == nil {
		return models.VariableOption{}, false
	}
	value := m[0]
	if len(m) > 1 {
		value = m[1]
	}
	for i, name := range re.SubexpNames() {
		switch name {
		case "value":
			value = m[i]
		case "text":
			if m[i] != "" {
				label = m[i]
			}
		}
	}
	if value == "" {
		return models.VariableOption{}, false
	}
	if label == value {
		label = ""
	}
	return models.VariableOption{Value: value, Label: label}, true
}

// dedupe keeps the first option for each value.
func dedupe(options []models.VariableOption) []models.VariableOption {
	seen := map[string]bool{}
	out := options[:0]
	for _, o := range options {
		if seen[o.Value] {
			continue
		}
		seen[o.Value] = true
		out = append(out, o)
	}
	return out
}

func findEntity(states []homeassistant.Entity, id string) (homeassistant.Entity, bool) {
	for _, e := range states {
		if e.EntityID == id {
			return e, true
		}
	}
	return homeassistant.Entity{}, false
}
