package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// commandNode runs a program on the agent host with an explicit argument list.
// It is separate from the script node rather than a mode on it: a script is
// text the author writes and a shebang resolves, while a command is a program
// that already exists on the host, and the two share neither their config nor
// their failure modes. No shell is involved, so an argument holding a space, a
// quote, or a semicolon is one argument and nothing else.
type commandNode struct {
	opts agentnode.Options
}

func (commandNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:        nodeCommand,
		Folder:      folder,
		Label:       "Run command",
		Description: "Runs a program on this agent host with an argument list, no shell involved",
		Icon:        "las la-play",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in", TypeRule: processRule()},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "command",
				Label:       "Command",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "systemctl",
				Help: "Program to run, by name in PATH or by absolute path. There is no shell, " +
					"so pipes, redirects, and globs are not expanded; use a script node for those.",
			},
			{
				ID:      "args",
				Label:   "Arguments",
				Type:    flow.FieldJSON,
				Control: "list",
				Help:    "One entry per argument. {{ }} segments are rendered against the message.",
			},
			{
				ID:          "stdin_property",
				Label:       "Standard input",
				Type:        flow.FieldString,
				Placeholder: "payload",
				Help: "Dot path to the value piped to the program. Leave empty to give it none; " +
					"binary is piped as its bytes and anything structured as JSON.",
			},
			{
				ID:      "output_format",
				Label:   "Output format",
				Type:    flow.FieldSelect,
				Default: agentnode.FormatAuto,
				Options: agentnode.FormatOptions(),
				Help: "How to read stdout. Auto tries JSON and falls back to text; the others are " +
					"strict and fail the node when stdout does not match.",
			},
			{
				ID:          "working_dir",
				Label:       "Working directory",
				Type:        flow.FieldString,
				Placeholder: "/var/tmp",
				Help:        "Directory to run in. Defaults to the agent's own.",
			},
			{
				ID:      "env",
				Label:   "Environment",
				Type:    flow.FieldJSON,
				Control: "map",
				Help:    "Extra environment variables, added on top of the agent's own.",
			},
			{
				ID:          "stdout_path",
				Label:       "Write standard output to",
				Type:        flow.FieldString,
				Placeholder: "/var/tmp/command.out",
				Help:        "File on the agent host receiving stdout. Return mode also returns the stream; file-only mode requires this path.",
			},
			{
				ID:          "stderr_path",
				Label:       "Write standard error to",
				Type:        flow.FieldString,
				Placeholder: "/var/tmp/command.err",
				Help:        "File on the agent host receiving stderr. Return mode also returns the stream in metadata; file-only mode requires this path.",
			},
			{
				ID:      "ignore_exit_code",
				Label:   "Ignore the exit code",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Takes the normal branch whatever the program exits with, leaving the code in " +
					"meta.exit_code. Off, a non-zero exit takes the error branch.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout (seconds)",
				Type:        flow.FieldNumber,
				Placeholder: "60",
				Help:        "Per-execution time budget. Leave empty for the agent default.",
			},
		},
	}
	m.ConfigSpec = append(m.ConfigSpec, processFields()...)
	return m
}

// commandConfig is the decoded run-command configuration.
type commandConfig struct {
	processPolicy
	Command        string            `json:"command"`
	Args           []string          `json:"args"`
	StdinProperty  string            `json:"stdin_property"`
	OutputFormat   string            `json:"output_format"`
	WorkingDir     string            `json:"working_dir"`
	Env            map[string]string `json:"env"`
	StdoutPath     string            `json:"stdout_path"`
	StderrPath     string            `json:"stderr_path"`
	IgnoreExitCode bool              `json:"ignore_exit_code"`
	Timeout        int               `json:"timeout"`
}

// Execute runs the program and turns its stdout into the outgoing payload.
func (n *commandNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg commandConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	return n.execute(ctx, ec, inputs, cfg)
}

func (n *commandNode) execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg, cfg commandConfig) (map[string][]*nodeapi.Msg, error) {
	msg := agentnode.PrimaryInput(inputs)
	if err := cfg.processPolicy.validate(msg); err != nil {
		return nil, err
	}

	// Render the command line. Every part goes through the same interpolation,
	// so an argument can carry a value from the message without a script.
	name, err := agentnode.Interpolate(ec, cfg.Command, msg)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("command node has no command")
	}
	args := make([]string, 0, len(cfg.Args))
	for i, raw := range cfg.Args {
		// An argument is passed as written. Trimming it would turn the lone
		// space a program takes as a delimiter into an empty string.
		rendered, err := ec.Interpolate(raw, msg)
		if err != nil {
			return nil, fmt.Errorf("argument %d: invalid template %q: %w", i+1, raw, err)
		}
		args = append(args, rendered)
	}

	timeout := agentnode.ResolveTimeout(cfg.Timeout, n.opts.ExecTimeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Run it.
	cmd := exec.CommandContext(runCtx, name, args...)
	tree, err := newProcessTree(cmd, cfg.TerminationGrace)
	if err != nil {
		return nil, err
	}
	if cfg.WorkingDir != "" {
		dir, err := agentnode.Interpolate(ec, cfg.WorkingDir, msg)
		if err != nil {
			return nil, err
		}
		cmd.Dir = dir
	}
	cmd.Env, err = commandEnv(ec, cfg, msg)
	if err != nil {
		return nil, err
	}
	if property := strings.TrimSpace(cfg.StdinProperty); property != "" {
		value, found := agentnode.ResolveProperty(msg, property)
		if !found {
			return nil, fmt.Errorf("property %q is not present on the message", property)
		}
		cmd.Stdin = strings.NewReader(string(agentnode.ValueBytes(value)))
	}

	stdoutPath, err := outputPath(ec, cfg.StdoutPath, msg)
	if err != nil {
		return nil, fmt.Errorf("standard output path: %w", err)
	}
	stderrPath, err := outputPath(ec, cfg.StderrPath, msg)
	if err != nil {
		return nil, fmt.Errorf("standard error path: %w", err)
	}
	if stdoutPath != "" && stdoutPath == stderrPath {
		return nil, fmt.Errorf("standard output and standard error cannot write to the same path")
	}
	if err := validateCapture(cfg.StdoutMode, stdoutPath); err != nil {
		return nil, err
	}
	if err := validateCapture(cfg.StderrMode, stderrPath); err != nil {
		return nil, err
	}
	stdoutCapture, err := newOutputCapture(stdoutPath, cfg.StdoutMode)
	if err != nil {
		return nil, fmt.Errorf("standard output: %w", err)
	}
	defer stdoutCapture.cleanup()
	stderrCapture, err := newOutputCapture(stderrPath, cfg.StderrMode)
	if err != nil {
		return nil, fmt.Errorf("standard error: %w", err)
	}
	defer stderrCapture.cleanup()
	cmd.Stdout = stdoutCapture.Writer()
	cmd.Stderr = stderrCapture.Writer()
	runErr := tree.run()

	stdout, stdoutErr := stdoutCapture.finish()
	stderr, stderrErr := stderrCapture.finish()
	if stdoutErr != nil {
		return nil, fmt.Errorf("reading standard output: %w", stdoutErr)
	}
	if stderrErr != nil {
		return nil, fmt.Errorf("reading standard error: %w", stderrErr)
	}
	if len(stderr) > 0 {
		ec.Debug(string(stderr), "stderr")
	}

	// Decide the outcome. A timeout is always a failure; a non-zero exit is one
	// unless the author said the code is the answer they are after. A context
	// that expired after the program exited stopped nothing, so the exit
	// stands. Start reports a context that was done before launch.
	if runCtx.Err() != nil && (tree.wasCanceled() || errors.Is(runErr, runCtx.Err())) {
		if ctx.Err() != nil {
			return nil, withCleanup(ctx.Err(), tree)
		}
		return nil, withCleanup(fmt.Errorf("%s timed out after %s", name, timeout), tree)
	}
	exitCode, err := exitStatus(runErr)
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", name, err)
	}
	if !cfg.accepts(exitCode, cfg.IgnoreExitCode) {
		if detail := agentnode.FirstLine(strings.TrimSpace(string(stderr))); detail != "" {
			return nil, fmt.Errorf("%s exited with code %d: %s", name, exitCode, detail)
		}
		return nil, fmt.Errorf("%s exited with code %d", name, exitCode)
	}

	out, err := cfg.output(msg, cfg.OutputFormat, stdout, map[string]any{
		"command": name, "exit_code": exitCode, "stderr": strings.TrimRight(string(stderr), "\n"),
	})
	if err != nil {
		return nil, err
	}
	if stdoutPath != "" {
		out.Meta["stdout_path"] = stdoutPath
	}
	if stderrPath != "" {
		out.Meta["stderr_path"] = stderrPath
	}
	return nodeapi.One("out", out), nil
}

// commandEnv assembles the child environment: the agent's own, then the node's
// additions, each rendered against the message.
func commandEnv(ec nodeapi.ExecContext, cfg commandConfig, msg *nodeapi.Msg) ([]string, error) {
	env := os.Environ()
	for k, v := range cfg.Env {
		// A value is passed as written, like an argument. Trimming it would
		// empty a separator such as a lone space.
		rendered, err := ec.Interpolate(v, msg)
		if err != nil {
			return nil, fmt.Errorf("environment variable %s: invalid template %q: %w", k, v, err)
		}
		env = append(env, k+"="+rendered)
	}
	return env, nil
}

// outputPath resolves an optional path without treating an empty field as an
// error.
func outputPath(ec nodeapi.ExecContext, raw string, msg *nodeapi.Msg) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return agentnode.Path(ec, raw, msg)
}
