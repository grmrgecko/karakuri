//go:build windows

package storage

import "os"

// keepOwner does nothing on Windows, where a file has no owner and group pair
// to carry over and the staged file inherits its directory's access list.
func keepOwner(_ *os.File, _ os.FileInfo) {}
