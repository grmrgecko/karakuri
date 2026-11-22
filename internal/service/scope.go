package service

import (
	"fmt"
	"os"
)

// UserScope decides where an install lands. "auto" follows the account doing
// the installing: a privileged one (root, or an elevated token on Windows)
// installs the system's service, and anyone else installs their own: a
// systemd --user unit, a launchd LaunchAgent, or on Windows a logon scheduled
// task, since its services have no per-user form and run outside the desktop
// session whatever account they use. That per-user place is both the only one
// the account can write and the only one a process can reach the desktop
// from. Both binaries ask, because both are installed the same two ways.
func UserScope(scope, goos string, privileged bool) (bool, error) {
	switch scope {
	case "user":
		if !HasUserScope(goos) {
			return false, fmt.Errorf("%s has no per-user service manager; install with --scope=system as root", goos)
		}
		return true, nil
	case "system":
		return false, nil
	default:
		return !privileged && HasUserScope(goos), nil
	}
}

// HasUserScope reports whether the platform has somewhere a user-scope
// install can land: a per-user service manager under systemd or launchd, and
// the Task Scheduler's logon tasks on Windows. Everywhere else a service is
// the system's, and installing one takes root.
func HasUserScope(goos string) bool {
	return goos == "linux" || goos == "darwin" || goos == "windows"
}

// LingerHint returns the note an installed user service needs on Linux, and an
// empty string when there is nothing to say. A user manager stops with the
// session unless the account lingers, which is the difference between a service
// that survives a logout and one that looks fine until the operator closes
// their desktop.
func LingerHint(def Definition, goos string) string {
	if !def.UserService || goos != "linux" {
		return ""
	}
	return fmt.Sprintf("run `loginctl enable-linger %s` to keep %s running while you are logged out.",
		currentUserName(), def.Name)
}

// currentUserName names the account for the linger hint, falling back to what
// the environment says when the OS will not answer.
func currentUserName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return fmt.Sprintf("%d", os.Getuid())
}
