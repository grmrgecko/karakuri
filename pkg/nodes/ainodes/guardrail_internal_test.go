package ainodes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDetectPIISparesTimestamps locks that an ISO date followed by a time is
// not reported as a phone number. The phone pattern admits whitespace, so
// "2026-11-21 14" is one candidate with ten digits, inside the 9 to 15 the
// digit count accepts. Log lines and sensor payloads carry such timestamps,
// and redacting them cut the hour off every one. The numbers in the last rows
// must still be found, including one that only starts like a date.
func TestDetectPIISparesTimestamps(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{name: "a log line timestamp", text: "Motion in the hall at 2026-11-21 14:14:22"},
		{name: "a date and a 24 hour time", text: "Bins go out on 2026-11-24 07:30"},
		{name: "an international number", text: "Call +1 (555) 123-4567 today", want: []string{"+1 (555) 123-4567"}},
		{name: "a number opening with four digits", text: "Ring 2026-112-1140 now", want: []string{"2026-112-1140"}},
	}
	for _, tc := range cases {
		var got []string
		for _, d := range detectPII(tc.text, map[string]bool{"phone": true}) {
			got = append(got, tc.text[d.start:d.end])
		}
		assert.Equalf(t, tc.want, got, "phone matches for %s", tc.name)
	}
}
