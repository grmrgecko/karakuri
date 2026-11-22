//go:build !windows

package storage

import (
	"fmt"
	"os"
	"syscall"
)

// fileIdentity returns the device and inode pair Unix filesystems use to keep
// a file's identity stable across rename-based log rotation.
func fileIdentity(_ *os.File, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("%s has no device and inode identity", info.Name())
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
