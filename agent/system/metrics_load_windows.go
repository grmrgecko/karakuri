//go:build windows

package system

import "context"

// loadAverage reports nothing on Windows. The kernel keeps no load average,
// and the usual way to produce one is a goroutine sampling the processor queue
// every few seconds for the life of the process. That is exactly the standing
// measurement these nodes exist to avoid, so the field is absent here rather
// than filled in with an emulation nobody asked to run.
func loadAverage(ctx context.Context) ([3]float64, bool) {
	return [3]float64{}, false
}
