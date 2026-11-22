package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Node type names this group contributes. They are the names an operator
// writes in the disable list and the names the host namespaces as
// agent.<id>.<name>, so they are constants rather than literals in the metadata.
const (
	nodeScript  = "script"
	nodeCommand = "command"
)

// scriptNode runs a script on the agent host through the real OS, picking the
// interpreter from the shebang. It is the counterpart of the sandbox script
// node, with the opposite trade: full access to the machine instead of none.
type scriptNode struct {
	opts agentnode.Options
}

func (scriptNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:        nodeScript,
		Folder:      folder,
		Label:       "Run script",
		Description: "Runs a real script on this agent host, interpreter chosen by the shebang",
		Icon:        "las la-terminal",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in", TypeRule: processRule()},
			{ID: flow.ScriptErrorPort, Label: "Error", Type: flow.PortAny, Kind: flow.PortKindError, TypeFrom: "in"},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:       "script",
				Label:    "Script",
				Type:     flow.FieldScript,
				Required: true,
				Help:     "The first line's shebang picks the interpreter, by path (#!/usr/bin/env python3) or by name (#!python3, #!powershell, #!cmd). A path is the program that runs, and a name is resolved on this host's PATH. The message arrives on stdin as JSON and in the PAYLOAD, TOPIC, META, VARS, and INPUT environment variables. A variable over 32,000 bytes is written to a file instead, and NAME_FILE holds its path. stdout is the result, returned stderr is logged, and exits outside the accepted list take the error branch.",
			},
			{
				ID:      "output_format",
				Label:   "Output format",
				Type:    flow.FieldSelect,
				Default: agentnode.FormatAuto,
				Options: agentnode.FormatOptions(),
				Help:    "How to read stdout. Auto tries JSON and falls back to text; the others are strict and fail the node if stdout does not match.",
			},
			{
				ID:          "working_dir",
				Label:       "Working directory",
				Type:        flow.FieldString,
				Placeholder: "/var/tmp",
				Help:        "Directory to run the script in. Defaults to a temporary directory.",
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
				Placeholder: "/var/tmp/script.out",
				Help:        "File on the agent host receiving stdout. Return mode also returns the stream; file-only mode requires this path.",
			},
			{
				ID:          "stderr_path",
				Label:       "Write standard error to",
				Type:        flow.FieldString,
				Placeholder: "/var/tmp/script.err",
				Help:        "File on the agent host receiving stderr. Return mode also logs and returns the stream; file-only mode requires this path.",
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

// scriptConfig is the node's configuration.
type scriptConfig struct {
	processPolicy
	Script       string            `json:"script"`
	OutputFormat string            `json:"output_format"`
	WorkingDir   string            `json:"working_dir"`
	Env          map[string]string `json:"env"`
	StdoutPath   string            `json:"stdout_path"`
	StderrPath   string            `json:"stderr_path"`
	Timeout      int               `json:"timeout"`
}

// Execute writes the script to a file, runs it as a real process, and turns its
// output into the outgoing message. A non-zero exit or a spawn failure is
// returned as an error, which the engine routes to the node's error branch.
func (n *scriptNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg scriptConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Script) == "" {
		return nil, errors.New("script node has no script")
	}

	msg := agentnode.PrimaryInput(inputs)
	if err := cfg.processPolicy.validate(msg); err != nil {
		return nil, err
	}

	// Resolve the timeout: node value, else agent default, else the ceiling.
	runCtx, cancel := context.WithTimeout(ctx, agentnode.ResolveTimeout(cfg.Timeout, n.opts.ExecTimeout))
	defer cancel()

	stdout, stderr, stdoutPath, stderrPath, exitCode, err := n.run(runCtx, cfg, msg, inputs, ec)

	// Surface stderr as console output either way; it is the fastest route to
	// why a script failed.
	if len(stderr) > 0 {
		ec.Debug(string(stderr), "stderr")
	}
	if err != nil {
		return nil, err
	}

	out, err := cfg.output(msg, cfg.OutputFormat, stdout, map[string]any{"exit_code": exitCode, "stderr": strings.TrimRight(string(stderr), "\n")})
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

// run performs the actual OS execution and returns captured output. It never
// touches the sandbox: the interpreter is the real one the shebang names, and
// the script runs with the agent user's privileges.
func (n *scriptNode) run(ctx context.Context, cfg scriptConfig, msg *nodeapi.Msg, inputs map[string]*nodeapi.Msg, ec nodeapi.ExecContext) (stdout, stderr []byte, stdoutPath, stderrPath string, exitCode int, err error) {
	dir := cfg.WorkingDir
	tmpDir, err := os.MkdirTemp("", "karakuri-agent-*")
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("creating work directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if dir == "" {
		dir = tmpDir
	} else {
		dir, err = agentnode.Interpolate(ec, dir, msg)
		if err != nil {
			return nil, nil, "", "", 0, fmt.Errorf("working directory: %w", err)
		}
	}

	sb, hasShebang := parseShebang(cfg.Script)
	scriptPath := filepath.Join(tmpDir, "script"+scriptExt(sb, hasShebang))
	if err := writeScript(scriptPath, []byte(scriptSource(cfg.Script, sb, hasShebang))); err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("writing script: %w", err)
	}

	cmd, err := command(ctx, sb, hasShebang, scriptPath)
	if err != nil {
		return nil, nil, "", "", 0, err
	}
	tree, err := newProcessTree(cmd, cfg.TerminationGrace)
	if err != nil {
		return nil, nil, "", "", 0, err
	}
	cmd.Dir = dir
	cmd.Env, err = buildEnv(cfg, msg, inputs, ec, tmpDir)
	if err != nil {
		return nil, nil, "", "", 0, err
	}
	cmd.Stdin = strings.NewReader(string(payloadJSON(msg)))

	stdoutPath, err = outputPath(ec, cfg.StdoutPath, msg)
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("standard output path: %w", err)
	}
	stderrPath, err = outputPath(ec, cfg.StderrPath, msg)
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("standard error path: %w", err)
	}
	if stdoutPath != "" && stdoutPath == stderrPath {
		return nil, nil, "", "", 0, fmt.Errorf("standard output and standard error cannot write to the same path")
	}
	if err := validateCapture(cfg.StdoutMode, stdoutPath); err != nil {
		return nil, nil, "", "", 0, err
	}
	if err := validateCapture(cfg.StderrMode, stderrPath); err != nil {
		return nil, nil, "", "", 0, err
	}
	stdoutCapture, err := newOutputCapture(stdoutPath, cfg.StdoutMode)
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("standard output: %w", err)
	}
	defer stdoutCapture.cleanup()
	stderrCapture, err := newOutputCapture(stderrPath, cfg.StderrMode)
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("standard error: %w", err)
	}
	defer stderrCapture.cleanup()
	cmd.Stdout = stdoutCapture.Writer()
	cmd.Stderr = stderrCapture.Writer()

	runErr := tree.run()
	stdout, err = stdoutCapture.finish()
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("reading standard output: %w", err)
	}
	stderr, err = stderrCapture.finish()
	if err != nil {
		return nil, nil, "", "", 0, fmt.Errorf("reading standard error: %w", err)
	}

	// A context that expired after the script exited stopped nothing, so the
	// exit stands. Start reports a context that was done before launch.
	if ctx.Err() != nil && (tree.wasCanceled() || errors.Is(runErr, ctx.Err())) {
		return stdout, stderr, stdoutPath, stderrPath, 0, withCleanup(ctx.Err(), tree)
	}
	exitCode, err = exitStatus(runErr)
	if err != nil {
		return stdout, stderr, stdoutPath, stderrPath, 0, fmt.Errorf("running script: %w", err)
	}
	if !cfg.accepts(exitCode, false) {
		return stdout, stderr, stdoutPath, stderrPath, exitCode, fmt.Errorf("script exited with code %d", exitCode)
	}
	return stdout, stderr, stdoutPath, stderrPath, exitCode, nil
}

// interpreterSpec is how one interpreter is invoked: the arguments that go
// before the script path, the extension the temp file needs, and whether the
// interpreter can read the shebang line as a comment. Only cmd cannot, which is
// what commentShebang exists for.
type interpreterSpec struct {
	args           []string
	ext            string
	commentShebang bool
}

// interpreters is what a shebang name means. Everything not listed still runs:
// the name is looked up on PATH and handed the script, which is what a shell
// would do. This table is for the ones that need more than that — PowerShell
// wants -File and refuses an unsigned script without a policy override, and cmd
// wants /c and a batch extension.
var interpreters = map[string]interpreterSpec{
	"python":     {ext: ".py"},
	"python3":    {ext: ".py"},
	"node":       {ext: ".js"},
	"bash":       {ext: ".sh"},
	"sh":         {ext: ".sh"},
	"zsh":        {ext: ".sh"},
	"perl":       {ext: ".pl"},
	"ruby":       {ext: ".rb"},
	"powershell": {args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}, ext: ".ps1"},
	"pwsh":       {args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}, ext: ".ps1"},
	"cmd":        {args: []string{"/c"}, ext: ".bat", commentShebang: true},
}

// interpreterFor returns what the table says about a shebang's interpreter.
// Windows names its programs with an extension and without regard to case, so
// the base name of C:\Windows\System32\cmd.exe is cmd.exe and the system
// spells PowerShell.exe with capitals. Both mean the entry the bare lower-case
// name has.
func interpreterFor(key string) interpreterSpec {
	key = strings.ToLower(key)
	return interpreters[strings.TrimSuffix(key, ".exe")]
}

// command builds the exec.Cmd. A shebang naming an absolute path is what the
// kernel itself reads, so where the platform honors that the file is executed
// directly and the whole shebang line applies, arguments included. Everywhere
// else — Windows, which has no shebang support, and a bare name like
// #!python3, which is not a path the kernel can exec — the name is resolved on
// PATH the way a shell resolves a command, and the interpreter is handed the
// script. PATH is what an install already sets up, on Windows too, where
// LookPath tries each PATHEXT extension and finds python.exe for "python".
//
// The interpreter map overrides all of it, for an interpreter that is not on
// PATH or an operator pinning which one runs.
func command(ctx context.Context, sb shebang, ok bool, scriptPath string) (*exec.Cmd, error) {
	// Windows is the platform that cannot read a shebang; everywhere else the
	// kernel does. It is a parameter rather than a check inside so the Windows
	// path is exercised from any host.
	return commandOn(ctx, runtime.GOOS != "windows", sb, ok, scriptPath)
}

// commandOn is command with the platform's shebang support given rather than
// looked up.
func commandOn(ctx context.Context, kernelReadsShebang bool, sb shebang, ok bool, scriptPath string) (*exec.Cmd, error) {
	if !ok {
		return nil, errors.New("script has no shebang; add one such as #!/usr/bin/env python3")
	}
	spec := interpreterFor(sb.Key)

	// A shebang naming a path has already answered the question, so it is
	// authoritative and no lookup happens. Where the platform reads the line
	// itself the file is executed directly, which applies the whole line,
	// arguments included; where it does not, the named program is run.
	if sb.Absolute {
		if kernelReadsShebang {
			if err := os.Chmod(scriptPath, 0o700); err != nil {
				return nil, fmt.Errorf("making script executable: %w", err)
			}
			return exec.CommandContext(ctx, scriptPath), nil
		}
		return exec.CommandContext(ctx, sb.Path, interpreterArgs(spec, sb, scriptPath)...), nil
	}

	// A bare name is resolved the way a shell resolves a command.
	interpreter, err := exec.LookPath(sb.Key)
	if err != nil {
		return nil, fmt.Errorf("no interpreter %q on PATH; install it, put it on the PATH of the "+
			"user this agent runs as, or name it by full path in the shebang", sb.Key)
	}
	return exec.CommandContext(ctx, interpreter, interpreterArgs(spec, sb, scriptPath)...), nil
}

// interpreterArgs assembles what follows the interpreter: its own required
// arguments, then any the shebang carried, then the script.
func interpreterArgs(spec interpreterSpec, sb shebang, scriptPath string) []string {
	args := make([]string, 0, len(spec.args)+len(sb.Args)+1)
	args = append(args, spec.args...)
	args = append(args, sb.Args...)
	return append(args, scriptPath)
}

// shebang is a script's first line, taken apart.
type shebang struct {
	// Key is the interpreter name: the env argument for
	// "#!/usr/bin/env python3", the basename for "#!/bin/bash", or the word
	// itself for "#!python3". It is what the interpreter map keys on and what
	// is looked up on PATH, so an author writes the command rather than a path.
	Key string
	// Args are the arguments the shebang carried after the interpreter.
	Args []string
	// Path is the interpreter exactly as the line wrote it, meaningful only
	// when Absolute: it is what runs on a platform that cannot read the
	// shebang itself.
	Path string
	// Absolute reports that the line named the interpreter by path. A POSIX
	// path is not absolute on Windows, which is what makes "#!/bin/bash" fall
	// through to a PATH lookup for bash there rather than to a path that
	// cannot exist.
	Absolute bool
}

// parseShebang reads a script's first line. A line that is not a shebang
// returns ok false; nothing here guesses an interpreter from the content.
func parseShebang(source string) (sb shebang, ok bool) {
	line := source
	if i := strings.IndexByte(source, '\n'); i >= 0 {
		line = source[:i]
	}
	line = strings.TrimRight(line, "\r")
	if !strings.HasPrefix(line, "#!") {
		return shebang{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return shebang{}, false
	}

	first := fields[0]
	rest := fields[1:]
	if filepath.Base(first) == "env" {
		// env's own flags come before the command it runs; -S is how a shebang
		// passes arguments through on the systems that need it.
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			split := rest[0] == "-S" || rest[0] == "--split-string"
			rest = rest[1:]
			if split {
				break
			}
		}
		if len(rest) == 0 {
			return shebang{}, false
		}
		return shebang{Key: rest[0], Args: trailing(rest[1:])}, true
	}
	return shebang{
		Key:      filepath.Base(first),
		Path:     first,
		Args:     trailing(rest),
		Absolute: filepath.IsAbs(first),
	}, true
}

// trailing normalizes an empty argument list to nil, so "no arguments" has one
// spelling rather than two.
func trailing(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	return args
}

// scriptExt names the temp file the way the interpreter expects. Windows keys
// on the extension for its own interpreters — cmd will not run a file that is
// not .bat or .cmd, and PowerShell's -File wants .ps1 — and elsewhere it is
// only a hint to anything reading the path.
func scriptExt(sb shebang, ok bool) string {
	if !ok {
		return ""
	}
	return interpreterFor(sb.Key).ext
}

// scriptSource is what actually gets written to the file. Every interpreter
// here reads # as a comment and ignores the shebang, except cmd, which would
// try to run it and print a "not recognized" line to stderr on every
// execution. The line is rewritten rather than removed so the line numbers in
// an error still match what the author wrote.
//
// The shebang line also loses a trailing carriage return. A kernel that reads
// the line takes everything after the path as part of it, so a CRLF ending
// names an interpreter that does not exist.
func scriptSource(source string, sb shebang, ok bool) string {
	if !ok {
		return source
	}
	line, rest := source, ""
	if i := strings.IndexByte(source, '\n'); i >= 0 {
		line, rest = source[:i], source[i:]
	}
	line = strings.TrimRight(line, "\r")
	if interpreterFor(sb.Key).commentShebang {
		line = "@rem " + line
	}
	return line + rest
}

// scriptEnvLimit is the largest message variable passed in the environment.
// Linux refuses execve with E2BIG when one string passes 128 KiB, and Windows
// caps a variable at 32,767 characters, so larger values go to a file instead.
const scriptEnvLimit = 32000

// buildEnv assembles the child environment: the agent's own, the message in the
// variables the help text names, then the node's own additions rendered against
// the message. A script reads the payload from stdin or from PAYLOAD, whichever
// suits the language. A message variable over scriptEnvLimit is written to dir
// and passed as NAME_FILE, the path of that file, with NAME left unset.
func buildEnv(cfg scriptConfig, msg *nodeapi.Msg, inputs map[string]*nodeapi.Msg, ec nodeapi.ExecContext, dir string) ([]string, error) {
	env := os.Environ()
	for _, v := range []struct{ name, value string }{
		{"PAYLOAD", string(payloadJSON(msg))},
		{"TOPIC", msg.Topic},
		{"META", jsonString(msg.Meta)},
		{"VARS", jsonString(ec.Vars())},
		{"INPUT", jsonString(namedInputs(inputs))},
	} {
		if len(v.value) <= scriptEnvLimit {
			env = append(env, v.name+"="+v.value)
			continue
		}
		path := filepath.Join(dir, strings.ToLower(v.name))
		if err := os.WriteFile(path, []byte(v.value), 0o600); err != nil {
			return nil, fmt.Errorf("writing %s: %w", v.name, err)
		}
		env = append(env, v.name+"_FILE="+path)
	}
	for k, v := range cfg.Env {
		// A value is passed as written, like a Run Command argument.
		rendered, err := ec.Interpolate(v, msg)
		if err != nil {
			return nil, fmt.Errorf("environment variable %s: invalid template %q: %w", k, v, err)
		}
		env = append(env, k+"="+rendered)
	}
	return env, nil
}

// namedInputs maps each port to its payload for the INPUT variable, so a script
// on a node with several inputs can reach the ones PrimaryInput did not pick.
func namedInputs(inputs map[string]*nodeapi.Msg) map[string]any {
	out := make(map[string]any, len(inputs))
	for port, msg := range inputs {
		if msg == nil {
			continue
		}
		out[port] = msg.Payload
	}
	return out
}

// payloadJSON encodes the message payload for stdin, null when there is none.
func payloadJSON(msg *nodeapi.Msg) []byte {
	if msg == nil || msg.Payload == nil {
		return []byte("null")
	}
	enc, err := json.Marshal(msg.Payload)
	if err != nil {
		return []byte("null")
	}
	return enc
}

// jsonString encodes a value to a JSON string, empty object on failure.
func jsonString(v any) string {
	if v == nil {
		return "{}"
	}
	enc, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(enc)
}
