package dashvars

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubstituteQuery pins spec 025 FR-022: a plain reference cannot break
// out of the string literal the author quoted it in, while the explicit
// formats give the author the raw value, a regex-safe value, a quoted SQL
// string, or a time range's ends. A regex value is escaped twice for PromQL,
// whose strings take Go escapes (a regex \. is written "\\."), and has its
// slashes escaped for InfluxQL's /.../ literals.
func TestSubstituteQuery(t *testing.T) {
	values := map[string]models.VariableOption{
		"host":  {Value: `a"} or vector(1) #`, Label: "Host A"},
		"room":  {Value: "o'neil.room"},
		"range": {Value: "now-24h,now"},
		"tpl":   {Value: "${now()}"},
	}
	cases := []struct {
		lang, text, want string
	}{
		{tsdb.LangPromQL, `up{host="${host}"}`, `up{host="a\"} or vector(1) #"}`},
		{tsdb.LangPromQL, `up{host=~"${room:regex}"}`, `up{host=~"o'neil\\.room"}`},
		{tsdb.LangPromQL, `up{host=~"${host:regex}"}`, `up{host=~"a\"\\} or vector\\(1\\) #"}`},
		{tsdb.LangInfluxQL, `WHERE "room" =~ /${room:regex}/`, `WHERE "room" =~ /o'neil\.room/`},
		{tsdb.LangFlux, `r.note == "${tpl}"`, `r.note == "\${now()}"`},
		{tsdb.LangSQL, `WHERE room = '${room}'`, `WHERE room = 'o''neil.room'`},
		{tsdb.LangSQL, `WHERE room = ${room:sqlstring}`, `WHERE room = 'o''neil.room'`},
		{tsdb.LangInfluxQL, `WHERE "room" = '${room}'`, `WHERE "room" = 'o\'neil.room'`},
		{tsdb.LangFlux, `r.room == "${room:raw}"`, `r.room == "o'neil.room"`},
		{tsdb.LangSQL, `${range:from} ${missing}`, `now-24h ${missing}`},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, SubstituteQuery(tc.text, tc.lang, values), "%s: %s", tc.lang, tc.text)
	}
}

// TestOptionCacheKeySeparatesRegexAndSort locks that two query variables
// sharing a query but not its post-processing do not share cached options.
// The cache holds options after the regex and sort are applied, so without
// them in the key a variable with no regex was handed the port-stripped
// hosts of its neighbor for the 30 seconds the entry lived.
func TestOptionCacheKeySeparatesRegexAndSort(t *testing.T) {
	base := models.VariableQuery{ConnectionID: "c1", Source: models.QueryPrometheusLabelValues, Label: "instance"}
	stripped := base
	stripped.Regex = `^(.*):\d+$`
	sorted := base
	sorted.Sort = "desc"

	key := func(q models.VariableQuery) string {
		return optionCacheKey(&q, tsdb.LangPromQL, "", "", "now-1h", "now")
	}
	assert.Equal(t, key(base), key(base), "the same variable keeps hitting its own entry")
	assert.NotEqual(t, key(base), key(stripped), "a regex changes the options, so it changes the key")
	assert.NotEqual(t, key(base), key(sorted), "a sort order changes the options, so it changes the key")
}

// TestTableOptionsWriteNumbersInFull locks how a table cell becomes an option
// value. PostgreSQL integer columns and InfluxDB numbers arrive as float64,
// and the default float formatting switches to an exponent at a million, so
// a device ID of 1234567 became the option "1.234567e+06" and every query
// substituting it compared against the wrong text.
func TestTableOptionsWriteNumbersInFull(t *testing.T) {
	res := &tsdb.Result{Series: []tsdb.Series{{Fields: []tsdb.Field{
		{Name: "id", Type: tsdb.FieldNumber, Values: []any{1234567.0, 0.5, nil}},
		{Name: "floor", Type: tsdb.FieldNumber, Values: []any{2000000.0, nil, 3.0}},
	}}}}
	assert.Equal(t, []models.VariableOption{{Value: "1234567", Label: "2000000"}, {Value: "0.5"}},
		tableOptions(res, true), "numbers must be written as decimals, and a row without a value is left out")
}

// TestScopeUnionResolvesAutoInterval locks that the scope union runs a
// chained query with the duration an interval's auto option stands for, as a
// viewer's resolution does. The union passed the raw option, so the query read
// rate(x[auto]) and scope granted different options than a viewer was offered.
func TestScopeUnionResolvesAutoInterval(t *testing.T) {
	var steps []string
	prev := TimeSeriesOptions
	TimeSeriesOptions = func(_ context.Context, _ *models.VariableQuery, values map[string]models.VariableOption, _ string) Result {
		steps = append(steps, values["step"].Value)
		return Result{Options: []models.VariableOption{{Value: "handler-at-" + values["step"].Value}}}
	}
	t.Cleanup(func() { TimeSeriesOptions = prev })

	vars := []models.DashboardVariable{
		{Name: "range", Type: models.VariableTypeTimeRange, Default: "now-6h,now"},
		{Name: "step", Type: models.VariableTypeInterval, Auto: true, RangeVariable: "range",
			Options: []models.VariableOption{{Value: "5m"}}},
		{Name: "handler", Type: models.VariableTypeQuery, Query: &models.VariableQuery{
			Source: models.QueryPrometheusQuery, ConnectionID: "c1", Label: "handler", RangeVariable: "range",
			Text: `topk(5, sum by (handler) (rate(http_requests_total[${step}])))`,
		}},
	}

	ResolveFor(vars, nil)
	require.Len(t, steps, 1, "a viewer's resolution runs the query once, with the default step")
	auto := steps[0]
	require.NotEqual(t, models.AutoInterval, auto, "a viewer's query gets the duration auto resolves to")
	steps = nil

	Resolve(vars)
	assert.Equal(t, []string{auto, "5m"}, steps,
		"the union runs the query once per step option, with auto as the same duration a viewer gets and a fixed step as written")
}
