//go:build !windows

package storage

import (
	"os"
	"syscall"
)

// keepOwner gives a staged replacement the owner and group of the file it
// replaces. A staged file belongs to the agent's user and primary group, so
// without this a root agent rewriting another account's file takes it away
// from that account. Only root can give a file away. Any other agent keeps
// the group when it is a member of it, and otherwise the replacement stays
// its own.
func keepOwner(staged *os.File, prior os.FileInfo) {
	stat, ok := prior.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	if err := staged.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
		_ = staged.Chown(-1, int(stat.Gid))
	}
}
