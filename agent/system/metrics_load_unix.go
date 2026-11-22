//go:build !windows

package system

import (
	"context"

	"github.com/shirou/gopsutil/v4/load"
)

// loadAverage returns the kernel's 1, 5, and 15 minute load averages. Every
// Unix keeps them itself, so reading them costs one file or one sysctl.
func loadAverage(ctx context.Context) ([3]float64, bool) {
	avg, err := load.AvgWithContext(ctx)
	if err != nil || avg == nil {
		return [3]float64{}, false
	}
	return [3]float64{avg.Load1, avg.Load5, avg.Load15}, true
}
