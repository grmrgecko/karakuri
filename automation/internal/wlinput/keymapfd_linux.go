//go:build linux

package wlinput

import "golang.org/x/sys/unix"

func createKeymapFile() (int, error) {
	return unix.MemfdCreate("karakuri-keymap", unix.MFD_CLOEXEC)
}
