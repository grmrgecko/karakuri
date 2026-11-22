package influxdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// Query runs InfluxQL on every version, Flux on 2, and SQL on 3.
func (d *Driver) Query(ctx context.Context, req tsdb.Request) (*tsdb.Result, error) {
	var series []tsdb.Series
	var err error
	switch {
	case req.Language == tsdb.LangInfluxQL:
		var resp *influxQLResponse
		resp, err = d.cl.influxQL(ctx, req.Text, true)
		if err == nil {
			series, err = influxQLSeries(resp, req)
		}
	case req.Language == tsdb.LangFlux && d.cfg.Version == V2:
		var body []byte
		body, err = d.cl.flux(ctx, req.Text)
		if err == nil {
			series, err = fluxSeries(body, req)
		}
	case req.Language == tsdb.LangSQL && d.cfg.Version == V3:
		var body []byte
		body, err = d.cl.sql(ctx, req.Text)
		if err == nil {
			series, err = rowSeries(body, req)
		}
	default:
		return nil, tsdb.Unsupportedf("InfluxDB %s does not query %s", d.cfg.Version, req.Language)
	}
	if err != nil {
		return nil, tsdb.Classify(err)
	}
	for i := range series {
		series[i].RefID = req.RefID
	}
	return &tsdb.Result{Series: series, ExecutedQuery: req.Text}, nil
}

// Metadata answers editor lookups with InfluxQL SHOW statements, which every
// version serves, and lists buckets on InfluxDB 2.
func (d *Driver) Metadata(ctx context.Context, req tsdb.MetaRequest) ([]string, error) {
	from := ""
	if req.Measurement != "" {
		from = " FROM " + quoteIdent(req.Measurement)
	}
	var q, column string
	switch req.Kind {
	case tsdb.MetaDatabases:
		if d.cfg.Version == V2 {
			return d.cl.buckets(ctx)
		}
		q, column = "SHOW DATABASES", "name"
	case tsdb.MetaMeasurements:
		q, column = fmt.Sprintf("SHOW MEASUREMENTS LIMIT %d", tsdb.MaxMetaValues), "name"
	case tsdb.MetaTagKeys:
		q, column = "SHOW TAG KEYS"+from, "tagKey"
	case tsdb.MetaTagValues:
		if req.Name == "" {
			return nil, tsdb.Rejectedf("tag_values needs a tag key in name")
		}
		q, column = "SHOW TAG VALUES"+from+" WITH KEY = "+quoteIdent(req.Name), "value"
	case tsdb.MetaFieldKeys:
		q, column = "SHOW FIELD KEYS"+from, "fieldKey"
	default:
		return nil, tsdb.Unsupportedf("InfluxDB has no %s lookup", req.Kind)
	}
	resp, err := d.cl.influxQL(ctx, q, true)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	for _, r := range resp.Results {
		if r.Error != "" {
			return nil, tsdb.Rejectedf("%s", r.Error)
		}
		for _, s := range r.Series {
			col := -1
			for i, c := range s.Columns {
				if c == column {
					col = i
				}
			}
			if col < 0 {
				continue
			}
			for _, row := range s.Values {
				if col < len(row) {
					if v, ok := row[col].(string); ok {
						set[v] = struct{}{}
					}
				}
			}
		}
	}
	return sortedNames(set), nil
}

func sortedNames(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) > tsdb.MaxMetaValues {
		out = out[:tsdb.MaxMetaValues]
	}
	return out
}

// quoteIdent double-quotes an InfluxQL identifier.
func quoteIdent(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// influxQLResponse is the JSON body of the v1 /query endpoint, which
// InfluxDB 2 serves for its DBRP mappings and InfluxDB 3 for compatibility.
type influxQLResponse struct {
	Results []struct {
		Series []influxQLSeriesJSON `json:"series"`
		Error  string               `json:"error"`
	} `json:"results"`
	Error string `json:"error"`
}

type influxQLSeriesJSON struct {
	Name    string            `json:"name"`
	Tags    map[string]string `json:"tags"`
	Columns []string          `json:"columns"`
	Values  [][]any           `json:"values"`
}

// influxQL runs statements through /query with millisecond epochs. It uses
// GET, which InfluxDB 1 limits to read statements. InfluxDB 3's
// /api/v3/query_influxql would drop the tag and column order the v1 shape
// keeps, so all versions share /query.
func (c *client) influxQL(ctx context.Context, text string, withDB bool) (*influxQLResponse, error) {
	q := url.Values{"q": {text}, "epoch": {"ms"}}
	if withDB {
		switch c.cfg.Version {
		case V1:
			q.Set("db", c.cfg.Database)
			if c.cfg.RetentionPolicy != "" {
				q.Set("rp", c.cfg.RetentionPolicy)
			}
		case V2:
			q.Set("db", c.cfg.Bucket)
		case V3:
			q.Set("db", c.cfg.Database)
		}
	}
	body, err := c.get(ctx, "/query", q)
	if err != nil {
		return nil, err
	}
	return decodeInfluxQL(body)
}

func decodeInfluxQL(body []byte) (*influxQLResponse, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var resp influxQLResponse
	if err := dec.Decode(&resp); err != nil {
		return nil, tsdb.Rejectedf("decode InfluxQL response: %v", err)
	}
	if resp.Error != "" {
		return nil, tsdb.Rejectedf("%s", resp.Error)
	}
	return &resp, nil
}

// influxQLSeries turns each value column of each InfluxQL series into one
// time series. A series whose first column is not
// time, such as a SHOW result, or a table-format request, becomes a table
// with the tags appended as columns.
func influxQLSeries(resp *influxQLResponse, req tsdb.Request) ([]tsdb.Series, error) {
	var out []tsdb.Series
	for _, r := range resp.Results {
		if r.Error != "" {
			return nil, tsdb.Rejectedf("%s", r.Error)
		}
		for _, s := range r.Series {
			if len(s.Columns) == 0 {
				continue
			}
			column := func(j int) []any {
				col := make([]any, len(s.Values))
				for i, row := range s.Values {
					if j < len(row) {
						col[i] = row[j]
					}
				}
				return col
			}
			if req.Format == tsdb.FormatTable || s.Columns[0] != "time" {
				t := tsdb.Series{Name: s.Name, Labels: s.Tags}
				for j, name := range s.Columns {
					if j == 0 && name == "time" {
						t.Fields = append(t.Fields, timeField(name, column(j)))
						continue
					}
					t.Fields = append(t.Fields, valueField(name, nil, column(j)))
				}
				for _, k := range sortedKeys(s.Tags) {
					vals := make([]any, len(s.Values))
					for i := range vals {
						vals[i] = s.Tags[k]
					}
					t.Fields = append(t.Fields, tsdb.Field{Name: k, Type: tsdb.FieldString, Values: vals})
				}
				out = append(out, t)
				continue
			}
			times := timeField("time", column(0))
			for j := 1; j < len(s.Columns); j++ {
				name := s.Columns[j]
				legend := map[string]string{"_measurement": s.Name, "_field": name}
				for k, v := range s.Tags {
					legend[k] = v
				}
				out = append(out, tsdb.Series{
					Name:   seriesName(req.LegendFormat, legend, s.Name+"."+name, s.Tags),
					Labels: s.Tags,
					Fields: []tsdb.Field{times, valueField(name, s.Tags, column(j))},
				})
			}
		}
	}
	return out, nil
}

// seriesName applies the legend format, or appends the labels to a default
// base name.
func seriesName(legend string, legendLabels map[string]string, base string, labels map[string]string) string {
	if legend != "" {
		return tsdb.ApplyLegend(legend, legendLabels)
	}
	if len(labels) == 0 {
		return base
	}
	parts := make([]string, 0, len(labels))
	for _, k := range sortedKeys(labels) {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.TrimSpace(base + " {" + strings.Join(parts, ", ") + "}")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// timeField converts epoch milliseconds or timestamp strings to Unix
// milliseconds.
func timeField(name string, vals []any) tsdb.Field {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = toMillis(v)
	}
	return tsdb.Field{Name: name, Type: tsdb.FieldTime, Values: out}
}

func toMillis(v any) any {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		if f, err := t.Float64(); err == nil {
			return int64(f)
		}
	case float64:
		return int64(t)
	case int64:
		return t
	case string:
		if ms, ok := parseTime(t); ok {
			return ms
		}
	}
	return nil
}

// parseTime reads the timestamps InfluxDB 2 Flux (RFC 3339 with a zone) and
// InfluxDB 3 JSON (RFC 3339 without a zone, in UTC) emit.
func parseTime(s string) (int64, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

// valueField types a column by its first non-null value: JSON numbers are
// numbers, booleans booleans, and anything else strings. Values that do not
// fit the column's type become null, or text in a string column.
func valueField(name string, labels map[string]string, vals []any) tsdb.Field {
	typ := tsdb.FieldNumber
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch v.(type) {
		case json.Number, float64:
		case bool:
			typ = tsdb.FieldBoolean
		default:
			typ = tsdb.FieldString
		}
		break
	}
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = convertValue(typ, v)
	}
	return tsdb.Field{Name: name, Type: typ, Labels: labels, Values: out}
}

func convertValue(typ string, v any) any {
	if v == nil {
		return nil
	}
	switch typ {
	case tsdb.FieldNumber:
		switch n := v.(type) {
		case json.Number:
			if f, err := n.Float64(); err == nil {
				return f
			}
		case float64:
			return n
		}
		return nil
	case tsdb.FieldBoolean:
		if b, ok := v.(bool); ok {
			return b
		}
		return nil
	}
	switch s := v.(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	}
	return fmt.Sprint(v)
}

// buckets lists InfluxDB 2 bucket names in pages of 100, the API's maximum.
func (c *client) buckets(ctx context.Context) ([]string, error) {
	set := map[string]struct{}{}
	for offset := 0; offset < tsdb.MaxMetaValues; offset += 100 {
		q := url.Values{"org": {c.cfg.Org}, "limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		body, err := c.get(ctx, "/api/v2/buckets", q)
		if err != nil {
			return nil, err
		}
		var res struct {
			Buckets []struct {
				Name string `json:"name"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, tsdb.Rejectedf("decode buckets: %v", err)
		}
		for _, b := range res.Buckets {
			set[b.Name] = struct{}{}
		}
		if len(res.Buckets) < 100 {
			break
		}
	}
	return sortedNames(set), nil
}

// sql runs an InfluxDB 3 SQL query with JSON rows.
func (c *client) sql(ctx context.Context, text string) ([]byte, error) {
	q := url.Values{"db": {c.cfg.Database}, "q": {text}, "format": {"json"}}
	body, err := c.get(ctx, "/api/v3/query_sql", q)
	return body, v3QueryError(err)
}

// v3QueryError reclassifies the planning failures InfluxDB 3 reports as HTTP
// 500, such as an unknown column, as rejected: the query will fail the same
// way every time.
func v3QueryError(err error) error {
	var e *tsdb.Error
	if !errors.As(err, &e) || e.Class != tsdb.ClassUnavailable {
		return err
	}
	msg := strings.TrimPrefix(e.Message, "HTTP 500: ")
	for _, p := range []string{"Schema error", "Error during planning", "SQL error"} {
		if strings.HasPrefix(msg, p) {
			return &tsdb.Error{Class: tsdb.ClassRejected, Message: e.Message}
		}
	}
	return err
}

// rowSeries converts InfluxDB 3 JSON rows, an array of objects that omit
// null columns, into series. With a time column and a time-series format,
// string columns become labels splitting the rows into series and every
// number or boolean column is a value, which widens long-format SQL
// results. Otherwise the rows are one table.
func rowSeries(body []byte, req tsdb.Request) ([]tsdb.Series, error) {
	cols, rows, err := decodeRows(body)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	column := func(name string, idx []int) []any {
		out := make([]any, len(idx))
		for i, r := range idx {
			out[i] = rows[r][name]
		}
		return out
	}
	all := make([]int, len(rows))
	for i := range all {
		all[i] = i
	}
	types := map[string]string{}
	hasTime := false
	for _, c := range cols {
		if c == "time" {
			hasTime = true
			continue
		}
		types[c] = valueField(c, nil, column(c, all)).Type
	}
	var labelCols, valueCols []string
	for _, c := range cols {
		switch {
		case c == "time":
		case types[c] == tsdb.FieldString:
			labelCols = append(labelCols, c)
		default:
			valueCols = append(valueCols, c)
		}
	}
	if req.Format == tsdb.FormatTable || !hasTime || len(valueCols) == 0 {
		t := tsdb.Series{Name: "result"}
		for _, c := range cols {
			if c == "time" {
				t.Fields = append(t.Fields, timeField(c, column(c, all)))
			} else {
				t.Fields = append(t.Fields, valueField(c, nil, column(c, all)))
			}
		}
		return []tsdb.Series{t}, nil
	}
	type group struct {
		labels map[string]string
		rows   []int
	}
	var order []string
	groups := map[string]*group{}
	for i, row := range rows {
		labels := map[string]string{}
		var key strings.Builder
		for _, c := range labelCols {
			v, _ := row[c].(string)
			if v != "" {
				labels[c] = v
			}
			key.WriteString(v)
			key.WriteByte(0)
		}
		g, ok := groups[key.String()]
		if !ok {
			g = &group{labels: labels}
			groups[key.String()] = g
			order = append(order, key.String())
		}
		g.rows = append(g.rows, i)
	}
	var out []tsdb.Series
	for _, k := range order {
		g := groups[k]
		times := timeField("time", column("time", g.rows))
		for _, c := range valueCols {
			legend := map[string]string{"_field": c}
			for lk, lv := range g.labels {
				legend[lk] = lv
			}
			out = append(out, tsdb.Series{
				Name:   seriesName(req.LegendFormat, legend, c, g.labels),
				Labels: g.labels,
				Fields: []tsdb.Field{times, valueField(c, g.labels, column(c, g.rows))},
			})
		}
	}
	return out, nil
}

// decodeRows reads a JSON array of objects, keeping the column order of
// first appearance since the objects carry the schema's order but omit
// nulls.
func decodeRows(body []byte) ([]string, []map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	bad := func(err error) error { return tsdb.Rejectedf("decode InfluxDB 3 rows: %v", err) }
	if tok, err := dec.Token(); err != nil {
		return nil, nil, bad(err)
	} else if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, nil, bad(fmt.Errorf("expected an array"))
	}
	var cols []string
	seen := map[string]bool{}
	var rows []map[string]any
	for dec.More() {
		if tok, err := dec.Token(); err != nil {
			return nil, nil, bad(err)
		} else if d, ok := tok.(json.Delim); !ok || d != '{' {
			return nil, nil, bad(fmt.Errorf("expected an object"))
		}
		row := map[string]any{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return nil, nil, bad(err)
			}
			key, _ := tok.(string)
			var v any
			if err := dec.Decode(&v); err != nil {
				return nil, nil, bad(err)
			}
			row[key] = v
			if !seen[key] {
				seen[key] = true
				cols = append(cols, key)
			}
		}
		if _, err := dec.Token(); err != nil {
			return nil, nil, bad(err)
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

// flux posts a Flux query asking for annotated CSV.
func (c *client) flux(ctx context.Context, text string) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{
		"query": text,
		"type":  "flux",
		"dialect": map[string]any{
			"annotations": []string{"datatype", "group", "default"},
			"header":      true,
		},
	})
	if err != nil {
		return nil, tsdb.Rejectedf("encode Flux query: %v", err)
	}
	return c.do(ctx, http.MethodPost, "/api/v2/query", url.Values{"org": {c.cfg.Org}}, payload,
		map[string]string{"Content-Type": "application/json", "Accept": "application/csv"})
}
