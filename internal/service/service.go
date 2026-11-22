// Package service installs and controls the two binaries as services of
// whatever the host runs: systemd, launchd, OpenRC, SysV, or the Windows
// service manager. Both mains need the same three things: a Definition of the
// service they install for themselves, one dispatcher for the operator-facing
// actions, and a way to hand a running process to the manager that started it,
// so keeping them here is what stops the server and the agent describing the
// same thing two ways.
package service

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	kservice "github.com/kardianos/service"
)

// Actions are the actions a service command accepts, in the order an operator
// meets them. The list is also the kong enum both binaries declare, so a name
// added here is accepted by both without touching either command.
var Actions = []string{"install", "uninstall", "start", "stop", "restart", "status"}

// Definition is one binary's description of the service it installs for
// itself. Everything platform-specific is derived from it below, so a caller
// names what the service is rather than how each manager spells it.
type Definition struct {
	// Name is the service and unit name, no spaces.
	Name string
	// DisplayName and Description are what a service manager shows.
	DisplayName string
	// Description describes the service to the operator.
	Description string
	// Arguments are appended to the executable path in the installed unit.
	// Pass whatever the process needs resolved at install time rather than
	// rediscovered later: a service account has neither the installing user's
	// home directory nor, usually, their configuration file.
	Arguments []string
	// Dependencies are systemd [Unit] lines, written verbatim.
	Dependencies []string
	// UserName is the account a system service runs as. Empty runs it as
	// whatever the manager's default is, which is root under systemd.
	UserName string
	// UserService installs into the calling user's own service manager
	// instead of the system's: a systemd --user unit, a launchd LaunchAgent,
	// or on Windows a logon scheduled task. The process then runs in that
	// user's session, which is the only place the agent's desktop and GUI
	// nodes can reach a screen.
	UserService bool
	// LimitNOFILE raises the descriptor limit of a system service. Zero leaves
	// the manager's default, which is 1024 soft under systemd. That is enough
	// for a process that is not holding a socket per dashboard.
	LimitNOFILE int
	// Notify marks the systemd unit Type=notify, so systemd holds dependent
	// units until the process calls NotifyReady. Nothing else reads it.
	Notify bool
}

// New builds the service from a definition. The stop callback is what a
// manager's stop request unwinds through; it may be nil for a command that
// only installs or controls a service rather than being one.
func New(def Definition, stop func()) (kservice.Service, error) {
	options := kservice.KeyValue{
		"SystemdScript": systemdScript(def),
		"Restart":       "always",
		// launchd would otherwise install the agent and leave it stopped
		// until the next login, and Windows has no restart policy of its own.
		"RunAtLoad":              true,
		"OnFailure":              "restart",
		"OnFailureDelayDuration": "5s",
	}
	if def.UserService {
		options["UserService"] = true
	}
	if def.LimitNOFILE > 0 {
		options["LimitNOFILE"] = def.LimitNOFILE
	}

	return kservice.New(handler{stop: stop}, &kservice.Config{
		Name:         def.Name,
		DisplayName:  def.DisplayName,
		Description:  def.Description,
		Arguments:    def.Arguments,
		Dependencies: def.Dependencies,
		UserName:     def.UserName,
		Option:       options,
	})
}

// Control performs one operator-facing action and reports the result on
// stdout, which is the whole of a `service` subcommand.
func Control(def Definition, action string) error {
	// An elevated relaunch arrives with a console of its own; put the output
	// back on the one the operator is watching before anything prints.
	adoptCallerConsole()

	// A Windows user-scope install is a logon scheduled task, which the
	// service library knows nothing about.
	if taskScoped(def) {
		return userTaskControl(def, action)
	}

	svc, err := New(def, nil)
	if err != nil {
		return err
	}

	switch action {
	case "install":
		err = svc.Install()
	case "uninstall":
		err = svc.Uninstall()
	case "start":
		err = svc.Start()
	case "stop":
		err = svc.Stop()
	case "restart":
		err = svc.Restart()
	case "status":
		return status(def, svc)
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s %s complete.\n", def.Name, action)
	return nil
}

// status prints what the service manager reports. A service that was never
// installed is an answer rather than a failure: asking is how an operator
// finds out.
func status(def Definition, svc kservice.Service) error {
	state, err := svc.Status()
	switch {
	case errors.Is(err, kservice.ErrNotInstalled):
		fmt.Printf("%s is not installed.\n", def.Name)
		return nil
	case err != nil:
		return err
	}

	switch state {
	case kservice.StatusRunning:
		fmt.Printf("%s is running.\n", def.Name)
	case kservice.StatusStopped:
		fmt.Printf("%s is stopped.\n", def.Name)
	default:
		fmt.Printf("%s is in an unknown state.\n", def.Name)
	}
	return nil
}

// Attach hands a running process to the service manager that started it, so a
// stop request unwinds through the same path a termination signal takes. It is
// a no-op in a terminal, which is what makes it safe to call unconditionally
// at the top of a long-running command.
func Attach(def Definition, stop func()) error {
	// A scheduled-task launch on Windows gets a console window of its own;
	// drop it before the desktop shows it.
	hideOwnConsole()

	if kservice.Interactive() {
		return nil
	}

	svc, err := New(def, stop)
	if err != nil {
		return err
	}

	// Run blocks for the life of the service: on Windows it is the control
	// dispatcher the manager expects the process to register, and elsewhere it
	// waits for the signal the command is already handling itself.
	go func() { _ = svc.Run() }()
	return nil
}

// Managed reports whether a service manager started this process, the only
// case in which exiting brings it back: an automatic update relies on that
// restart to start the version it installed.
func Managed() bool {
	return !kservice.Interactive()
}

// NotifyReady tells systemd the process finished starting, which is what a
// Definition with Notify set promised the unit. It is a no-op when
// NOTIFY_SOCKET is unset, which is every case but a systemd-started process,
// so a caller can report the failure and carry on.
func NotifyReady() error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}

	// A socket in the abstract namespace is spelled with a leading '@' in the
	// environment and a NUL byte on the wire.
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}

	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Write([]byte("READY=1"))
	return err
}

// handler adapts the service manager's own lifecycle to a command that is
// already running the process. Start has nothing to do because the command
// itself is the service.
type handler struct{ stop func() }

// Start satisfies the service interface.
func (h handler) Start(kservice.Service) error { return nil }

// Stop unblocks the command that is running.
func (h handler) Stop(kservice.Service) error {
	if h.stop != nil {
		h.stop()
	}
	return nil
}

// systemdScript renders the unit template. It is the library's own default
// with four changes: a start rate limit that tolerates a brief flap, a
// five-second restart delay instead of two minutes, no EnvironmentFile drop-in
// (karakuri is configured by its own file), and the Type and install target
// the definition implies. The syntax is the library's mini template engine
// rather than text/template: keys are bare, and only the keys and pipeline
// functions of its default template exist here.
func systemdScript(def Definition) string {
	unitType := "simple"
	if def.Notify {
		unitType = "notify"
	}

	// A user manager has no multi-user.target to hang off; default.target is
	// what it reaches on login, and a unit wanted by anything else is enabled
	// and then never started.
	wantedBy := "multi-user.target"
	if def.UserService {
		wantedBy = "default.target"
	}

	return `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
{{range Dependencies}}{{.}}
{{end}}StartLimitIntervalSec=500
StartLimitBurst=5

[Service]
Type=` + unitType + `
ExecStart={{Path | cmdEscape}}{{range Arguments}} {{. | cmd}}{{end}}
{{if ChRoot}}RootDirectory={{ChRoot | cmd}}
{{end}}{{if WorkingDirectory}}WorkingDirectory={{WorkingDirectory | cmdEscape}}
{{end}}{{if UserName}}User={{UserName}}
{{end}}{{if ReloadSignal}}ExecReload=/bin/kill -{{ReloadSignal}} "$MAINPID"
{{end}}{{if PIDFile}}PIDFile={{PIDFile | cmd}}
{{end}}{{if OutputFileSupport}}StandardOutput=file:{{LogDirectory}}/{{Name}}.out
StandardError=file:{{LogDirectory}}/{{Name}}.err
{{end}}{{if LimitNOFILE}}LimitNOFILE={{LimitNOFILE}}
{{end}}{{if Restart}}Restart={{Restart}}
{{end}}{{if SuccessExitStatus}}SuccessExitStatus={{SuccessExitStatus}}
{{end}}RestartSec=5

{{range EnvVars}}{{.}}
{{end}}[Install]
WantedBy=` + wantedBy + `
`
}

// Handle controls an installed service without printing anything, for a
// caller that acts on the result, such as an update that restarts the server
// and watches it come up.
type Handle struct{ svc kservice.Service }

// Open returns a handle on the service a definition describes.
func Open(def Definition) (Handle, error) {
	if taskScoped(def) {
		return Handle{}, errors.New("a logon task cannot be restarted by another process; use the service command")
	}
	svc, err := New(def, nil)
	return Handle{svc: svc}, err
}

// Restart restarts the service. A systemd unit with Notify set returns only
// once the process reports ready, and fails if it exits first.
func (h Handle) Restart() error { return h.svc.Restart() }

// Installed reports whether the service manager knows the service.
func (h Handle) Installed() bool {
	_, err := h.svc.Status()
	return !errors.Is(err, kservice.ErrNotInstalled)
}

// Running reports whether the service manager reports the service running.
func (h Handle) Running() (bool, error) {
	state, err := h.svc.Status()
	if err != nil {
		return false, err
	}
	return state == kservice.StatusRunning, nil
}
