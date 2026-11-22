//go:build !darwin

package system

import (
	"context"

	"github.com/shirou/gopsutil/v4/process"
)

// statusTable is macOS's answer to a state it cannot read without running
// something; everywhere else the library reads /proc or the kinfo_proc
// directly, so the table holds nothing and the lookup is the library call.
type statusTable struct{}

// newStatusTable reads nothing on these platforms.
func newStatusTable(ctx context.Context) statusTable { return statusTable{} }

// status reports one process's state through the library.
func (statusTable) status(ctx context.Context, proc *process.Process) ([]string, error) {
	return proc.StatusWithContext(ctx)
}
