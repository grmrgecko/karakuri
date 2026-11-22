//go:build !windows

package exec

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// treePlatform holds the launched command on Unix, where the process group
// needs no handle beyond the leader's pid.
type treePlatform struct {
	cmd *exec.Cmd
}

// newProcessTree keeps cancellation on the child's dedicated group even when
// the leader exits before its descendants. Descendants that moved to another
// group or session are found through their parent links and signaled with the
// group. Cancel completes before Wait, including file-only or discarded output
// that has no copy pipes to wait on.
func newProcessTree(cmd *exec.Cmd, rawGrace string) (*processTree, error) {
	grace, err := processTerminationGrace(rawGrace)
	if err != nil {
		return nil, err
	}
	tree := &processTree{treePlatform: treePlatform{cmd: cmd}}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		tree.markCanceled()
		if cmd.Process == nil {
			return nil
		}
		return tree.record(terminateTree(cmd.Process.Pid, grace))
	}
	cmd.WaitDelay = 5 * time.Second
	return tree, nil
}

// run starts the command and waits for it.
func (t *processTree) run() error {
	return t.cmd.Run()
}

// terminateTree signals the group and its escaped descendants with SIGTERM,
// waits up to grace for them to exit, and sends SIGKILL to what remains.
func terminateTree(pgid int, grace time.Duration) error {
	escaped := escapedDescendants(pgid, nil)
	if grace > 0 {
		err := signalTree(pgid, escaped, syscall.SIGTERM)
		if err != nil {
			return errors.Join(fmt.Errorf("terminating process group: %w", err), killTree(pgid, escaped))
		}
		deadline := time.Now().Add(grace)
		for treeAlive(pgid, escaped) {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			time.Sleep(min(remaining, 25*time.Millisecond))
		}
	}
	return killTree(pgid, escaped)
}

// killTree sends SIGKILL to the group and to escaped descendants, including
// any that the escaped ones started during the grace period.
func killTree(pgid int, escaped []int) error {
	return signalTree(pgid, escapedDescendants(pgid, escaped), syscall.SIGKILL)
}

// signalTree delivers one signal, accepting members that exited between the
// last observation and the signal.
func signalTree(pgid int, escaped []int, signal syscall.Signal) error {
	var errs []error
	if err := syscall.Kill(-pgid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		errs = append(errs, fmt.Errorf("signaling process group %d: %w", pgid, err))
	}
	for _, pid := range escaped {
		if err := syscall.Kill(pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Errorf("signaling escaped descendant %d: %w", pid, err))
		}
	}
	return errors.Join(errs...)
}

// treeAlive reports whether the group or any escaped descendant still exists.
// A zombie still counts until its parent reaps it.
func treeAlive(pgid int, escaped []int) bool {
	if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
		return true
	}
	for _, pid := range escaped {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			return true
		}
	}
	return false
}

// escapedDescendants returns the processes descended from the group's members
// or from known that belong to another process group, which is what setsid or
// setpgid in a child does. The walk follows parent links, so a descendant that
// was orphaned to init before cancellation is no longer reachable. A failed
// process listing returns known, leaving the group signal as the only cleanup.
func escapedDescendants(pgid int, known []int) []int {
	pids, err := process.Pids()
	if err != nil {
		return known
	}
	children := map[int][]int{}
	var queue []int
	for _, raw := range pids {
		pid := int(raw)
		proc := &process.Process{Pid: raw}
		if ppid, err := proc.Ppid(); err == nil {
			children[int(ppid)] = append(children[int(ppid)], pid)
		}
		if group, err := syscall.Getpgid(pid); err == nil && group == pgid {
			queue = append(queue, pid)
		}
	}
	seen := map[int]bool{pgid: true}
	queue = append(queue, pgid)
	queue = append(queue, known...)
	var escaped []int
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if seen[child] {
				continue
			}
			seen[child] = true
			queue = append(queue, child)
			if group, err := syscall.Getpgid(child); err == nil && group != pgid {
				escaped = append(escaped, child)
			}
		}
	}
	for _, pid := range known {
		if !seen[pid] {
			seen[pid] = true
			escaped = append(escaped, pid)
		}
	}
	return escaped
}
