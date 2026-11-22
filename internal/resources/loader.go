package resources

import (
	"os"
	"path/filepath"
)

// System install locations. Both are searched for configuration and both are
// where a packaged install keeps its state, so callers deriving one directory
// from another (the agent places its identity by where its config was found)
// name them rather than repeating the strings.
const (
	SystemConfigDir = "/etc/karakuri"
	SystemDataDir   = "/var/lib/karakuri"
)

// BinDir returns the directory containing the running executable, which is the
// root of a dev or self-contained install.
func BinDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// ResourceType identifies a category of loadable assets.
type ResourceType string

const (
	// Runtimes holds optional script-engine assets such as the CPython
	// WebAssembly module used by the sandboxed Python script node.
	Runtimes ResourceType = "runtimes"
	// Plugins holds plugin packages, one directory per plugin with a
	// manifest.yaml declaring what it contributes: an executable, dashboard
	// widget packages, or both.
	Plugins ResourceType = "plugins"
	// Themes holds dashboard theme packages, one directory per package with a
	// manifest.yaml and a styles.css of CSS custom properties.
	Themes ResourceType = "themes"
)

// SystemResourceDir is where a system install keeps the assets the binary did
// not bring with it.
const SystemResourceDir = "/usr/share/karakuri"

// GetResourcePaths resolves resource directories in priority order: beside the
// binary, then the user's own directories, then the system's. Every existing
// one is returned, because the callers merge them — plugin discovery keeps the
// first copy of a name it sees, so a personal plugin sits beside the installed
// set rather than replacing it. A directory beside the binary is the exception
// and is returned alone: an unpacked archive or a dev tree is a complete set,
// and mixing an installed one into it is how a build ends up running someone
// else's plugins.
func GetResourcePaths(resourceType ResourceType) []string {
	localPath := filepath.Join(BinDir(), string(resourceType))
	if _, err := os.Stat(localPath); err == nil {
		return []string{localPath}
	}

	var candidates []string
	if dir, ok := UserConfigDir(); ok {
		candidates = append(candidates, filepath.Join(dir, string(resourceType)))
	}
	if dir, ok := UserDataDir(); ok {
		candidates = append(candidates, filepath.Join(dir, string(resourceType)))
	}
	candidates = append(candidates,
		filepath.Join(SystemConfigDir, string(resourceType)),
		filepath.Join(SystemResourceDir, string(resourceType)),
	)

	var paths []string
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

// UserConfigDir returns the per-user configuration directory and whether the
// base could be resolved: ~/.config/karakuri, or %AppData%\karakuri on
// Windows. A service manager that starts a process with no HOME leaves it
// unresolved, which is why callers get the answer instead of a guess.
func UserConfigDir() (string, bool) {
	base, ok := userConfigBase()
	if !ok {
		return "", false
	}
	return filepath.Join(base, "karakuri"), true
}

// UserDataDir returns the per-user resource and state directory and whether
// the base could be resolved: ~/.local/share/karakuri, or
// %LocalAppData%\karakuri on Windows. It is what a user install fills
// instead of /usr/share/karakuri and /var/lib/karakuri, neither of which an
// account without root can write.
func UserDataDir() (string, bool) {
	base, ok := userDataBase()
	if !ok {
		return "", false
	}
	return filepath.Join(base, "karakuri"), true
}

// ConfigSearchPaths returns the locations searched for the named configuration
// file, in priority order: beside the binary, the user's own directory, then
// the system's. A person's configuration outranks the system's for the same
// reason their resources do — a user install on a host that also has a system
// one has to reach its own database rather than the one under /var/lib it
// cannot write. A system service has no home directory of its own to find,
// which is what keeps it on /etc.
func ConfigSearchPaths(name string) []string {
	paths := []string{filepath.Join(BinDir(), "etc", name)}
	if dir, ok := UserConfigDir(); ok {
		paths = append(paths, filepath.Join(dir, name))
	}
	return append(paths, filepath.Join(SystemConfigDir, name))
}

// FindConfigFile returns the first configuration file of the given name that
// exists, and whether one was found. GetConfigPath cannot tell a real file
// from its fallback, so callers whose behavior depends on where the file
// actually lives ask here.
func FindConfigFile(name string) (string, bool) {
	for _, path := range ConfigSearchPaths(name) {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// GetConfigPath returns the first configuration file found in the search path,
// falling back to the dev default.
func GetConfigPath() string {
	if path, ok := FindConfigFile("config.yaml"); ok {
		return path
	}
	return filepath.Join(BinDir(), "etc", "config.yaml")
}

// GetDataPath returns the first data directory found in the search path,
// falling back to the dev default. Unlike resources there is only ever one:
// state is written rather than read, so a second directory would be a second
// database.
func GetDataPath() string {
	paths := []string{filepath.Join(BinDir(), "var")}
	if dir, ok := UserDataDir(); ok {
		paths = append(paths, filepath.Join(dir, "var"))
	}
	paths = append(paths, SystemDataDir)

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return filepath.Join(BinDir(), "var")
}
