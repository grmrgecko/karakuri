// Package instlock holds the lock that marks an installation's data directory
// as in use by a running server.
package instlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the lock file inside the data directory.
const FileName = "karakuri.lock"

// ErrHeld reports that another process holds the installation's lock.
var ErrHeld = errors.New("the server is running")

// Acquire takes the installation lock in dataDir without waiting. The lock is
// an operating system file lock, so it is released when the process exits,
// even after a crash, and leaves no stale marker. The returned function
// releases it early.
func Acquire(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}
	path := filepath.Join(dataDir, FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrHeld) {
			return nil, fmt.Errorf("%w: %s is locked by another karakuri process", ErrHeld, path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// TryLock takes an exclusive operating system lock on f without waiting. It
// returns ErrHeld when another open file holds the lock. Closing f releases
// it.
func TryLock(f *os.File) error {
	return lockFile(f)
}

// Unlock releases a lock TryLock took.
func Unlock(f *os.File) error {
	return unlockFile(f)
}
