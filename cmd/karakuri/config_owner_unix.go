//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// preserveOwner gives a staged copy the original file's owner. A rotation on a
// system install runs as root while the service account reads the config, and
// a copy owned by root:root at mode 0640 would leave the server starting on
// the placeholder secrets after the rename.
func preserveOwner(path string, original os.FileInfo) error {
	st, ok := original.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if err := os.Chown(path, int(st.Uid), int(st.Gid)); err != nil {
		return fmt.Errorf("keeping the owner of %s: %w", path, err)
	}
	return nil
}
