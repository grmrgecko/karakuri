package postgresql

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// column is a result column with the field type its values convert to.
type column struct {
	name string
	kind string
}

// Query runs a SQL query in a read-only transaction that is always rolled
// back, so a query cannot change data even when the role could.
func (d *Driver) Query(ctx context.Context, req tsdb.Request) (*tsdb.Result, error) {
	if req.Language != "" && req.Language != tsdb.LangSQL {
		return nil, tsdb.Unsupportedf("postgres connections query sql, not %s", req.Language)
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, tsdb.Rejectedf("query text is empty")
	}
	cols, rows, truncated, err := d.run(ctx, req.Text)
	if err != nil {
		e := classify(err)
		d.observe(ctx, e)
		return nil, e
	}
	d.observe(ctx, nil)
	return &tsdb.Result{Series: convert(cols, rows, req), Truncated: truncated}, nil
}

// run executes text read-only and returns its rows with normalized values,
// at most tsdb.MaxValues of them.
func (d *Driver) run(ctx context.Context, text string) ([]column, [][]any, bool, error) {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// SET cannot take a bind parameter; the value is an integer.
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", d.QueryTimeout().Milliseconds())); err != nil {
		return nil, nil, false, err
	}
	rs, err := tx.Query(ctx, text)
	if err != nil {
		return nil, nil, false, err
	}
	defer rs.Close()
	fds := rs.FieldDescriptions()
	cols := make([]column, len(fds))
	for i, fd := range fds {
		cols[i] = column{name: fd.Name, kind: columnKind(fd.DataTypeOID)}
	}
	var out [][]any
	truncated := false
	for rs.Next() {
		if len(out) >= tsdb.MaxValues {
			truncated = true
			break
		}
		vals, err := rs.Values()
		if err != nil {
			return nil, nil, false, err
		}
		row := make([]any, len(cols))
		for i := range cols {
			row[i] = normalize(cols[i].kind, vals[i])
		}
		out = append(out, row)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, nil, false, err
	}
	return cols, out, truncated, nil
}

// columnKind maps a column type to the field type its values become. Types
// without a natural number or time reading are text.
func columnKind(oid uint32) string {
	switch oid {
	case pgtype.TimestamptzOID, pgtype.TimestampOID, pgtype.DateOID:
		return tsdb.FieldTime
	case pgtype.Int2OID, pgtype.Int4OID, pgtype.Int8OID, pgtype.Float4OID, pgtype.Float8OID, pgtype.NumericOID, pgtype.OIDOID:
		return tsdb.FieldNumber
	case pgtype.BoolOID:
		return tsdb.FieldBoolean
	}
	return tsdb.FieldString
}

// normalize converts a pgx value to its field form: Unix milliseconds,
// float64, bool, or string. NaN and infinities become gaps, since results
// are JSON.
func normalize(kind string, v any) any {
	if v == nil {
		return nil
	}
	switch kind {
	case tsdb.FieldTime:
		if t, ok := v.(time.Time); ok {
			return t.UnixMilli()
		}
		return nil
	case tsdb.FieldNumber:
		f, ok := toFloat(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	case tsdb.FieldBoolean:
		if b, ok := v.(bool); ok {
			return b
		}
		return nil
	}
	return text(v)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case pgtype.Numeric:
		f, err := n.Float64Value()
		if err != nil || !f.Valid {
			return 0, false
		}
		return f.Float64, true
	}
	return 0, false
}

func text(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case time.Time:
		return s.Format(time.RFC3339Nano)
	case [16]byte:
		return uuid.UUID(s).String()
	case map[string]any, []any:
		b, err := json.Marshal(s)
		if err == nil {
			return string(b)
		}
	case fmt.Stringer:
		return s.String()
	}
	return fmt.Sprint(v)
}

// convert shapes rows by their columns. With a time
// column and at least one numeric column, rows become time series: a text
// column named metric names the series, other text and boolean columns are
// labels, and each numeric column is a separate series per metric and label
// set. Anything else, or the table format, returns one table.
func convert(cols []column, rows [][]any, req tsdb.Request) []tsdb.Series {
	timeIdx, metricIdx := -1, -1
	for i, c := range cols {
		if c.kind == tsdb.FieldTime && strings.EqualFold(c.name, "time") {
			timeIdx = i
			break
		}
	}
	if timeIdx < 0 {
		for i, c := range cols {
			if c.kind == tsdb.FieldTime {
				timeIdx = i
				break
			}
		}
	}
	var valueIdx, labelIdx []int
	for i, c := range cols {
		switch {
		case i == timeIdx || c.kind == tsdb.FieldTime:
		case c.kind == tsdb.FieldNumber:
			valueIdx = append(valueIdx, i)
		case c.kind == tsdb.FieldString && strings.EqualFold(c.name, "metric") && metricIdx < 0:
			metricIdx = i
		default:
			labelIdx = append(labelIdx, i)
		}
	}
	if req.Format == tsdb.FormatTable || timeIdx < 0 || len(valueIdx) == 0 {
		return []tsdb.Series{table(cols, rows, req.RefID)}
	}

	type acc struct {
		name   string
		labels map[string]string
		times  []int64
		values []any
	}
	var order []*acc
	byKey := map[string]*acc{}
	for _, row := range rows {
		t, ok := row[timeIdx].(int64)
		if !ok {
			continue
		}
		metric := ""
		if metricIdx >= 0 && row[metricIdx] != nil {
			metric = text(row[metricIdx])
		}
		var labels map[string]string
		var key strings.Builder
		key.WriteString(metric)
		for _, li := range labelIdx {
			if row[li] == nil {
				continue
			}
			if labels == nil {
				labels = map[string]string{}
			}
			v := text(row[li])
			labels[cols[li].name] = v
			fmt.Fprintf(&key, "\x00%s=%s", cols[li].name, v)
		}
		for _, vi := range valueIdx {
			k := key.String() + "\x01" + cols[vi].name
			a := byKey[k]
			if a == nil {
				a = &acc{name: seriesName(req.LegendFormat, metric, cols[vi].name, len(valueIdx) > 1, labels), labels: labels}
				byKey[k] = a
				order = append(order, a)
			}
			a.times = append(a.times, t)
			a.values = append(a.values, row[vi])
		}
	}
	out := make([]tsdb.Series, 0, len(order))
	for _, a := range order {
		idx := make([]int, len(a.times))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(i, j int) bool { return a.times[idx[i]] < a.times[idx[j]] })
		times := make([]int64, len(idx))
		values := make([]any, len(idx))
		for i, j := range idx {
			times[i], values[i] = a.times[j], a.values[j]
		}
		s := tsdb.TimeSeries(a.name, a.labels, times, values)
		s.RefID = req.RefID
		out = append(out, s)
	}
	return out
}

// seriesName names a series from the legend format, or else from the metric
// column, the value column, and the labels.
func seriesName(legend, metric, field string, manyFields bool, labels map[string]string) string {
	if legend != "" {
		vars := map[string]string{"metric": metric, "field": field}
		for k, v := range labels {
			vars[k] = v
		}
		return tsdb.ApplyLegend(legend, vars)
	}
	name := field
	if metric != "" {
		name = metric
		if manyFields {
			name += " " + field
		}
	}
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%q", k, labels[k])
	}
	return name + "{" + strings.Join(parts, ", ") + "}"
}

func table(cols []column, rows [][]any, refID string) tsdb.Series {
	fields := make([]tsdb.Field, len(cols))
	for i, c := range cols {
		values := make([]any, len(rows))
		for r, row := range rows {
			values[r] = row[i]
		}
		fields[i] = tsdb.Field{Name: c.name, Type: c.kind, Values: values}
	}
	name := refID
	if name == "" {
		name = "table"
	}
	return tsdb.Series{Name: name, RefID: refID, Fields: fields}
}

// Metadata lists schemas, tables of a schema, or columns of a table from
// information_schema.
func (d *Driver) Metadata(ctx context.Context, req tsdb.MetaRequest) ([]string, error) {
	var (
		sql  string
		args []any
	)
	schema := req.Schema
	if schema == "" {
		schema = "public"
	}
	switch req.Kind {
	case tsdb.MetaSchemas:
		sql = `SELECT schema_name FROM information_schema.schemata
			WHERE schema_name NOT IN ('pg_catalog', 'information_schema')
			AND schema_name NOT LIKE 'pg\_%' AND schema_name NOT LIKE '\_timescaledb%'
			ORDER BY 1 LIMIT $1`
		args = []any{tsdb.MaxMetaValues}
	case tsdb.MetaTables:
		sql = `SELECT table_name FROM information_schema.tables WHERE table_schema = $1 ORDER BY 1 LIMIT $2`
		args = []any{schema, tsdb.MaxMetaValues}
	case tsdb.MetaColumns:
		if req.Table == "" {
			return nil, tsdb.Rejectedf("table is required for column lookups")
		}
		table := req.Table
		if s, t, ok := strings.Cut(req.Table, "."); ok {
			schema, table = s, t
		}
		sql = `SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY 1 LIMIT $3`
		args = []any{schema, table, tsdb.MaxMetaValues}
	default:
		return nil, tsdb.Unsupportedf("postgres connections do not list %s", req.Kind)
	}
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		e := classify(err)
		d.observe(ctx, e)
		return nil, e
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e := classify(err)
		d.observe(ctx, e)
		return nil, e
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}
