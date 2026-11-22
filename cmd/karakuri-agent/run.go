package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent"
	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/agent/identity"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/internal/update"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

// RunCmd connects to the karakuri server and serves this agent's nodes until
// interrupted. Everything it needs is in config.AC; the command itself takes no
// arguments of its own.
type RunCmd struct{}

// Run loads the stored identity and serves the agent.
func (c *RunCmd) Run(cli *CLI) error {
	cfg := config.AC
	id, _, _, err := identity.Load(cfg.Dir)
	if err != nil {
		return err
	}

	// The stored address is what enrollment agreed on; a configured one
	// overrides it, which is how an agent follows a server that moved.
	server := id.Server
	if cfg.Server != "" {
		server = cfg.Server
	}
	if server == "" {
		return fmt.Errorf("no server address; pass --server")
	}

	log, err := Logger()
	if err != nil {
		return err
	}
	defer func() { _ = log.Sync() }()

	// A version installed by an automatic update gets a few starts to stay
	// up. Past them its predecessor is back in place, and exiting lets the
	// service manager start it.
	pending, err := update.Recover(cfg.Dir, version.Version)
	if err != nil {
		log.Warn("could not read the pending update record", zap.Error(err))
	}
	if pending.RolledBack {
		log.Error("the updated version failed to start; restored the previous version",
			zap.String("failed", pending.To), zap.String("restored", pending.From))
		_ = log.Sync()
		os.Exit(updateRestartCode)
	}

	// A system agent brings its own credential provider: the DLL is
	// embedded, written beside the binary when its content differs, and
	// registered with the logon screen. Nothing to download, nothing to
	// install by hand; a user-scope agent skips it, and a failure is a log
	// line rather than a failed start, since the typed unlock works without
	// the provider.
	if status, err := automation.EnsureUnlockProvider(); err != nil {
		log.Warn("credential provider install failed", zap.Error(err))
	} else if status != "" {
		log.Info(status)
	}

	// The stored CA is the whole check, and the leaf it signed is issued to
	// ca.ServerName, so the agent can be pointed at a new address without a
	// certificate that names it. The identity is read per dial, so a renewal
	// takes effect on the next reconnection.
	tlsCfg := func() (*tls.Config, error) {
		_, cert, caPool, err := identity.Load(cfg.Dir)
		if err != nil {
			return nil, err
		}
		return &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{cert},
			RootCAs:      caPool,
			ServerName:   ca.ServerName,
		}, nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Launched by a service manager rather than a terminal: hand the process to
	// the service wrapper so a stop request cancels the same context an
	// interrupt does. Only an install reads the scope, so what it resolves to
	// here has no bearing on a process the manager has already started.
	def, err := agentServiceDefinition("auto", "")
	if err != nil {
		return err
	}
	if err := service.Attach(def, stop); err != nil {
		return err
	}

	// Keep the certificate and trust bundle current for as long as this runs.
	go identity.RenewLoop(ctx, cfg.Dir, server, log)

	log.Info("starting agent",
		zap.String("agent", id.AgentName),
		zap.String("id", id.AgentID),
		zap.String("server", server))

	plug, err := agent.New(agentnode.Options{
		ExecTimeout: cfg.Timeout,
		MaxOutput:   cfg.MaxOutput,
		Dir:         cfg.Dir,
		Logf:        func(format string, args ...any) { log.Info(fmt.Sprintf(format, args...)) },
		Credentials: credentialResolver(server, cfg.Dir),
	}, cfg.Disable)
	if err != nil {
		return err
	}
	if plug.Disabled() > 0 {
		log.Info("node types withheld by configuration",
			zap.Strings("disabled", cfg.Disable),
			zap.Strings("serving", plug.EnabledNames()))
	}

	// A minute of running without crashing is the healthy start an update
	// waits for; reaching the server is not required, since it may be down.
	if pending.To == version.Version {
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(time.Minute):
				if err := update.Confirm(cfg.Dir, version.Version); err != nil {
					log.Warn("could not confirm the update", zap.Error(err))
					return
				}
				log.Info("updated", zap.String("from", pending.From), zap.String("to", pending.To))
			}
		}()
	}

	// Check for updates daily when a service manager is there to start the
	// version an update installs.
	var restarting atomic.Bool
	if cfg.AutoUpdate && service.Managed() {
		go update.Loop(ctx, func(ctx context.Context) {
			if autoUpdate(ctx, log) {
				restarting.Store(true)
				stop()
			}
		})
	}

	err = sdk.ServeAgent(ctx, sdk.AgentTransport{
		ServerAddr: server,
		TLSConfig:  tlsCfg,
		Logger:     log,
	}, plug)
	if restarting.Load() {
		log.Info("exiting for the service manager to start the updated version")
		_ = log.Sync()
		os.Exit(updateRestartCode)
	}
	if err != nil && err != context.Canceled {
		return err
	}
	return nil
}

// updateRestartCode is the exit status after an automatic update or a
// rollback. It is non-zero because the Windows service manager and scheduler
// restart only a process that failed; systemd and launchd restart it either
// way.
const updateRestartCode = 3
