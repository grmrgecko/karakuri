//go:build !windows && !darwin

package linuxdesk

import (
	"fmt"
	"path/filepath"

	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// A Terminal=true entry needs an emulator wrapped around its command, which
// is the one job gtk-launch was kept for. This is glib's own answer
// (gdesktopappinfo.c's terminal table): the spec'd xdg-terminal-exec first,
// then the desktop terminals with the flag each takes before the command.

// terminalEmulators is glib's search order. An empty flag means the command
// follows directly.
var terminalEmulators = []struct {
	name string
	flag string
}{
	{"xdg-terminal-exec", ""},
	{"kgx", "-e"},
	{"gnome-terminal", "--"},
	{"mate-terminal", "-x"},
	{"xfce4-terminal", "-x"},
	{"tilix", "-e"},
	{"konsole", "-e"},
	{"nxterm", "-e"},
	{"color-xterm", "-e"},
	{"rxvt", "-e"},
	{"dtterm", "-e"},
	{"xterm", "-e"},
}

// launchTerminalEntry runs a Terminal=true entry inside whichever emulator
// the host has, detached like every other launch.
func launchTerminalEntry(id string, args []string) error {
	path, err := findDesktopEntry(id)
	if err != nil {
		return err
	}
	entry, err := parseDesktopEntry(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	argv, err := expandExec(entry.exec, path, args)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	if len(argv) == 0 {
		return fmt.Errorf("%s has an empty Exec line", filepath.Base(path))
	}

	for _, term := range terminalEmulators {
		bin, ok := toolsh.ToolPath(term.name)
		if !ok {
			continue
		}
		full := []string{}
		if term.flag != "" {
			full = append(full, term.flag)
		}
		full = append(full, argv...)
		return toolsh.StartDetached(bin, full...)
	}
	return fmt.Errorf("%s runs in a terminal and no terminal emulator was found", id)
}
