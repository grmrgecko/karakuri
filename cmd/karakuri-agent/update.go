package main

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/update"
	"github.com/grmrgecko/karakuri/internal/version"
)

// UpdateCmd checks for a newer agent release and installs it over this binary.
type UpdateCmd struct {
	Check      bool `help:"Only report whether a newer release exists."`
	Prerelease bool `help:"Also consider releases marked as pre-releases."`
}

// Run checks, and unless --check installs, then says how to start the new
// version.
func (c *UpdateCmd) Run(cli *CLI) error {
	ctx := context.Background()
	opts := agentUpdateOptions()
	opts.Prerelease = opts.Prerelease || c.Prerelease
	rel, err := update.Check(ctx, opts, update.Agent())
	if err != nil {
		return err
	}
	if rel == nil {
		fmt.Printf("karakuri-agent %s is the latest release\n", version.Version)
		return nil
	}
	fmt.Printf("karakuri-agent %s is available (running %s): %s\n", rel.Version, version.Version, rel.URL)
	if c.Check {
		return nil
	}
	if err := update.Apply(ctx, opts, update.Agent(), rel); err != nil {
		if errors.Is(err, update.ErrNotWritable) {
			return fmt.Errorf("%w; run the update as the account that installed the agent", err)
		}
		return err
	}
	fmt.Printf("installed karakuri-agent %s; restart the agent to run it: karakuri-agent service restart\n", rel.Version)
	return nil
}

// agentUpdateOptions are the update options this agent runs with. The
// pending-update record lives in the identity directory, which the agent's
// account always owns.
func agentUpdateOptions() update.Options {
	return update.Options{
		Current:    version.Version,
		Prerelease: config.AC.UpdatePrerelease,
		StateDir:   config.AC.Dir,
	}
}

// autoUpdate is one automatic update check, reporting whether a new version
// was installed. A binary the agent's account cannot write is logged and left
// alone, which is what an agent installed by root and run as a user sees.
func autoUpdate(ctx context.Context, log *zap.Logger) bool {
	opts := agentUpdateOptions()
	rel, err := update.Check(ctx, opts, update.Agent())
	if err != nil {
		log.Warn("update check failed", zap.Error(err))
		return false
	}
	if rel == nil {
		log.Debug("no newer release", zap.String("version", version.Version))
		return false
	}
	if err := update.Writable(update.Agent()); err != nil {
		log.Warn("a newer release is available but this agent cannot update itself",
			zap.String("release", rel.Version), zap.Error(err))
		return false
	}
	log.Info("installing update", zap.String("from", version.Version), zap.String("to", rel.Version))
	if err := update.Apply(ctx, opts, update.Agent(), rel); err != nil {
		log.Error("update failed", zap.String("release", rel.Version), zap.Error(err))
		return false
	}
	return true
}
