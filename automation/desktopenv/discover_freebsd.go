//go:build freebsd

package desktopenv

import (
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Discover finds the live graphical session belonging to uid on FreeBSD, where
// there is no /proc environ to read and no logind to name the active session.
// The environment is read with procstat, the base system's own process
// inspector, so nothing has to be installed; a caller running as that user or as
// root can read it. ok is false when the user has no live graphical session.
//
// procstat is a subprocess rather than a sysctl read because FreeBSD does not
// export the KERN_PROC_ENV interface through a stable pure-Go binding, and
// procstat is the tool the base system ships for exactly this. It is the one
// place this package leaves the process to learn a session, and it does so only
// for the user's own processes or, for the root delegate, another user's.
func Discover(ctx context.Context, uid int) (Result, bool) {
	pids, err := userPIDs(ctx, uid)
	if err != nil {
		return Result{}, false
	}
	var candidates []map[string]string
	for _, pid := range pids {
		env := procEnviron(ctx, pid)
		if env["WAYLAND_DISPLAY"] == "" && env["DISPLAY"] == "" {
			continue
		}
		candidates = append(candidates, env)
	}
	// FreeBSD has no session manager to name the active session, so the ranking
	// heuristic decides on its own.
	return choose(candidates, "")
}

// userPIDs lists the process ids owned by uid.
func userPIDs(ctx context.Context, uid int) ([]int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-U", strconv.Itoa(uid), "-o", "pid=").Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// procEnviron reads one process's environment through procstat and keeps the
// session variables.
func procEnviron(ctx context.Context, pid int) map[string]string {
	out, err := exec.CommandContext(ctx, "procstat", "-e", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	return parseProcstatEnv(out)
}

// DiscoverAny finds a live graphical session for a root delegate to act on. It
// returns the owning uid and that session's environment.
//
// FreeBSD has no session manager to name the physically active seat, so this
// makes a deterministic choice rather than a live-active one: among the accounts
// that own a live graphical session, the lowest uid at or above 1000 wins. The
// 1000 floor keeps the display manager's own greeter and the system accounts out
// of the running, since those sit below it by convention; the single-user desktop
// that FreeBSD almost always is has exactly one candidate. A host whose only
// desktop account is below 1000 is not reached, which is the price of excluding a
// greeter without a session class to tell them apart.
func DiscoverAny(ctx context.Context) (int, Result, bool) {
	// One -o per column: BSD ps reads everything after a keyword's '=' as that
	// column's header, commas included, so "uid=,pid=" would print the uid alone
	// under a header of ",pid=".
	out, err := exec.CommandContext(ctx, "ps", "-ax", "-o", "uid=", "-o", "pid=").Output()
	if err != nil {
		return 0, Result{}, false
	}
	seen := map[int]bool{}
	var uids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		uid, err := strconv.Atoi(fields[0])
		if err != nil || uid < 1000 || seen[uid] {
			continue
		}
		seen[uid] = true
		uids = append(uids, uid)
	}
	// Sort so the choice does not depend on process-table order, which would let
	// the same host delegate to different users between calls.
	sort.Ints(uids)
	for _, uid := range uids {
		if r, ok := Discover(ctx, uid); ok {
			return uid, r, true
		}
	}
	return 0, Result{}, false
}
