//go:build darwin

package macos

import "os"

// RunningAsRoot reports whether this process runs as the superuser — the
// identity that can install loginwindow authorization plugins and read the
// local password database.
func RunningAsRoot() bool {
	return os.Geteuid() == 0
}
