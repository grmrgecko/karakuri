//go:build !windows

package exec

import (
	"os"
	"syscall"
)

// writeScript writes the script file with no fork in progress. A child forked
// on another goroutine while the file is open for writing holds a copy of that
// descriptor until its own exec, and the kernel refuses to execute a file
// anyone has open for writing, so a concurrent run failed with "text file
// busy". Process creation takes syscall.ForkLock for writing, which the read
// lock held here keeps out until the descriptor is closed.
func writeScript(path string, data []byte) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, data, 0o700)
}
