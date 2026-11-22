package config

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"gopkg.in/yaml.v3"

	"github.com/grmrgecko/karakuri/internal/resources"
)

// AgentConfigName is the agent's configuration file. It is searched for in
// the same places as the server's config.yaml (beside the binary, the user
// config directory, then /etc/karakuri) but under its own name, so a host
// running both a server and an agent keeps one configuration directory and
// neither file has to know about the other.
const AgentConfigName = "agent.yaml"

// agentIdentityDirName is the subdirectory holding the agent's key,
// certificate, and identity, wherever the install keeps its state.
const agentIdentityDirName = "agent"

// AgentConfig is every setting the agent has. Unlike the server's Config it is
// one flat set rather than sections, because it is also the command line: the
// struct tags below are kong's grammar, and agent.yaml supplies a default for
// each flag through AgentResolver. That is what makes the precedence flag, then
// file, then default, and what keeps the file's keys and the flags from ever
// describing different things.
//
// The flags are global rather than per-command for the same reason: the file
// has always been one flat set applying to whichever command reads it, so a
// setting that lived on one subcommand would be a key the file could set and
// the other command would silently ignore.
type AgentConfig struct {
	// Dir holds the agent's key, certificate, and identity. Its default
	// follows where the configuration file was found; see DefaultAgentIdentityDir.
	Dir string `yaml:"dir" help:"Directory holding the agent's identity." default:"${defaultDir}" type:"path"`
	// Server is the address the agent dials, host:port. Enrolling requires it;
	// running falls back to the address stored at enrollment.
	Server string `yaml:"server" help:"Server address to dial, host:port. Required to enroll; overrides the stored address when running."`
	// Timeout is the default per-execution budget in seconds. A node may name
	// its own; this is what applies when it does not.
	Timeout int `yaml:"timeout" help:"Default per-execution timeout in seconds." default:"60"`
	// MaxOutput caps one file, clipboard, or log-line read, in bytes.
	MaxOutput int `yaml:"max_output" help:"Cap on one file, clipboard, or log-line read, in bytes." default:"262144"`
	// Disable withholds node types from registration entirely, so the host
	// never learns they exist. A name matching no node type stops the agent.
	Disable []string `yaml:"disable" help:"Node types this agent must not contribute, repeatable: --disable=power --disable=file-write. A name that matches no node type is an error." name:"disable"`
	// LogLevel and LogFormat build the agent's logger. They are flat rather
	// than a logging section because every other key here is too.
	LogLevel  string `yaml:"log_level" help:"Log level: debug, info, warn, error." default:"${defaultLogLevel}" name:"log-level"`
	LogFormat string `yaml:"log_format" help:"Log format: json or console." default:"${defaultLogFormat}" name:"log-format"`
	// AutoUpdate lets an agent started by a service manager install newer
	// releases over its own binary once a day, when its account can write
	// it, and restart through the manager.
	AutoUpdate bool `yaml:"auto_update" help:"Install newer releases automatically when running as a service." default:"true" negatable:"" name:"auto-update"`
	// UpdatePrerelease also accepts releases marked as pre-releases.
	UpdatePrerelease bool `yaml:"update_prerelease" help:"Also update to releases marked as pre-releases." name:"update-prerelease"`
}

// AC is the agent's configuration, the counterpart to the server's C. The agent
// sets it once the command line has been parsed, since kong is what resolves
// the file and the flags into one answer.
var AC *AgentConfig

// AgentVars are the kong variables AgentConfig's defaults reference. The
// identity directory depends on where the configuration file was found, which
// is why it is computed rather than written into the tag.
func AgentVars(configPath string) kong.Vars {
	return kong.Vars{
		"defaultDir":       DefaultAgentIdentityDir(configPath),
		"defaultLogLevel":  DefaultLogLevel,
		"defaultLogFormat": DefaultLogFormat,
	}
}

// AgentResolver loads the agent's configuration file for kong, which consults
// it only for flags the command line left unset: precedence reads flag, then
// file, then default. Keys are the yaml tags on AgentConfig, which are the flag
// names with dashes written as underscores (max_output), and a repeatable flag
// (disable) may be written as a sequence.
func AgentResolver(r io.Reader) (kong.Resolver, error) {
	values := map[string]any{}
	if err := yaml.NewDecoder(r).Decode(&values); err != nil && err != io.EOF {
		return nil, err
	}
	var resolve kong.ResolverFunc = func(_ *kong.Context, _ *kong.Path, flag *kong.Flag) (any, error) {
		raw, ok := values[strings.ReplaceAll(flag.Name, "-", "_")]
		if !ok {
			return nil, nil
		}
		return raw, nil
	}
	return resolve, nil
}

// DefaultAgentIdentityDir is where the agent keeps its identity when neither
// --dir nor the configuration file names a directory.
func DefaultAgentIdentityDir(configPath string) string {
	userDir, _ := resources.UserConfigDir()
	return agentIdentityDirFor(configPath, resources.BinDir(), userDir)
}

// agentIdentityDirFor places the identity by where the configuration file was
// found, so state lands wherever the rest of the install already lives: an
// agent.yaml in /etc/karakuri keeps its identity under /var/lib/karakuri, one
// running out of a build directory keeps it beside the binary, and any other
// location keeps it next to the file that named it. With no configuration file
// at all the identity falls back to the user's own config directory — the same
// root the server searches — with the key in a subdirectory of its own rather
// than loose beside config.yaml. An empty configPath means none was found, and
// an empty userDir means the home directory was unreadable, which is what a
// service manager started with no HOME looks like.
func agentIdentityDirFor(configPath, binDir, userDir string) string {
	if configPath == "" {
		if userDir != "" {
			return filepath.Join(userDir, agentIdentityDirName)
		}
		return filepath.Join(binDir, "var", agentIdentityDirName)
	}

	configDir := filepath.Dir(configPath)
	switch configDir {
	case filepath.Join(binDir, "etc"):
		return filepath.Join(binDir, "var", agentIdentityDirName)
	case resources.SystemConfigDir:
		return filepath.Join(resources.SystemDataDir, agentIdentityDirName)
	default:
		return filepath.Join(configDir, agentIdentityDirName)
	}
}
