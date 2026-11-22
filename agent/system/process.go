package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Signals the node sends, named for what they do rather than for their
// numbers. Windows has no signal table: gopsutil turns terminate and kill into
// TerminateProcess there and cannot deliver the other two, which the node says
// rather than pretending to send them.
const (
	signalTerminate = "terminate"
	signalKill      = "kill"
	signalInterrupt = "interrupt"
	signalHangup    = "hangup"
)

// How a target is named: a pid a previous node reported, or a pattern matched
// against the process table.
const (
	targetPID  = "pid"
	targetName = "name"
)

// processListNode reports the processes running on the agent host. Whether a
// program is running at all is the common question, so the payload leads with
// the count and a flow can switch on it without touching the list.
type processListNode struct{}

func (processListNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeProcessList,
		Folder:            folder,
		Label:             "Process list",
		Description:       "Lists processes on this agent host, optionally matching a pattern",
		Icon:              "las la-microchip",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "pattern",
				Label:       "Match pattern",
				Type:        flow.FieldString,
				Placeholder: "plex|jellyfin",
				Help:        "Go regular expression matched against the process name. Empty lists everything.",
			},
			{
				ID:      "match_cmdline",
				Label:   "Match the whole command line",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Matches the pattern against the arguments too, which is how an interpreter " +
					"running a named script is found. Costs a read per process rather than per match.",
			},
			{
				ID:          "top",
				Label:       "Heaviest only",
				Type:        flow.FieldNumber,
				Placeholder: "10",
				Help:        "Keep only this many processes, the ones using the most CPU. Empty keeps all matches.",
			},
		},
	}
}

// processListConfig is the decoded process configuration.
type processListConfig struct {
	Pattern      string `json:"pattern"`
	MatchCmdline bool   `json:"match_cmdline"`
	Top          int    `json:"top"`
}

// processWatchNode starts a run when a matching process appears or exits.
type processWatchNode struct{}

func (processWatchNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type: nodeProcessWatch, Folder: folder, Label: "Watch processes",
		Description: "Starts a run when a matching process starts or stops on this agent host",
		Icon:        "las la-eye", Trigger: true, TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []nodeapi.FieldSpec{
			{ID: "pattern", Label: "Match pattern", Type: flow.FieldString, Required: true, Placeholder: "^jellyfin$",
				Help: "Go regular expression matched against the process name."},
			{ID: "match_cmdline", Label: "Match the whole command line", Type: flow.FieldBoolean, Default: false},
			{ID: "on_start", Label: "On start", Type: flow.FieldBoolean, Default: true},
			{ID: "on_stop", Label: "On stop", Type: flow.FieldBoolean, Default: true},
			{ID: "emit_initial", Label: "Report processes already running", Type: flow.FieldBoolean, Default: false},
			{ID: "poll", Label: "Poll interval", Type: flow.FieldDuration, Default: "2s", Placeholder: "2s"},
		},
	}
}

type processWatchConfig struct {
	Pattern      string `json:"pattern"`
	MatchCmdline bool   `json:"match_cmdline"`
	OnStart      *bool  `json:"on_start"`
	OnStop       *bool  `json:"on_stop"`
	EmitInitial  bool   `json:"emit_initial"`
	Poll         string `json:"poll"`
}

type watchedProcess struct {
	PID     int32
	Name    string
	Cmdline string
	Started int64
}

func (processWatchNode) Execute(_ context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	msg := inputs[nodeapi.TriggerInput]
	if msg != nil && msg.Payload != nil {
		return nodeapi.One("out", msg), nil
	}
	sample := processWatchMsg("start", watchedProcess{PID: 1234, Name: "example", Cmdline: "/usr/bin/example", Started: time.Now().UnixMilli()})
	if msg != nil {
		sample.ID, sample.Group = msg.ID, msg.Group
	}
	return nodeapi.One("out", sample), nil
}

func (processWatchNode) Start(ctx context.Context, ec nodeapi.ExecContext, emit func(*nodeapi.Msg)) error {
	var cfg processWatchConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	pattern, err := regexp.Compile(strings.TrimSpace(cfg.Pattern))
	if err != nil {
		return fmt.Errorf("invalid match pattern %q: %w", cfg.Pattern, err)
	}
	poll := 2 * time.Second
	if raw := strings.TrimSpace(cfg.Poll); raw != "" {
		poll, err = time.ParseDuration(raw)
		if err != nil || poll <= 0 {
			return fmt.Errorf("invalid poll interval %q", cfg.Poll)
		}
	}
	onStart := cfg.OnStart == nil || *cfg.OnStart
	onStop := cfg.OnStop == nil || *cfg.OnStop
	previous, err := watchedProcesses(ctx, pattern, cfg.MatchCmdline)
	if err != nil {
		return err
	}
	if cfg.EmitInitial && onStart {
		for _, proc := range previous {
			emit(processWatchMsg("start", proc))
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ec.Clock().After(poll):
		}
		current, err := watchedProcesses(ctx, pattern, cfg.MatchCmdline)
		if err != nil {
			ec.Logger().Warn("process watch poll failed", zap.Error(err))
			continue
		}
		if onStart {
			for key, proc := range current {
				if _, existed := previous[key]; !existed {
					emit(processWatchMsg("start", proc))
				}
			}
		}
		if onStop {
			for key, proc := range previous {
				if _, exists := current[key]; !exists {
					emit(processWatchMsg("stop", proc))
				}
			}
		}
		previous = current
	}
}

func watchedProcesses(ctx context.Context, pattern *regexp.Regexp, matchCmdline bool) (map[string]watchedProcess, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}
	out := make(map[string]watchedProcess)
	for _, proc := range procs {
		name, err := proc.NameWithContext(ctx)
		if err != nil {
			continue
		}
		cmdline := ""
		matched := pattern.MatchString(name)
		if matchCmdline {
			cmdline, _ = proc.CmdlineWithContext(ctx)
			matched = matched || pattern.MatchString(cmdline)
		}
		if !matched {
			continue
		}
		started, _ := proc.CreateTimeWithContext(ctx)
		item := watchedProcess{PID: proc.Pid, Name: name, Cmdline: cmdline, Started: started}
		out[fmt.Sprintf("%d:%d", proc.Pid, started)] = item
	}
	return out, nil
}

func processWatchMsg(event string, proc watchedProcess) *nodeapi.Msg {
	payload := map[string]any{"event": event, "pid": proc.PID, "name": proc.Name, "cmdline": proc.Cmdline}
	if proc.Started > 0 {
		payload["started"] = time.UnixMilli(proc.Started).UTC().Format(time.RFC3339)
	}
	return &nodeapi.Msg{Payload: payload, Topic: proc.Name, Meta: map[string]any{"source": "process-watch", "event": event}}
}

// Execute walks the process table once and reports what matched.
func (processListNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg processListConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	var pattern *regexp.Regexp
	if raw := strings.TrimSpace(cfg.Pattern); raw != "" {
		compiled, err := regexp.Compile(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid match pattern %q: %w", raw, err)
		}
		pattern = compiled
	}

	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	// The state of every process is read once here rather than per process,
	// which on macOS is the difference between one ps and one per row.
	states := newStatusTable(ctx)

	matched := make([]map[string]any, 0, 16)
	for _, proc := range procs {
		// Match on the name first: it is one small read, while the command
		// line, the user, and the memory figures are several each, and a
		// pattern usually rejects most of the table.
		name, err := proc.NameWithContext(ctx)
		if err != nil {
			continue
		}
		cmdline := ""
		if cfg.MatchCmdline {
			cmdline, _ = proc.CmdlineWithContext(ctx)
		}
		if pattern != nil && !pattern.MatchString(name) && !(cfg.MatchCmdline && pattern.MatchString(cmdline)) {
			continue
		}
		if !cfg.MatchCmdline {
			cmdline, _ = proc.CmdlineWithContext(ctx)
		}
		matched = append(matched, processPayload(ctx, states, proc, name, cmdline))
	}

	// Sort by CPU so "heaviest only" has something to cut, and so an
	// unfiltered list arrives in a useful order rather than by pid.
	sort.SliceStable(matched, func(i, j int) bool {
		return toFloat(matched[i]["cpu_percent"]) > toFloat(matched[j]["cpu_percent"])
	})
	total := len(matched)
	if cfg.Top > 0 && len(matched) > cfg.Top {
		matched = matched[:cfg.Top]
	}

	processes := make([]any, 0, len(matched))
	for _, p := range matched {
		processes = append(processes, p)
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: map[string]any{
			"count":     total,
			"running":   total > 0,
			"processes": processes,
		},
		Meta: map[string]any{"source": "process-list", "pattern": cfg.Pattern},
	}), nil
}

// processPayload shapes one process. cpu_percent is the share of one core the
// process has used since it started, not an instantaneous reading: a single
// visit to the process table is a lifetime average, and the alternative is
// sampling every process twice for a number nothing asked to be live.
func processPayload(ctx context.Context, states statusTable, proc *process.Process, name, cmdline string) map[string]any {
	out := map[string]any{
		"pid":     proc.Pid,
		"name":    name,
		"cmdline": cmdline,
	}
	if username, err := proc.UsernameWithContext(ctx); err == nil {
		out["username"] = username
	}
	if status, err := states.status(ctx, proc); err == nil && len(status) > 0 {
		out["status"] = strings.Join(status, ",")
	}
	if created, err := proc.CreateTimeWithContext(ctx); err == nil && created > 0 {
		out["started"] = time.UnixMilli(created).UTC().Format(time.RFC3339)
	}
	if percent, err := proc.CPUPercentWithContext(ctx); err == nil {
		out["cpu_percent"] = round2(percent)
	}
	if percent, err := proc.MemoryPercentWithContext(ctx); err == nil {
		out["memory_percent"] = round2(float64(percent))
	}
	if info, err := proc.MemoryInfoWithContext(ctx); err == nil && info != nil {
		out["memory_rss"] = info.RSS
	}
	return out
}

// processSignalNode signals processes on the agent host. It is the counterpart
// to the process list, which only reads: restarting a stuck program is the
// thing an operator wants a flow to do once it has noticed.
type processSignalNode struct{}

func (processSignalNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:        nodeProcessSignal,
		Folder:      folder,
		Label:       "Signal process",
		Description: "Terminates or signals processes on this agent host, by pid or by name",
		Icon:        "las la-skull-crossbones",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "target",
				Label:   "Target",
				Type:    flow.FieldSelect,
				Default: targetName,
				Options: []nodeapi.Option{
					{Value: targetName, Label: "Every process matching a pattern"},
					{Value: targetPID, Label: "One process id"},
				},
			},
			{
				ID:          "pid",
				Label:       "Process id",
				Type:        flow.FieldNumber,
				ShowIf:      &nodeapi.ShowIf{Field: "target", Equals: targetPID},
				Placeholder: "1234",
				Help:        "Bind this to a process list entry over a value edge, or read it from the message.",
			},
			{
				ID:          "pattern",
				Label:       "Match pattern",
				Type:        flow.FieldString,
				ShowIf:      &nodeapi.ShowIf{Field: "target", Equals: targetName},
				Placeholder: "^plexmediaserver$",
				Help: "Go regular expression matched against the process name. Every match is " +
					"signalled, so anchor it unless you mean the whole family.",
			},
			{
				ID:      "match_cmdline",
				Label:   "Match the whole command line",
				Type:    flow.FieldBoolean,
				Default: false,
				ShowIf:  &nodeapi.ShowIf{Field: "target", Equals: targetName},
				Help:    "Matches the arguments too, which is how an interpreter running a named script is found.",
			},
			{
				ID:      "signal",
				Label:   "Signal",
				Type:    flow.FieldSelect,
				Default: signalTerminate,
				Options: []nodeapi.Option{
					{Value: signalTerminate, Label: "Terminate (ask it to exit)"},
					{Value: signalKill, Label: "Kill (no chance to clean up)"},
					{Value: signalInterrupt, Label: "Interrupt"},
					{Value: signalHangup, Label: "Hang up (reload, for most daemons)"},
				},
			},
			{
				ID:      "require_match",
				Label:   "Fail when nothing matched",
				Type:    flow.FieldBoolean,
				Default: true,
				Help: "A flow restarting a service usually wants to hear that it was not running. " +
					"Turn this off to make signalling nothing a success.",
			},
		},
	}
}

// processSignalConfig is the decoded signal configuration.
type processSignalConfig struct {
	Target       string `json:"target"`
	PID          int32  `json:"pid"`
	Pattern      string `json:"pattern"`
	MatchCmdline bool   `json:"match_cmdline"`
	Signal       string `json:"signal"`
	RequireMatch *bool  `json:"require_match"`
}

// Execute signals every process the configuration selects.
func (processSignalNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg processSignalConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)

	targets, err := signalTargets(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		if cfg.RequireMatch == nil || *cfg.RequireMatch {
			return nil, fmt.Errorf("no process matched %s", describeTarget(cfg))
		}
		out := agentnode.PassThrough(msg, map[string]any{"signalled": []any{}, "count": 0, "signal": cfg.Signal})
		return nodeapi.One("out", out), nil
	}

	signalled := make([]any, 0, len(targets))
	var failures []string
	for _, target := range targets {
		if err := sendSignal(ctx, target.proc, cfg.Signal); err != nil {
			failures = append(failures, fmt.Sprintf("%d (%s): %v", target.pid, target.name, err))
			continue
		}
		ec.Logger().Info("signalled process",
			zap.Int32("pid", target.pid), zap.String("name", target.name), zap.String("signal", cfg.Signal))
		signalled = append(signalled, map[string]any{"pid": target.pid, "name": target.name})
	}

	// A partial failure is a failure: a flow restarting a service cannot act on
	// "some of them stopped", and the ones that did are named in the error.
	if len(failures) > 0 {
		completed := make([]string, 0, len(signalled))
		for _, value := range signalled {
			item := value.(map[string]any)
			completed = append(completed, fmt.Sprintf("%v (%v)", item["pid"], item["name"]))
		}
		if len(completed) > 0 {
			return nil, fmt.Errorf("cannot signal all of %s; signalled %s; failed %s",
				describeTarget(cfg), strings.Join(completed, ", "), strings.Join(failures, "; "))
		}
		return nil, fmt.Errorf("cannot signal %s: %s", describeTarget(cfg), strings.Join(failures, "; "))
	}

	out := agentnode.PassThrough(msg, map[string]any{
		"signalled": signalled,
		"count":     len(signalled),
		"signal":    cfg.Signal,
	})
	return nodeapi.One("out", out), nil
}

// signalTarget is one selected process, carrying the name for the report so it
// is read once rather than after the process is already gone.
type signalTarget struct {
	pid  int32
	name string
	proc *process.Process
}

// signalTargets resolves the configuration into the processes to signal. The
// agent's own process is never a target: an agent whose node killed it would
// take the run, the report, and the connection home with it, and a pattern
// broad enough to match the agent is exactly the kind that would.
func signalTargets(ctx context.Context, cfg processSignalConfig) ([]signalTarget, error) {
	self := int32(os.Getpid())

	if cfg.Target == targetPID {
		if cfg.PID <= 0 {
			return nil, fmt.Errorf("no process id configured")
		}
		if cfg.PID == self {
			return nil, fmt.Errorf("pid %d is the agent itself", cfg.PID)
		}
		// A pid that no longer exists selects nothing, so require_match decides
		// the outcome as it does for a pattern.
		proc, err := process.NewProcessWithContext(ctx, cfg.PID)
		if errors.Is(err, process.ErrorProcessNotRunning) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("checking pid %d: %w", cfg.PID, err)
		}
		name, _ := proc.NameWithContext(ctx)
		return []signalTarget{{pid: cfg.PID, name: name, proc: proc}}, nil
	}

	raw := strings.TrimSpace(cfg.Pattern)
	if raw == "" {
		return nil, fmt.Errorf("no match pattern configured")
	}
	pattern, err := regexp.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid match pattern %q: %w", raw, err)
	}

	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}
	var targets []signalTarget
	for _, proc := range procs {
		if proc.Pid == self {
			continue
		}
		name, err := proc.NameWithContext(ctx)
		if err != nil {
			continue
		}
		matched := pattern.MatchString(name)
		if !matched && cfg.MatchCmdline {
			cmdline, _ := proc.CmdlineWithContext(ctx)
			matched = cmdline != "" && pattern.MatchString(cmdline)
		}
		if !matched {
			continue
		}
		targets = append(targets, signalTarget{pid: proc.Pid, name: name, proc: proc})
	}
	return targets, nil
}

// sendSignal delivers one signal, mapping the node's names onto what gopsutil
// can do on this platform.
func sendSignal(ctx context.Context, proc *process.Process, signal string) error {
	switch signal {
	case "", signalTerminate:
		return proc.TerminateWithContext(ctx)
	case signalKill:
		return proc.KillWithContext(ctx)
	case signalInterrupt, signalHangup:
		return sendNamedSignal(ctx, proc, signal)
	default:
		return fmt.Errorf("unknown signal %q", signal)
	}
}

// describeTarget names what was being signalled, for an error a flow author
// reads without opening the node.
func describeTarget(cfg processSignalConfig) string {
	if cfg.Target == targetPID {
		return fmt.Sprintf("pid %d", cfg.PID)
	}
	return fmt.Sprintf("pattern %q", cfg.Pattern)
}
