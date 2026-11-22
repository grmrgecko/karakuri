//go:build darwin

package system

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// macOS is the one platform whose process state cannot be read without
// running something. The BSD kinfo_proc the kernel hands out still carries
// p_stat, but Darwin no longer maintains it — every process on a live system
// reports SRUN and a sleep time of zero, measured across 536 processes on a
// Sonoma host — because the real state lives in the Mach thread layer, which
// needs a task port the agent does not hold for another user's processes.
// That is why the library shells out here and not on Linux or the BSDs.
//
// What it does wrong is run ps once per process: a 537-process listing paid
// 2.1 seconds in forks for what one invocation answers in eleven
// milliseconds. So the table is read once per listing and looked up per
// process, which keeps exactly what ps reports.

// statusTable maps pid to the state char ps printed, read once per listing.
type statusTable map[int32]string

// newStatusTable reads every process's state in one ps invocation. A failure
// answers an empty table, and each lookup then falls back to the library's
// own per-process call rather than dropping the field.
func newStatusTable(ctx context.Context) statusTable {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,state=").Output()
	if err != nil {
		return statusTable{}
	}
	table := make(statusTable, 512)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil {
			continue
		}
		table[int32(pid)] = fields[1]
	}
	return table
}

// status reports one process's state in the library's vocabulary.
func (t statusTable) status(ctx context.Context, proc *process.Process) ([]string, error) {
	state, ok := t[proc.Pid]
	if !ok || state == "" {
		return proc.StatusWithContext(ctx)
	}
	return []string{convertStateChar(state[:1])}, nil
}

// convertStateChar maps a ps state letter onto the library's status names, so
// a payload reads the same however the state was obtained. Only the first
// letter is the state; what follows it describes the process's session and
// scheduling, which the library discards too.
func convertStateChar(letter string) string {
	switch letter {
	case "D", "U":
		return process.Blocked
	case "I":
		return process.Idle
	case "L":
		return process.Lock
	case "R":
		return process.Running
	case "S":
		return process.Sleep
	case "T", "t":
		return process.Stop
	case "W":
		return process.Wait
	case "Z":
		return process.Zombie
	default:
		return ""
	}
}
