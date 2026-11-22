//go:build !linux && !windows && !darwin

package wlinput

import (
	"os"

	"golang.org/x/sys/unix"
)

func createKeymapFile() (int, error) {
	file, err := os.CreateTemp("", "karakuri-keymap-*")
	if err != nil {
		return -1, err
	}
	name := file.Name()
	defer os.Remove(name)

	fd, dupErr := unix.Dup(int(file.Fd()))
	closeErr := file.Close()
	if dupErr != nil {
		return -1, dupErr
	}
	if closeErr != nil {
		unix.Close(fd)
		return -1, closeErr
	}
	unix.CloseOnExec(fd)
	return fd, nil
}
