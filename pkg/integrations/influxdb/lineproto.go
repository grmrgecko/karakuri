package influxdb

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

var (
	measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `, "\n", `\ `, "\r", `\ `)
	tagEscaper         = strings.NewReplacer(",", `\,`, "=", `\=`, " ", `\ `, "\n", `\ `, "\r", `\ `)
	stringEscaper      = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
)

// identifier escapes a measurement, tag key, or tag value. InfluxDB 1, 2,
// and 3 all refuse one ending in a backslash, even an escaped one, so
// trailing backslashes become underscores.
func identifier(r *strings.Replacer, s string) string {
	if n := len(s) - len(strings.TrimRight(s, `\`)); n > 0 {
		s = s[:len(s)-n] + strings.Repeat("_", n)
	}
	return r.Replace(s)
}

// encodeLines renders points as line protocol with millisecond timestamps,
// one line per point: the measurement is the metric, labels are tags in
// key order, and the field is value for a number or state for text. Points
// with neither, an empty metric, or a non-finite value are dropped, since
// InfluxDB rejects the whole batch for one bad line.
func encodeLines(points []tsdb.Point) []byte {
	var b strings.Builder
	keys := make([]string, 0, 8)
	for _, p := range points {
		if p.Metric == "" {
			continue
		}
		var field string
		switch {
		case p.Value != nil:
			if math.IsNaN(*p.Value) || math.IsInf(*p.Value, 0) {
				continue
			}
			field = "value=" + strconv.FormatFloat(*p.Value, 'g', -1, 64)
		case p.State != nil:
			field = `state="` + stringEscaper.Replace(*p.State) + `"`
		default:
			continue
		}
		m := identifier(measurementEscaper, p.Metric)
		// A line starting with # is a comment.
		if m[0] == '#' {
			m = "_" + m
		}
		b.WriteString(m)
		keys = keys[:0]
		for k, v := range p.Labels {
			if k != "" && v != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteByte(',')
			b.WriteString(identifier(tagEscaper, k))
			b.WriteByte('=')
			b.WriteString(identifier(tagEscaper, p.Labels[k]))
		}
		b.WriteByte(' ')
		b.WriteString(field)
		if p.Time != 0 {
			b.WriteByte(' ')
			b.WriteString(strconv.FormatInt(p.Time, 10))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
