package main

import (
	"fmt"
	"runtime"

	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/internal/version"
)

// ServiceCmd installs and controls karakuri as a service.
type ServiceCmd struct {
	Action string `arg:"" enum:"${serviceActions}" help:"One of: ${serviceActions}."`
	Scope  string `enum:"auto,user,system" default:"auto" help:"Install into the calling user's own service manager (user: a systemd --user unit, a launchd LaunchAgent, or a Windows logon scheduled task), the system's (system), or whichever the installing account can write (auto: root and administrators get the system's, anyone else their own)."`
	User   string `help:"Account a system service runs as; defaults to karakuri, the account the installer creates. Ignored for a user service."`
}

// Run dispatches the requested action to the service manager.
func (c *ServiceCmd) Run() error {
	userService, err := service.UserScope(c.Scope, runtime.GOOS, service.Privileged())
	if err != nil {
		return err
	}
	// A system unit with no User= runs the server as root, which is never what
	// an install meant; the installer creates a service account for it.
	if !userService && c.User == "" {
		c.User = version.Name
	}
	def := serviceDefinition(userService, c.User)

	// A system action from an unelevated Windows prompt reruns this command
	// through UAC; when that happened, the answer is the child's.
	if handled, err := service.ElevateIfNeeded(c.Action, userService); handled {
		return err
	}

	if err := service.Control(def, c.Action); err != nil {
		return err
	}

	switch c.Action {
	case "install":
		if hint := service.LingerHint(def, runtime.GOOS); hint != "" {
			fmt.Println(hint)
		}
		// A system install's files belong to root, so the server's account
		// cannot update them; a root timer does it instead.
		if !userService && service.TimersSupported() {
			if err := service.InstallTimer(updateTimer); err != nil {
				return fmt.Errorf("installing %s.timer: %w", updateTimer.Name, err)
			}
			fmt.Printf("%s.timer installs new releases when update.enabled is set.\n", updateTimer.Name)
		}
	case "uninstall":
		if !userService {
			if err := service.UninstallTimer(updateTimer.Name); err != nil {
				return fmt.Errorf("removing %s.timer: %w", updateTimer.Name, err)
			}
		}
	}
	return nil
}

// serviceDefinition describes the installed service. It runs `serve` with no
// arguments of its own: the server finds its configuration on the same search
// path whichever way it was started, and the account it runs as is what decides
// which copy it finds. A user-scope install is how a person runs their own
// server out of their home directory, where the binary and its resources sit
// together and the search never leaves them.
func serviceDefinition(userService bool, userName string) service.Definition {
	// network-online is wanted rather than merely ordered because the first
	// thing the engine does is dial brokers and Home Assistant. A user manager
	// has no such target, and a session starts long after the network either
	// way, so the ordering is only written into a system unit.
	var deps []string
	if !userService {
		deps = []string{"After=network-online.target", "Wants=network-online.target"}
	}

	return service.Definition{
		Name:         version.Name,
		DisplayName:  "Karakuri",
		Description:  "Karakuri home automation flow engine and dashboard",
		Arguments:    []string{"serve"},
		Dependencies: deps,
		UserName:     userName,
		UserService:  userService,
		LimitNOFILE:  65535,
		Notify:       true,
	}
}

// stopChan tells a running serve command to shut down when the service manager
// asks the service to stop. It is buffered so the manager's stop callback never
// blocks on a serve loop that has already exited on a signal.
var stopChan = make(chan struct{}, 1)

// signalStop unblocks serve's wait, and drops the request when one is already
// pending.
func signalStop() {
	select {
	case stopChan <- struct{}{}:
	default:
	}
}
