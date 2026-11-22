//go:build !windows && !darwin

package linuxdesk

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestLogindIdleSeconds pins that IdleSinceHint counts only while IdleHint is
// set. logind stamps IdleSinceHint on every change of the idle hint, in either
// direction, so a user who came back ten minutes ago carries a ten minute old
// stamp while IdleHint is false and is idle for 0 seconds.
func TestLogindIdleSeconds(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tenMinutesAgo := uint64(now.Add(-10 * time.Minute).UnixMicro())
	cases := []struct {
		name   string
		idle   bool
		micros uint64
		want   float64
	}{
		{"active session whose hint cleared ten minutes ago", false, tenMinutesAgo, 0},
		{"session idle for ten minutes", true, tenMinutesAgo, 600},
		{"idle session with no stamp", true, 0, 0},
		{"stamp ahead of the clock", true, uint64(now.Add(time.Minute).UnixMicro()), 0},
	}
	for _, tc := range cases {
		assert.InDelta(t, tc.want, logindIdleSeconds(tc.idle, tc.micros, now), 0.001, tc.name)
	}
}
