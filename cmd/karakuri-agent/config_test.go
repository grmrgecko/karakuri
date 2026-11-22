package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/internal/config"
)

// TestAgentConfigResolvesFlags pins the agent.yaml format against kong: every
// key is a field of config.AgentConfig spelled with underscores, a sequence
// supplies a repeatable flag, and the command line still wins. The sequence is
// the fragile case, since kong hands its decoder a resolved []any and it only
// reaches the typed flag because that path exists. A config file that silently
// stopped applying would leave an operator's daemon running on defaults, with
// every node type it can serve registered. AgentConfig is embedded in the CLI
// rather than parsed on its own, so the grammar it has to satisfy is here.
func TestAgentConfigResolvesFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.AgentConfigName)
	require.NoError(t, os.WriteFile(path, []byte(`
dir: /var/lib/karakuri/agent
server: gateway.example:9443
timeout: 90
max_output: 500000
log_level: debug
log_format: console
disable:
  - power
  - open-app
`), 0o644), "the test needs a config file on disk to parse")

	parse := func(args ...string) CLI {
		t.Helper()
		var cli CLI
		parser, err := kong.New(&cli, kongOptions(path, true)...)
		require.NoError(t, err, "the CLI grammar must build")
		_, err = parser.Parse(args)
		require.NoError(t, err, "parsing must succeed for the resolved values to be checkable")
		return cli
	}

	cli := parse("run")
	assert.Equal(t, "/var/lib/karakuri/agent", cli.Dir, "the config file names where the identity lives")
	assert.Equal(t, "gateway.example:9443", cli.Server, "the config file supplies the server")
	assert.Equal(t, 90, cli.Timeout, "timeout comes from the config file, not the flag default")
	assert.Equal(t, 500000, cli.MaxOutput, "max_output is the underscored spelling of --max-output")
	assert.Equal(t, "debug", cli.LogLevel, "the config file sets the log level for every command")
	assert.Equal(t, "console", cli.LogFormat, "the config file selects human output over JSON")
	assert.Equal(t, []string{"power", "open-app"}, cli.Disable,
		"a YAML sequence resolves into the repeatable flag, which is how an operator withholds node types")

	cli = parse("run", "--dir=/opt/agent", "--timeout=5")
	assert.Equal(t, "/opt/agent", cli.Dir, "a flag overrides the directory the config file named")
	assert.Equal(t, 5, cli.Timeout, "a flag overrides a value the config file set")
	assert.Equal(t, "gateway.example:9443", cli.Server, "flags override only what they name")
}
