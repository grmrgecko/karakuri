package system

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/stretchr/testify/assert"
)

// TestCPUBusyPercent pins the two judgment calls in the only arithmetic these
// metrics nodes do. Neither is obvious from the numbers: iowait counts as idle
// because a host waiting on a disk is not working, and guest time is excluded
// because Linux already counts it inside user, so a hypervisor busy running
// VMs would otherwise total more than the interval and report over 100 percent.
func TestCPUBusyPercent(t *testing.T) {
	tests := []struct {
		name   string
		before cpu.TimesStat
		after  cpu.TimesStat
		want   float64
		why    string
	}{
		{
			name:   "half the interval was user time",
			before: cpu.TimesStat{User: 100, Idle: 100},
			after:  cpu.TimesStat{User: 110, Idle: 110},
			want:   50,
			why:    "10 user and 10 idle over a 20 second interval is 50 percent",
		},
		{
			name:   "iowait is not work",
			before: cpu.TimesStat{User: 100, Idle: 100, Iowait: 10},
			after:  cpu.TimesStat{User: 100, Idle: 100, Iowait: 30},
			want:   0,
			why:    "20 seconds of waiting on a disk over a 20 second interval is an idle host",
		},
		{
			name:   "guest time is already inside user",
			before: cpu.TimesStat{User: 100, Idle: 100, Guest: 50},
			after:  cpu.TimesStat{User: 120, Idle: 100, Guest: 70},
			want:   100,
			why:    "20 user seconds over a 20 second interval is fully busy, not 50 percent of 40",
		},
		{
			name:   "counters that did not move",
			before: cpu.TimesStat{User: 100, Idle: 100},
			after:  cpu.TimesStat{User: 100, Idle: 100},
			want:   0,
			why:    "a zero interval divides by nothing and must report zero rather than NaN",
		},
	}
	for _, tc := range tests {
		assert.InDeltaf(t, tc.want, cpuBusyPercent(tc.before, tc.after), 0.01, "%s: %s", tc.name, tc.why)
	}
}
