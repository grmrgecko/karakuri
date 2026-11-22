//go:build !windows && !darwin

package linuxdesk

import (
	"bufio"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// A URL's default handler is written down, not discovered: the mime-apps spec
// keeps x-scheme-handler entries in mimeapps.list across the config and data
// tiers, and xdg-open is a shell script walking those files. Reading them
// here is what lets a link open with neither a portal nor xdg-open
// installed; the tools stay behind it for the associations this lookup does
// not model.

// openURLByHandler resolves the URL's scheme to its registered .desktop
// entry and launches it, the way xdg-open's generic path does.
func openURLByHandler(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return errNoHandler
	}
	id, ok := defaultSchemeHandler(parsed.Scheme)
	if !ok {
		return errNoHandler
	}
	return launchDesktopEntry(id, []string{rawURL})
}

// errNoHandler reports that no association names a handler, sending the
// caller to the tools.
var errNoHandler = errors.New("no registered handler for the URL's scheme")

// defaultSchemeHandler walks the mimeapps lists for x-scheme-handler/<scheme>
// and answers the first listed entry that is actually installed.
func defaultSchemeHandler(scheme string) (string, bool) {
	key := "x-scheme-handler/" + strings.ToLower(scheme)
	for _, path := range mimeappsPaths() {
		for _, id := range mimeappsLookup(path, key) {
			if _, err := findDesktopEntry(id); err == nil {
				return id, true
			}
		}
	}
	return "", false
}

// mimeappsPaths lists every association file in precedence order: the
// desktop-prefixed then plain mimeapps.list through the config tiers, the
// deprecated data-tier locations, and last the legacy defaults.list.
func mimeappsPaths() []string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			configHome = filepath.Join(home, ".config")
		}
	}
	configDirs := os.Getenv("XDG_CONFIG_DIRS")
	if configDirs == "" {
		configDirs = "/etc/xdg"
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}

	// The current desktop's own list shadows the plain one per directory,
	// e.g. kde-mimeapps.list before mimeapps.list.
	var desktops []string
	for _, d := range strings.Split(os.Getenv("XDG_CURRENT_DESKTOP"), ":") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			desktops = append(desktops, d)
		}
	}
	perDir := func(dir string) []string {
		var paths []string
		for _, desktop := range desktops {
			paths = append(paths, filepath.Join(dir, desktop+"-mimeapps.list"))
		}
		return append(paths, filepath.Join(dir, "mimeapps.list"))
	}

	var paths []string
	if configHome != "" {
		paths = append(paths, perDir(configHome)...)
	}
	for _, dir := range filepath.SplitList(configDirs) {
		if dir != "" {
			paths = append(paths, perDir(dir)...)
		}
	}
	if dataHome != "" {
		paths = append(paths, perDir(filepath.Join(dataHome, "applications"))...)
	}
	for _, dir := range filepath.SplitList(dataDirs) {
		if dir != "" {
			paths = append(paths, perDir(filepath.Join(dir, "applications"))...)
			paths = append(paths, filepath.Join(dir, "applications", "defaults.list"))
		}
	}
	return paths
}

// mimeappsLookup reads one association file's [Default Applications] group
// and returns the entries listed for the type, in order. A missing file
// answers nothing.
func mimeappsLookup(path, mimeType string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	inDefaults := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			inDefaults = line == "[Default Applications]"
		case inDefaults:
			name, value, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(name) != mimeType {
				continue
			}
			var ids []string
			for _, id := range strings.Split(value, ";") {
				id = strings.TrimSuffix(strings.TrimSpace(id), ".desktop")
				if id != "" {
					ids = append(ids, id)
				}
			}
			return ids
		}
	}
	return nil
}
