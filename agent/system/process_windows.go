package system

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

// sendNamedSignal reports that Windows has no signal to deliver. There is no
// signal table there at all: terminate and kill both end the process through
// TerminateProcess, and interrupt and hangup have nothing to map onto, so the
// node says so rather than ending a process the author asked to reload.
func sendNamedSignal(ctx context.Context, proc *process.Process, signal string) error {
	return fmt.Errorf("Windows cannot deliver %s; terminate and kill are the signals it has", signal)
}
