package influxdb

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// fluxTable is one table of an annotated CSV response.
type fluxTable struct {
	result  string
	columns []string
	types   []string
	group   []bool
	rows    [][]any
}

// decodeFlux parses InfluxDB 2's annotated CSV. Each schema starts a block
// of #datatype, #group, and #default rows and a header; a block may hold
// several tables told apart by the table column. The first column of every
// row is the annotation column, empty on header and data rows.
func decodeFlux(body []byte) ([]*fluxTable, error) {
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	var (
		types, group, defaults []string
		header                 []string
		block                  int
		tables                 []*fluxTable
		byKey                  = map[string]*fluxTable{}
	)
	col := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		return -1
	}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, tsdb.Rejectedf("decode Flux CSV: %v", err)
		}
		if len(rec) == 0 {
			continue
		}
		if strings.HasPrefix(rec[0], "#") {
			if header != nil {
				header, types, group, defaults = nil, nil, nil, nil
				block++
			}
			switch rec[0] {
			case "#datatype":
				types = rec
			case "#group":
				group = rec
			case "#default":
				defaults = rec
			}
			continue
		}
		if header == nil {
			header = rec
			continue
		}
		// A failed query after the response started is reported as a
		// table with error and reference columns.
		if e := col("error"); e >= 0 && col("_time") < 0 && e < len(rec) {
			return nil, tsdb.Rejectedf("Flux: %s", rec[e])
		}
		get := func(i int, from []string) string {
			if i < len(from) {
				return from[i]
			}
			return ""
		}
		resultIdx, tableIdx := col("result"), col("table")
		result := get(resultIdx, rec)
		if result == "" {
			result = get(resultIdx, defaults)
		}
		key := strconv.Itoa(block) + "\x00" + result + "\x00" + get(tableIdx, rec)
		t, ok := byKey[key]
		if !ok {
			t = &fluxTable{result: result}
			for i, h := range header {
				if i == 0 || i == resultIdx || i == tableIdx {
					continue
				}
				t.columns = append(t.columns, h)
				t.types = append(t.types, get(i, types))
				t.group = append(t.group, get(i, group) == "true")
			}
			byKey[key] = t
			tables = append(tables, t)
		}
		row := make([]any, 0, len(t.columns))
		for i := range header {
			if i == 0 || i == resultIdx || i == tableIdx {
				continue
			}
			v := get(i, rec)
			if v == "" {
				v = get(i, defaults)
			}
			row = append(row, fluxValue(get(i, types), v))
		}
		t.rows = append(t.rows, row)
	}
	return tables, nil
}

// fluxValue converts one cell by its #datatype annotation.
func fluxValue(typ, v string) any {
	if v == "" {
		return nil
	}
	switch {
	case typ == "double" || typ == "long" || typ == "unsignedLong":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	case typ == "boolean":
		return v == "true"
	case strings.HasPrefix(typ, "dateTime"):
		if ms, ok := parseTime(v); ok {
			return ms
		}
		return nil
	}
	return v
}

func fluxFieldType(typ string) string {
	switch {
	case typ == "double" || typ == "long" || typ == "unsignedLong":
		return tsdb.FieldNumber
	case typ == "boolean":
		return tsdb.FieldBoolean
	case strings.HasPrefix(typ, "dateTime"):
		return tsdb.FieldTime
	}
	return tsdb.FieldString
}

// fluxSeries turns each value column of each Flux table into a series. The
// table's group key, less the _start and _stop range bounds, becomes its
// labels; tables without _time, or a table-format request, stay tables.
func fluxSeries(body []byte, req tsdb.Request) ([]tsdb.Series, error) {
	tables, err := decodeFlux(body)
	if err != nil {
		return nil, err
	}
	var out []tsdb.Series
	for _, t := range tables {
		column := func(j int) []any {
			vals := make([]any, len(t.rows))
			for i, row := range t.rows {
				vals[i] = row[j]
			}
			return vals
		}
		timeIdx := -1
		for j, c := range t.columns {
			if c == "_time" {
				timeIdx = j
			}
		}
		labels := map[string]string{}
		for j, c := range t.columns {
			if !t.group[j] || c == "_start" || c == "_stop" || len(t.rows) == 0 {
				continue
			}
			if s, ok := t.rows[0][j].(string); ok && s != "" {
				labels[c] = s
			}
		}
		if req.Format == tsdb.FormatTable || timeIdx < 0 {
			s := tsdb.Series{Name: t.result, Labels: labels}
			for j, c := range t.columns {
				s.Fields = append(s.Fields, tsdb.Field{Name: c, Type: fluxFieldType(t.types[j]), Values: column(j)})
			}
			out = append(out, s)
			continue
		}
		times := tsdb.Field{Name: "time", Type: tsdb.FieldTime, Values: column(timeIdx)}
		nameLabels := map[string]string{}
		for k, v := range labels {
			if k != "_measurement" && k != "_field" {
				nameLabels[k] = v
			}
		}
		for j, c := range t.columns {
			if t.group[j] || j == timeIdx || c == "_start" || c == "_stop" {
				continue
			}
			field := labels["_field"]
			if c != "_value" || field == "" {
				field = c
			}
			base := strings.TrimSpace(labels["_measurement"] + " " + field)
			out = append(out, tsdb.Series{
				Name:   seriesName(req.LegendFormat, labels, base, nameLabels),
				Labels: labels,
				Fields: []tsdb.Field{times, {Name: c, Type: fluxFieldType(t.types[j]), Labels: labels, Values: column(j)}},
			})
		}
	}
	return out, nil
}
