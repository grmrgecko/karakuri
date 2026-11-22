// Package config holds the configuration for both binaries: server.go for
// karakuri, agent.go for karakuri-agent. The two read their settings very
// differently — the server unmarshals a YAML file into sections through viper,
// while the agent's file only supplies defaults for kong flags — but they share
// a search path, a logging vocabulary, and the convention that a file found
// beside the binary belongs to a self-contained install. Keeping both here is
// what stops those conventions drifting apart.
package config

// Default log settings, shared so the agent's flags and the server's config
// defaults describe logging the same way. Format "console" selects human
// output; anything else is JSON.
const (
	DefaultLogLevel  = "info"
	DefaultLogFormat = "json"
)

// LoggingConfig controls log level and output format.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}
