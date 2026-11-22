package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/internal/update"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// UpdateCmd checks for a newer release and installs it over this install.
type UpdateCmd struct {
	Check      bool `help:"Only report whether a newer release exists."`
	Prerelease bool `help:"Also consider releases marked as pre-releases."`
	Scheduled  bool `help:"Do nothing unless update.enabled is set, as the update timer runs it."`
	Force      bool `help:"Download the latest release and replace this install with it even when it is not newer, such as to repair an install."`
}

// updateTimer is the root timer a system install updates from. The server's
// own account cannot write a system install, so the timer runs this command
// as root once a day.
var updateTimer = service.Timer{
	Name:               "karakuri-update",
	Description:        "Install newer Karakuri releases",
	Arguments:          []string{"update", "--scheduled"},
	OnCalendar:         "daily",
	RandomizedDelaySec: "4h",
}

// Run checks, and unless --check installs. With the service installed it
// restarts the service on the new version and puts the old one back if it
// does not start; otherwise the next start of the server confirms it.
func (c *UpdateCmd) Run() error {
	ctx := context.Background()
	if c.Scheduled && !config.C.Update.Enabled {
		fmt.Println("update.enabled is off; not checking for releases")
		return nil
	}
	opts := serverUpdateOptions()
	opts.Prerelease = opts.Prerelease || c.Prerelease
	// The timer never forces: a daily reinstall of the same release would
	// restart the service for nothing.
	opts.Force = c.Force && !c.Scheduled
	artifact := serverArtifact()

	// The record of a supervised update sits with the files it replaces,
	// where only the account doing the update can write, since a rollback
	// renames the paths it names.
	svc, svcErr := updateService()
	supervised := svcErr == nil && svc.Installed()
	if supervised {
		opts.StateDir = artifact.ResourceRoot
		rec, err := update.Resume(opts.StateDir, svc)
		if err != nil {
			return fmt.Errorf("settling the update to %s: %w", rec.To, err)
		}
		switch {
		case rec.RolledBack:
			fmt.Printf("karakuri %s was not running after its update, so %s was put back\n", rec.To, rec.From)
		case rec.To != "":
			fmt.Printf("confirmed the update from %s to %s\n", rec.From, rec.To)
		}
	}

	rel, err := update.Check(ctx, opts, artifact)
	if err != nil {
		return err
	}
	if rel == nil {
		fmt.Printf("karakuri %s is the latest release\n", version.Version)
		return nil
	}
	if cmp, ok := migrate.CompareVersions(rel.Version, version.Version); opts.Force && ok && cmp <= 0 {
		fmt.Printf("karakuri %s is the latest release; reinstalling it over %s: %s\n", rel.Version, version.Version, rel.URL)
	} else {
		fmt.Printf("karakuri %s is available (running %s): %s\n", rel.Version, version.Version, rel.URL)
	}
	if c.Check {
		return nil
	}

	if !supervised {
		if err := update.Apply(ctx, opts, artifact, rel); err != nil {
			if errors.Is(err, update.ErrNotWritable) {
				return fmt.Errorf("%w; update as the account that owns the install", err)
			}
			return err
		}
		fmt.Printf("installed karakuri %s; it runs from the next start\n", rel.Version)
		return nil
	}
	fmt.Printf("installing karakuri %s and restarting the service\n", rel.Version)
	if err := update.Supervise(ctx, opts, artifact, rel, svc); err != nil {
		if errors.Is(err, update.ErrNotWritable) {
			return fmt.Errorf("%w; for a system install, run sudo karakuri update", err)
		}
		return err
	}
	fmt.Printf("karakuri %s is running\n", rel.Version)
	return nil
}

// updateService is the installed service this command restarts: the
// system's when run as root, the calling user's otherwise, as `karakuri
// service` decides.
func updateService() (service.Handle, error) {
	userService, err := service.UserScope("auto", runtime.GOOS, service.Privileged())
	if err != nil {
		return service.Handle{}, err
	}
	return service.Open(serviceDefinition(userService, version.Name))
}

// serverUpdateOptions are the update options this server runs with. The
// pending-update record lives in the data directory, beside the database.
func serverUpdateOptions() update.Options {
	return update.Options{
		Current:    version.Version,
		Prerelease: config.C.Update.Prerelease,
		StateDir:   resources.GetDataPath(),
	}
}

// serverArtifact is this install's server artifact: the binary, and the
// plugins, themes, and runtimes an install put down with it. Those sit beside
// the binary in an unpacked archive, in the user data directory for a user
// install, and in the system resource directory for a system install. The
// configuration directories can hold plugins too, but those are a person's
// own additions and an update never replaces them.
func serverArtifact() update.Artifact {
	roots := []string{resources.BinDir()}
	if dir, ok := resources.UserDataDir(); ok {
		roots = append(roots, dir)
	}
	roots = append(roots, resources.SystemResourceDir)
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, string(resources.Plugins))); err == nil {
			return update.Server(root)
		}
	}
	return update.Server(resources.BinDir())
}

// autoUpdate is one automatic update check. It reports whether a new version
// was installed, which the caller answers by exiting for the service manager
// to restart. An install the server cannot write is logged and left alone.
func autoUpdate(ctx context.Context, log *zap.Logger) bool {
	opts := serverUpdateOptions()
	artifact := serverArtifact()

	// A system install is updated by its root timer, outside this process.
	writeErr := update.Writable(artifact)
	if writeErr != nil && service.TimerInstalled(updateTimer.Name) {
		log.Debug("install is updated by its timer", zap.String("timer", updateTimer.Name+".timer"))
		return false
	}

	rel, err := update.Check(ctx, opts, artifact)
	if err != nil {
		log.Warn("update check failed", zap.Error(err))
		return false
	}
	if rel == nil {
		log.Debug("no newer release", zap.String("version", version.Version))
		return false
	}
	if writeErr != nil {
		log.Warn("a newer release is available but this install cannot update itself; run sudo karakuri update, or reinstall the service to add its update timer",
			zap.String("release", rel.Version), zap.Error(writeErr))
		reportAvailable(log, rel.Version)
		return false
	}
	log.Info("installing update", zap.String("from", version.Version), zap.String("to", rel.Version))
	if err := update.Apply(ctx, opts, artifact, rel); err != nil {
		log.Error("update failed", zap.String("release", rel.Version), zap.Error(err))
		events.Record(events.Entry{Category: models.EventSystem, Level: models.EventError,
			Subject: events.Subject{Kind: "server", Name: "karakuri"},
			Message: "Update to " + rel.Version + " failed: " + err.Error()})
		return false
	}
	events.Record(events.Entry{Category: models.EventSystem, Subject: events.Subject{Kind: "server", Name: "karakuri"},
		Message: "Installed " + rel.Version + "; restarting to run it"})
	return true
}

// notifyUpdate is one check on a server that does not install releases
// itself, such as a container, which is updated by pulling a newer image. A
// newer release is reported, not installed.
func notifyUpdate(ctx context.Context, log *zap.Logger) {
	rel, err := update.Check(ctx, serverUpdateOptions(), serverArtifact())
	if err != nil {
		log.Warn("update check failed", zap.Error(err))
		return
	}
	if rel == nil {
		log.Debug("no newer release", zap.String("version", version.Version))
		return
	}
	reportAvailable(log, rel.Version)
}

// reportAvailable records a newer release for the overview and, the first
// time it is found, in the event log.
func reportAvailable(log *zap.Logger, release string) {
	if s, err := models.GetSetting(migrate.AvailableSetting); err == nil && s.Value == release {
		return
	}
	if err := models.SetSetting(migrate.AvailableSetting, release); err != nil {
		log.Warn("could not record the available release", zap.Error(err))
	}
	log.Info("a newer release is available", zap.String("release", release), zap.String("running", version.Version))
	events.Record(events.Entry{Category: models.EventSystem, Subject: events.Subject{Kind: "server", Name: "karakuri"},
		Message: "Karakuri " + release + " is available; this server runs " + version.Version})
}
