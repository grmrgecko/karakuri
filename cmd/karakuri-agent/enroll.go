package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/grmrgecko/karakuri/agent/identity"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/service"
)

// EnrollCmd registers this host with a karakuri server using a join token. The
// address and the identity directory come from config.AC; the token and the
// preferred name are per-enrollment and live here.
type EnrollCmd struct {
	Token string `help:"Join token from the karakuri agents panel: the server's CA and a one-time secret." required:""`
	Name  string `help:"Preferred agent name; the server may adjust it to stay unique."`
	// Service is opt-in rather than the default because enrolling and deciding
	// how the host runs the agent are separate choices: a container or a
	// foreground test wants the identity and nothing else.
	Service bool   `help:"Install and start the agent as a service once enrolled."`
	Scope   string `enum:"auto,user,system" default:"auto" help:"Service manager to install into with --service; see the service command."`
}

// Run performs enrollment and reports what to run next.
func (c *EnrollCmd) Run(cli *CLI) error {
	cfg := config.AC
	// Server is optional on the command line because running falls back to the
	// address enrollment stored; enrolling has nothing to fall back to.
	if cfg.Server == "" {
		return fmt.Errorf("no server address; pass --server")
	}

	// Resolve the service scope before enrolling: a system install this
	// account cannot perform would otherwise fail after the join token was
	// spent. No UAC relaunch here, since re-running the command would enroll
	// twice.
	var def service.Definition
	if c.Service {
		var err error
		def, err = agentServiceDefinition(c.Scope, "")
		if err != nil {
			return err
		}
		if !def.UserService && !service.Privileged() {
			return fmt.Errorf("installing a system service needs an administrator or root prompt; re-run elevated, or pass --scope=user")
		}
	}

	id, err := identity.Enroll(cfg.Dir, cfg.Server, c.Token, c.Name)
	if err != nil {
		return err
	}
	fmt.Printf("enrolled as %q (%s)\n", id.AgentName, id.AgentID)

	// Install and start, so one command takes a host from a token to an agent
	// the server can see. Sign first so the macOS Accessibility grant survives
	// updates (a no-op elsewhere).
	if c.Service {
		if exe, err := os.Executable(); err == nil {
			if msg := service.EnsureStableSignature(exe); msg != "" {
				fmt.Println(msg)
			}
		}
		if err := service.Control(def, "install"); err != nil {
			return err
		}
		return service.Control(def, "start")
	}

	fmt.Printf("run: karakuri-agent run --server %s --dir %s\n", cfg.Server, cfg.Dir)
	if runtime.GOOS == "windows" {
		fmt.Println("or install it: `karakuri-agent service install` registers a logon scheduled task that can reach the desktop; from an administrator prompt it installs a headless system service instead")
	} else {
		fmt.Println("or install it as a service: karakuri-agent service install")
	}
	return nil
}
