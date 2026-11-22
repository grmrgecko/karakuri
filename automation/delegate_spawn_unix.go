//go:build unix && !darwin

package automation

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// spawnHelper starts this binary again as the target user, in that user's
// session, and returns the framed pipe to it. Credential is what makes the
// child the user: it is applied between fork and exec, so the process the
// desktop sees was never privileged and the session bus accepts it the way it
// accepts any of that user's own programs.
func spawnHelper(ctx context.Context, opts Options, target DelegateTarget) (*hrpc.Conn, func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("finding this binary to start the session helper: %w", err)
	}

	cmd := exec.Command(exe, opts.HelperCommand...)
	cmd.Env = target.Env
	cmd.Dir = target.Home
	// Dropping to the target user is what makes a privileged parent's helper the
	// session's own, and it is exactly what a same-user attach must not do:
	// setgroups and setuid to credentials the process already holds are refused
	// to a non-root caller, so a person attaching to their own session gets no
	// credential change and simply inherits the identity they already have.
	if target.UID != os.Geteuid() {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid:    uint32(target.UID),
				Gid:    uint32(target.GID),
				Groups: target.Groups,
			},
			// The helper stays in the parent's process group so it is torn down
			// with the agent rather than outliving it holding a clipboard
			// selection nobody can replace.
			Setpgid: false,
		}
	}
	cmd.Stderr = helperLog{logf: opts.Logf}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting the session helper as %s: %w", target, err)
	}

	stop := func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	return hrpc.NewConn(stdout, stdin), stop, nil
}

// helperLog forwards the child's diagnostics to the caller's log sink, so a
// backend failing inside the helper is not swallowed by a pipe nobody reads.
type helperLog struct{ logf func(string, ...any) }

// Write logs one chunk of the helper's standard error.
func (h helperLog) Write(p []byte) (int, error) {
	if h.logf != nil && len(p) > 0 {
		h.logf("session helper: %s", string(p))
	}
	return len(p), nil
}

var _ io.Writer = helperLog{}
