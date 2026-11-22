// Command karakuri-agent is the remote agent: a small daemon that enrolls with
// a karakuri server, then dials in and contributes nodes that run real scripts
// on this host. It runs on macOS, Linux, FreeBSD, and Windows.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/logger"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/internal/version"
)

// CLI is the command tree. The settings themselves are config.AgentConfig,
// embedded so kong's grammar and agent.yaml's keys are one declaration; only
// the arguments that are genuinely per-command live on their commands.
type CLI struct {
	config.AgentConfig

	Enroll             EnrollCmd             `cmd:"" help:"Register this host with a karakuri server using a one-time token."`
	Run                RunCmd                `cmd:"" help:"Connect to the server and serve this agent's nodes."`
	Service            ServiceCmd            `cmd:"" help:"Install and control the agent as a service."`
	Credential         CredentialCmd         `cmd:"" help:"Store and manage account passwords for credentialed unlock."`
	CredentialProvider CredentialProviderCmd `cmd:"" help:"Install or remove the credential provider the logon screen loads."`
	Update             UpdateCmd             `cmd:"" help:"Check for a newer release and install it."`
	Version            VersionCmd            `cmd:"" help:"Print the agent version."`

	// SessionHelper is how a privileged agent reaches a desktop, not something
	// an operator runs: the agent starts it as the logged-in user and speaks to
	// it over a pipe. Hidden because a command with no output and no arguments
	// in the help is a question rather than an answer.
	SessionHelper SessionHelperCmd `cmd:"" hidden:"" help:"Serve one desktop session for a privileged agent."`

	// SessionInput is the Windows counterpart: the one-shot helper a SYSTEM
	// agent starts inside a locked session to type its password. Hidden for
	// the same reason — its input is a request the parent writes.
	SessionInput SessionInputCmd `cmd:"" hidden:"" help:"Type one credential into a locked console."`
}

// VersionCmd prints the build version.
type VersionCmd struct{}

// Run prints the version.
func (VersionCmd) Run() error {
	fmt.Printf("%s-agent %s\n", version.Name, version.String())
	return nil
}

// Logger builds the agent's logger from the resolved configuration, so a
// command that reports anything reports it the way the operator asked for.
func Logger() (*zap.Logger, error) {
	return logger.New(config.AC.LogLevel, config.AC.LogFormat)
}

// kongOptions are the parser options the CLI is built with. They are one
// function because the grammar test builds the same parser: a variable a flag
// tag references has to be declared in both, and a parser missing one fails at
// construction rather than at the flag.
func kongOptions(configPath string, found bool) []kong.Option {
	options := []kong.Option{
		kong.Name("karakuri-agent"),
		kong.Description("Karakuri remote agent."),
		kong.UsageOnError(),
		config.AgentVars(configPath),
		kong.Vars{"serviceActions": strings.Join(service.Actions, ",")},
	}
	if found {
		options = append(options, kong.Configuration(config.AgentResolver, configPath))
	}
	return options
}

func main() {
	// Find the configuration file first: it supplies flag defaults, and where
	// it was found decides where the identity lives when nothing names a
	// directory.
	configPath, found := resources.FindConfigFile(config.AgentConfigName)

	// Build the parser rather than kong.Parse, which panics when an option
	// fails: a config file that does not parse is an operator mistake and
	// deserves a message, not a stack trace.
	var cli CLI
	parser, err := kong.New(&cli, kongOptions(configPath, found)...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "karakuri-agent: %v\n", err)
		os.Exit(1)
	}
	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)

	// Publish the resolved settings before running: kong is what merges the
	// flags with agent.yaml, so this is the first point they are the answer.
	config.AC = &cli.AgentConfig

	ctx.FatalIfErrorf(ctx.Run(&cli))
}
