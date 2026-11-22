//go:build !windows

package resources

import (
	"os"
	"path/filepath"
)

// userConfigBase is ~/.config, fixed rather than read from XDG_CONFIG_HOME,
// and the same on macOS rather than Library/Application Support: install.sh
// writes these paths as literals, and an installer and a binary that could
// disagree about where the configuration went would find nothing.
func userConfigBase() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config"), true
}

// userDataBase is ~/.local/share, fixed the way userConfigBase is.
func userDataBase() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".local", "share"), true
}
