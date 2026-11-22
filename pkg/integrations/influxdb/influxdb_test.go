package influxdb

import (
	"math"
	"os"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEncodeLines locks the line protocol escaping InfluxDB 1.8, 2, and 3
// Core parse: commas and spaces escaped in measurements; commas, equals
// signs, and spaces in tag keys and values; backslashes and double quotes in
// string fields. All three servers refuse an identifier ending in a
// backslash even when it is escaped, and reject a whole batch over one bad
// line, so such lines are repaired or dropped instead of sent.
func TestEncodeLines(t *testing.T) {
	cases := []struct {
		name  string
		point tsdb.Point
		want  string
	}{
		{
			name:  "number with sorted tags",
			point: tsdb.Point{Time: 1761800000000, Metric: "temp", Labels: map[string]string{"room": "den", "id": "a"}, Value: tsdb.Float(21.5)},
			want:  "temp,id=a,room=den value=21.5 1761800000000\n",
		},
		{
			name:  "escaped measurement and tags",
			point: tsdb.Point{Time: 1, Metric: "living room,main", Labels: map[string]string{"friendly name": "a=b,c d"}, Value: tsdb.Float(1)},
			want:  `living\ room\,main,friendly\ name=a\=b\,c\ d value=1 1` + "\n",
		},
		{
			name:  "text state escapes quotes and backslashes",
			point: tsdb.Point{Time: 2, Metric: "mode", State: tsdb.Text(`say "hi" \o/`)},
			want:  `mode state="say \"hi\" \\o/" 2` + "\n",
		},
		{
			name:  "empty tag values are dropped and trailing backslashes replaced",
			point: tsdb.Point{Time: 3, Metric: `path\`, Labels: map[string]string{"unit": "", "dir": `C:\`}, Value: tsdb.Float(1e21)},
			want:  "path_,dir=C:_ value=1e+21 3\n",
		},
		{
			name:  "a leading hash would make the line a comment",
			point: tsdb.Point{Time: 4, Metric: "#hits", Value: tsdb.Float(0)},
			want:  "_#hits value=0 4\n",
		},
		{
			name:  "non-finite numbers are dropped",
			point: tsdb.Point{Time: 5, Metric: "bad", Value: tsdb.Float(math.Inf(1))},
			want:  "",
		},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, string(encodeLines([]tsdb.Point{tc.point})), "line for %s", tc.name)
	}
}

// TestFluxSeries decodes annotated CSV captured from InfluxDB 2.9 for a
// query with two yields: the first block holds two tables told apart by the
// table column, the second has a string _value and its own result name,
// rows end in CRLF, and blocks are separated by blank lines. Times are RFC
// 3339; 04:53:20Z on 2025-10-30 is Unix 1761800000 seconds.
func TestFluxSeries(t *testing.T) {
	body, err := os.ReadFile("testdata/flux_two_results.csv")
	require.NoError(t, err)

	series, err := fluxSeries(body, tsdb.Request{})
	require.NoError(t, err)
	require.Len(t, series, 3, "each table becomes one series")

	want := []struct {
		name   string
		host   string
		typ    string
		time   int64
		value  any
		labels int
	}{
		{"probe_m value {host=a b}", "a b", tsdb.FieldNumber, 1761800000000, 1.5, 3},
		{"probe_m value {host=c}", "c", tsdb.FieldNumber, 1761800002000, 2.0, 3},
		{"probe_m state {host=c}", "c", tsdb.FieldString, 1761800001000, "on", 3},
	}
	for i, w := range want {
		s := series[i]
		assert.Equal(t, w.name, s.Name, "series %d name", i)
		assert.Equal(t, w.host, s.Labels["host"], "series %d keeps the group key as labels", i)
		assert.Len(t, s.Labels, w.labels, "series %d labels hold _field, _measurement, and host but not _start or _stop", i)
		require.Len(t, s.Fields, 2, "series %d is time plus _value", i)
		assert.Equal(t, tsdb.FieldTime, s.Fields[0].Type)
		assert.Equal(t, []any{w.time}, s.Fields[0].Values, "series %d time in Unix ms", i)
		assert.Equal(t, w.typ, s.Fields[1].Type, "series %d value type follows #datatype", i)
		assert.Equal(t, []any{w.value}, s.Fields[1].Values, "series %d value", i)
	}

	legend, err := fluxSeries(body, tsdb.Request{Query: tsdb.Query{LegendFormat: "{{host}}/{{_field}}"}})
	require.NoError(t, err)
	assert.Equal(t, "a b/value", legend[0].Name, "legend formats read the group key")
}

// TestFluxErrorTable pins the shape InfluxDB 2 uses for an error raised
// after the response began: a table with error and reference columns.
func TestFluxErrorTable(t *testing.T) {
	body := "#datatype,string,string\r\n#group,true,true\r\n#default,,\r\n,error,reference\r\n,\"runtime error: boom\",\r\n\r\n"
	_, err := fluxSeries([]byte(body), tsdb.Request{})
	var e *tsdb.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, tsdb.ClassRejected, e.Class)
	assert.Contains(t, e.Message, "runtime error: boom")
}

// TestInfluxQLSeries decodes /query responses shaped as InfluxDB 1.8 sends
// them with epoch=ms: a GROUP BY tag query yields one series per tag set and
// one value column, a SELECT without time grouping carries several columns,
// and SHOW results have no time column, so they become tables.
func TestInfluxQLSeries(t *testing.T) {
	body := `{"results":[{"statement_id":0,"series":[` +
		`{"name":"probe_m","tags":{"host":"a b"},"columns":["time","mean"],"values":[[1761796800000,1.5],[1761800400000,null]]},` +
		`{"name":"probe_m","tags":{"host":"c"},"columns":["time","mean"],"values":[[1761796800000,2]]}]},` +
		`{"statement_id":1,"series":[{"name":"probe_m","columns":["time","host","state","value"],"values":[[1761800000000,"a b",null,1.5],[1761800001000,"c","on",null]]}]},` +
		`{"statement_id":2,"series":[{"name":"probe_m","columns":["key","value"],"values":[["host","a b"],["host","c"]]}]}]}`
	resp, err := decodeInfluxQL([]byte(body))
	require.NoError(t, err)
	series, err := influxQLSeries(resp, tsdb.Request{})
	require.NoError(t, err)

	names := make([]string, len(series))
	for i, s := range series {
		names[i] = s.Name
	}
	assert.Equal(t, []string{
		"probe_m.mean {host=a b}", "probe_m.mean {host=c}",
		"probe_m.host", "probe_m.state", "probe_m.value",
		"probe_m",
	}, names, "one series per value column, then the SHOW table")

	assert.Equal(t, []any{int64(1761796800000), int64(1761800400000)}, series[0].Fields[0].Values, "epoch=ms times stay integers")
	assert.Equal(t, []any{1.5, nil}, series[0].Fields[1].Values, "null is a gap")
	assert.Equal(t, []any{2.0}, series[1].Fields[1].Values, "integral JSON numbers are floats")
	assert.Equal(t, tsdb.FieldString, series[3].Fields[1].Type, "a string field column is typed string")
	assert.Equal(t, []any{nil, "on"}, series[3].Fields[1].Values)

	table := series[5]
	require.Len(t, table.Fields, 2)
	assert.Equal(t, tsdb.FieldString, table.Fields[0].Type, "a table without time keeps its first column as data")
	assert.Equal(t, []any{"a b", "c"}, table.Fields[1].Values)

	resp, err = decodeInfluxQL([]byte(`{"results":[{"statement_id":0,"error":"database not found: x"}]}`))
	require.NoError(t, err, "a statement error arrives inside a 200 response")
	_, err = influxQLSeries(resp, tsdb.Request{})
	var e *tsdb.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, tsdb.ClassRejected, e.Class)
}

// TestRowSeries converts InfluxDB 3 Core format=json SQL rows, which omit
// null columns and write times in RFC 3339 without a zone, meaning UTC.
// String columns split the rows into series the way tags would.
func TestRowSeries(t *testing.T) {
	body := `[{"host":"a b","time":"2025-10-30T04:53:20","value":1.5},{"host":"c","time":"2025-10-30T04:53:21.5"},{"host":"c","time":"2025-10-30T04:53:22","value":2.0}]`
	series, err := rowSeries([]byte(body), tsdb.Request{})
	require.NoError(t, err)
	require.Len(t, series, 2)
	assert.Equal(t, "value {host=a b}", series[0].Name)
	assert.Equal(t, []any{int64(1761800000000)}, series[0].Fields[0].Values)
	assert.Equal(t, []any{int64(1761800001500), int64(1761800002000)}, series[1].Fields[0].Values, "fractional seconds survive")
	assert.Equal(t, []any{nil, 2.0}, series[1].Fields[1].Values, "an omitted column is a gap")

	table, err := rowSeries([]byte(body), tsdb.Request{Query: tsdb.Query{Format: tsdb.FormatTable}})
	require.NoError(t, err)
	require.Len(t, table, 1)
	var cols []string
	for _, f := range table[0].Fields {
		cols = append(cols, f.Name)
	}
	assert.Equal(t, []string{"host", "time", "value"}, cols, "table columns keep the server's order")
}
