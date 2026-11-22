package tsdb

import (
	"fmt"
	"math"
)

// Reductions a query node can apply.
var Reductions = []string{"last", "first", "min", "max", "mean", "sum", "count", "delta"}

// MaxValues bounds the values one result may hold across every series.
const MaxValues = 100000

// Limit cuts a result to MaxValues values in total and marks it truncated.
func Limit(res *Result) {
	budget := MaxValues
	for i := range res.Series {
		s := &res.Series[i]
		rows := s.Len()
		cells := rows * len(s.Fields)
		if cells <= budget {
			budget -= cells
			continue
		}
		res.Truncated = true
		keep := 0
		if len(s.Fields) > 0 {
			keep = budget / len(s.Fields)
		}
		for j := range s.Fields {
			if len(s.Fields[j].Values) > keep {
				s.Fields[j].Values = s.Fields[j].Values[:keep]
			}
		}
		res.Series = res.Series[:i+1]
		if keep == 0 {
			res.Series = res.Series[:i]
		}
		return
	}
}

// Reduce reduces the first numeric field of the first series. Gaps are
// skipped; a series with no numbers has no value. Delta is the last value
// minus the first.
func Reduce(res *Result, how string) (*float64, error) {
	var values []float64
	for _, s := range res.Series {
		for _, f := range s.Fields {
			if f.Type != FieldNumber {
				continue
			}
			for _, v := range f.Values {
				if n, ok := v.(float64); ok && !math.IsNaN(n) {
					values = append(values, n)
				}
			}
			break
		}
		if values != nil {
			break
		}
	}
	if how == "count" {
		n := float64(len(values))
		return &n, nil
	}
	if len(values) == 0 {
		return nil, nil
	}
	var out float64
	switch how {
	case "last":
		out = values[len(values)-1]
	case "first":
		out = values[0]
	case "min":
		out = values[0]
		for _, v := range values {
			out = math.Min(out, v)
		}
	case "max":
		out = values[0]
		for _, v := range values {
			out = math.Max(out, v)
		}
	case "mean", "sum":
		for _, v := range values {
			out += v
		}
		if how == "mean" {
			out /= float64(len(values))
		}
	case "delta":
		out = values[len(values)-1] - values[0]
	default:
		return nil, fmt.Errorf("unknown reduction %q", how)
	}
	return &out, nil
}
