package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/grmrgecko/karakuri/agent/identity"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/internal/version"
)

// ServiceCmd installs and controls the agent as a service, so a host that has
// enrolled reconnects on its own after a reboot.
type ServiceCmd struct {
	Action string `arg:"" enum:"${serviceActions}" help:"One of: ${serviceActions}."`
	Scope  string `enum:"auto,user,system" default:"auto" help:"Install into the calling user's own service manager (user: a systemd --user unit, a launchd LaunchAgent, or a Windows logon scheduled task), the system's (system), or whichever the installing account can write (auto: root and administrators get the system's, anyone else their own)."`
	User   string `help:"Account a system service runs as. Ignored for a user service."`
}

// Run dispatches the requested action to the service manager.
func (c *ServiceCmd) Run(cli *CLI) error {
	def, err := agentServiceDefinition(c.Scope, c.User)
	if err != nil {
		return err
	}

	// A system action from an unelevated Windows prompt reruns this command
	// through UAC; when that happened, the answer is the child's. The
	// enrollment warning prints after, so only the process doing the install
	// says it.
	if handled, err := service.ElevateIfNeeded(c.Action, def.UserService); handled {
		return err
	}

	// An unenrolled agent installs fine and then fails at every start, which
	// reads as a broken service rather than a missing step.
	if c.Action == "install" && !identity.IsEnrolled(config.AC.Dir) {
		fmt.Fprintf(os.Stderr, "warning: no identity in %s; the service will not start until `%s-agent enroll` has run\n",
			config.AC.Dir, version.Name)
	}

	// Sign the binary with the host's persistent identity before the service
	// starts it, so on macOS the Accessibility grant the GUI nodes need survives
	// this and every future update. It is a no-op on the other platforms.
	if c.Action == "install" {
		if exe, err := os.Executable(); err == nil {
			if msg := service.EnsureStableSignature(exe); msg != "" {
				fmt.Println(msg)
			}
		}
	}

	if err := service.Control(def, c.Action); err != nil {
		return err
	}

	if c.Action == "install" {
		if hint := service.LingerHint(def, runtime.GOOS); hint != "" {
			fmt.Println(hint)
		}
	}
	return nil
}

// agentServiceDefinition describes the agent's service. The unit carries the
// resolved identity directory and server address rather than leaving them to
// the search that produced them: a service account has neither the enrolling
// user's home directory nor, usually, their agent.yaml, so a unit that only
// said `run` would start an agent looking for an identity somewhere else.
func agentServiceDefinition(scope, userName string) (service.Definition, error) {
	cfg := config.AC

	userService, err := service.UserScope(scope, runtime.GOOS, service.Privileged())
	if err != nil {
		return service.Definition{}, err
	}

	args := []string{"run", "--dir", cfg.Dir}
	if cfg.Server != "" {
		args = append(args, "--server", cfg.Server)
	}

	// A user manager has no network-online.target to order against, and a
	// session starts long after the network either way, so the ordering is
	// only written into a system unit where it means something.
	var deps []string
	if !userService {
		deps = []string{"After=network-online.target", "Wants=network-online.target"}
	}

	return service.Definition{
		Name:         version.Name + "-agent",
		DisplayName:  "Karakuri Agent",
		Description:  "Karakuri remote agent, running flow nodes on this host",
		Arguments:    args,
		Dependencies: deps,
		UserName:     userName,
		UserService:  userService,
	}, nil
}
