package exec

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

type automationScriptNode struct{ opts agentnode.Options }

func (automationScriptNode) Meta() nodeapi.NodeMeta {
	m := (commandNode{}).Meta()
	m.Type = "automation-script"
	m.Label = "Automation script"
	m.Description = "Runs a karakuri-auto command file on this agent host"
	m.Outputs[0].Type = flow.PortString
	m.Outputs[0].TypeRule = nil
	fields := []nodeapi.FieldSpec{
		{ID: "executable", Label: "Automation executable", Type: flow.FieldString, Default: "karakuri-auto", Required: true, Help: "Installed karakuri-auto path or program in PATH. Uses that executable's desktop and resident setup."},
		{ID: "source", Label: "Source", Type: flow.FieldSelect, Default: "file", Options: []nodeapi.Option{{Value: "file", Label: "Existing file"}, {Value: "inline", Label: "Inline commands"}}},
		{ID: "script", Label: "Command file", Type: flow.FieldString, Required: true, ShowIf: &nodeapi.ShowIf{Field: "source", Equals: "file"}, Help: "Existing file on the agent host. Supports {{ }} expressions."},
		{ID: "commands", Label: "Commands", Type: flow.FieldText, Required: true, ShowIf: &nodeapi.ShowIf{Field: "source", Equals: "inline"}, Help: "Literal karakuri-auto command text. Message expressions are not expanded."},
		{ID: "log", Label: "Command log", Type: flow.FieldString, Help: "Optional host-local log file. Contains command output and line results."},
	}
	for _, f := range m.ConfigSpec {
		switch f.ID {
		case "working_dir", "env", "stdout_path", "stderr_path", "timeout":
			fields = append(fields, f)
		}
	}
	m.ConfigSpec = fields
	return m
}

func (n *automationScriptNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg struct {
		commandConfig
		Executable string `json:"executable"`
		Script     string `json:"script"`
		Log        string `json:"log"`
		Source     string `json:"source"`
		Commands   string `json:"commands"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	switch cfg.Source {
	case "", "file":
		if strings.TrimSpace(cfg.Script) == "" {
			return nil, fmt.Errorf("automation command file is required")
		}
	case "inline":
		path, err := writeAutomationCommands(cfg.Commands)
		if err != nil {
			return nil, err
		}
		cfg.Script = path
		defer func() {
			if err := os.Remove(path); err != nil {
				ec.Logger().Error("removing automation command file", zap.String("path", path), zap.Error(err))
			}
		}()
	default:
		return nil, fmt.Errorf("unknown automation script source %q", cfg.Source)
	}
	if cfg.Executable == "" {
		cfg.Executable = "karakuri-auto"
	}
	cfg.Command = cfg.Executable
	// Command arguments are passed as written, so the two paths are trimmed
	// here the way every other path field is.
	cfg.Args = []string{"script", strings.TrimSpace(cfg.Script)}
	if log := strings.TrimSpace(cfg.Log); log != "" {
		cfg.Args = append(cfg.Args, "--log", log)
	}
	cfg.processPolicy = processPolicy{}
	cfg.OutputFormat = "text"
	cfg.IgnoreExitCode = false
	cfg.StdinProperty = ""
	command := commandNode{opts: n.opts}
	return command.execute(ctx, ec, inputs, cfg.commandConfig)
}

// writeAutomationCommands closes its private file before a Windows child opens it.
func writeAutomationCommands(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("automation commands are required")
	}
	file, err := os.CreateTemp("", "karakuri-automation-*.txt")
	if err != nil {
		return "", fmt.Errorf("creating automation command file: %w", err)
	}
	_, writeErr := file.WriteString(source)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(file.Name())
		if writeErr != nil {
			return "", fmt.Errorf("writing automation command file: %w", writeErr)
		}
		return "", fmt.Errorf("closing automation command file: %w", closeErr)
	}
	return file.Name(), nil
}
