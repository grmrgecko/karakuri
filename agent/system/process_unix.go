//go:build !windows

package system

import (
	"context"
	"fmt"
	"syscall"

	"github.com/shirou/gopsutil/v4/process"
)

// sendNamedSignal delivers the signals that have no gopsutil method of their
// own. Hangup is the one most daemons read as "reload your configuration",
// which is why it is offered beside the two that end a process.
func sendNamedSignal(ctx context.Context, proc *process.Process, signal string) error {
	switch signal {
	case signalInterrupt:
		return proc.SendSignalWithContext(ctx, syscall.SIGINT)
	case signalHangup:
		return proc.SendSignalWithContext(ctx, syscall.SIGHUP)
	default:
		return fmt.Errorf("unknown signal %q", signal)
	}
}
