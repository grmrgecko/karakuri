// Package idleparse converts the idle readings the platform backends take —
// IOKit nanoseconds on macOS, milliseconds everywhere else — into the seconds
// the idle-time node reports.
package idleparse

// FromNanos converts a nanosecond idle reading to seconds, rounded to a tenth
// so a comparison in a flow is not defeated by jitter.
func FromNanos(nanos int64) float64 {
	return float64(nanos/1e8) / 10
}

// FromMillis converts a millisecond idle reading to seconds.
func FromMillis(millis int64) float64 {
	return float64(millis/100) / 10
}
