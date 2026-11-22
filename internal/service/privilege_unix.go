//go:build !windows

package service

import "os"

// Privileged reports whether this process can register the system's services,
// which outside Windows is being root.
func Privileged() bool {
	return os.Getuid() == 0
}
