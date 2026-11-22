//go:build !darwin

package system

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/host"
)

// hostIdentity reads the host's identity through the library, which answers
// from /proc, the registry, or sysctl without running anything. Only macOS
// needs its own reader.
func hostIdentity(ctx context.Context) (hostFacts, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return hostFacts{}, fmt.Errorf("reading host info: %w", err)
	}
	return hostFacts{
		Hostname:        info.Hostname,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		KernelArch:      info.KernelArch,
		Uptime:          info.Uptime,
		BootTime:        time.Unix(int64(info.BootTime), 0),
	}, nil
}
