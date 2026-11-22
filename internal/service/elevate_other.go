//go:build !windows

package service

// ElevateIfNeeded is a Windows affordance; everywhere else an unprivileged
// system install fails with the OS's own permission error, and sudo is the
// operator's to type.
func ElevateIfNeeded(string, bool) (bool, error) {
	return false, nil
}

// adoptCallerConsole exists for the elevated Windows relaunch; nothing else
// starts with a console that is not the caller's.
func adoptCallerConsole() {}
