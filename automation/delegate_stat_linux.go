//go:build linux

package automation

import (
	"io/fs"
	"syscall"
)

// statUID reads the owning uid out of a stat result, reporting false on a
// platform-specific value this build does not recognise.
func statUID(info fs.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}
