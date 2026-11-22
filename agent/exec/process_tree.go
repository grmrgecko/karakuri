package exec

import (
	"errors"
	"os/exec"
	"sync"
)

// processTree is one launched command and the cleanup its cancellation ran.
// Cancellation failures do not reach exec.Cmd.Wait, which reports the killed
// parent's exit instead, so they are recorded here for the node to report.
type processTree struct {
	treePlatform

	mu       sync.Mutex
	cleanup  error
	canceled bool
}

// markCanceled records that os/exec ran the command's Cancel. os/exec stops
// watching the context once the program exits, so a context that expires
// during the wait delay leaves the program's own exit as the outcome.
func (t *processTree) markCanceled() {
	t.mu.Lock()
	t.canceled = true
	t.mu.Unlock()
}

// wasCanceled reports whether a context cancellation stopped the command.
func (t *processTree) wasCanceled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.canceled
}

// record keeps a cleanup failure and returns it unchanged.
func (t *processTree) record(err error) error {
	if err == nil {
		return nil
	}
	t.mu.Lock()
	t.cleanup = errors.Join(t.cleanup, err)
	t.mu.Unlock()
	return err
}

// cleanupErr reports what cancellation could not terminate, nil when it
// stopped everything it found.
func (t *processTree) cleanupErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cleanup
}

// withCleanup adds a cleanup failure to a cancellation or timeout error, so
// the node does not report a clean stop while descendants may still run.
func withCleanup(err error, tree *processTree) error {
	if cleanup := tree.cleanupErr(); cleanup != nil {
		return errors.Join(err, errors.Join(errors.New("process cleanup failed; descendants may still be running"), cleanup))
	}
	return err
}

// exitStatus reads the exit code out of a finished run, returning an error
// for a run that never produced one. exec.ErrWaitDelay means the program
// exited zero while a descendant still held an output or input pipe open. The
// exit is still a success: the node does not wait for processes a completed
// program leaves behind.
func exitStatus(runErr error) (int, error) {
	if runErr == nil || errors.Is(runErr, exec.ErrWaitDelay) {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return 0, runErr
	}
	return exitErr.ExitCode(), nil
}
