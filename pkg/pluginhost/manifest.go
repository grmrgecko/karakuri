package pluginhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/widgets"
)

// ExternalManifest is one discovered plugin: what it contributes plus where it
// lives. Every contribution is optional — a plugin may bring an executable,
// widget packages, or both — but one declaring nothing is not a plugin.
type ExternalManifest struct {
	Name        string         `yaml:"name"`
	Version     string         `yaml:"version"`
	Description string         `yaml:"description"`
	Author      string         `yaml:"author"`
	EntryPoint  string         `yaml:"entryPoint"`
	Widgets     []widgets.Spec `yaml:"widgets"`
	// DefaultDisabled leaves the plugin disabled until an operator enables it,
	// for plugins most installs have no use for. Only the first discovery
	// reads it; the stored enable choice wins afterwards.
	DefaultDisabled bool `yaml:"defaultDisabled"`
	// Dir is the plugin's directory; Executable is the resolved entry path,
	// empty for a plugin that ships no process.
	Dir        string `yaml:"-"`
	Executable string `yaml:"-"`
}

// HasProcess reports whether the plugin runs a subprocess. A widget-only
// plugin loads and unloads without one.
func (m ExternalManifest) HasProcess() bool { return m.Executable != "" }

// SearchPaths resolves where to look for plugins from the configured list.
// A nil list — the key left unset — means the standard resource search:
// plugins/ beside the binary, then the system install locations. An explicit
// empty list disables plugins, and is returned untouched.
func SearchPaths(configured []string) []string {
	if configured == nil {
		return resources.GetResourcePaths(resources.Plugins)
	}
	return configured
}

// DiscoverExternal scans each path for plugin directories: a subdirectory
// holding a manifest.yaml. Missing paths are skipped silently so a fresh
// install with no plugins directory starts clean. A path that cannot be read
// or a manifest that is refused costs only itself: the plugins found are
// returned along with an error naming everything that was left out.
func DiscoverExternal(paths []string) ([]ExternalManifest, error) {
	var out []ExternalManifest
	var refused []error
	seen := make(map[string]bool)
	for _, root := range paths {
		entries, err := os.ReadDir(root)
		if err != nil {
			if !os.IsNotExist(err) {
				refused = append(refused, fmt.Errorf("cannot read plugin path %s: %w", root, err))
			}
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			m, err := readManifest(dir)
			if err != nil {
				refused = append(refused, err)
				continue
			}
			if m == nil {
				continue
			}
			// Earlier paths win, so a local dev copy shadows an installed one.
			if seen[m.Name] {
				continue
			}
			seen[m.Name] = true
			out = append(out, *m)
		}
	}
	return out, errors.Join(refused...)
}

// readManifest loads one plugin directory's manifest and resolves its
// contributions against disk. A directory without a manifest is not a plugin
// and returns nil.
func readManifest(dir string) (*ExternalManifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest in %s: %w", dir, err)
	}

	m := &ExternalManifest{Dir: dir}
	if err := yaml.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("invalid manifest in %s: %w", dir, err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("manifest in %s has no name", dir)
	}

	// Resolve the executable when one is declared. A named entry point that is
	// missing is an error rather than a silent downgrade to widget-only: the
	// author said the process exists.
	if m.EntryPoint != "" {
		exe := filepath.Join(dir, m.EntryPoint)
		info, err := os.Stat(exe)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("plugin %s has no executable entry point %s", m.Name, exe)
		}
		m.Executable = exe
	}

	for i := range m.Widgets {
		if err := m.Widgets[i].Validate(m.Name, m.Version, m.Author, m.Description); err != nil {
			return nil, err
		}
	}

	if !m.HasProcess() && len(m.Widgets) == 0 {
		return nil, fmt.Errorf("plugin %s contributes nothing: no entryPoint and no widgets", m.Name)
	}
	return m, nil
}
