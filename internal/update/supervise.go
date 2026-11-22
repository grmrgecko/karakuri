package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Service is the service a supervised update restarts. A system install's
// update runs as root from its own timer, outside the server it replaces, so
// it starts the new version itself and watches it rather than relying on the
// server to confirm: the server's account cannot write the files a rollback
// puts back.
type Service interface {
	// Restart restarts the service. Under systemd the server's unit is
	// Type=notify, so this returns only once the new version is serving,
	// and fails when it exits first.
	Restart() error
	// Running reports whether the service is running now.
	Running() (bool, error)
}

// settleTime is how long a restarted service must keep running before its
// update is confirmed. It catches a version that serves and then crashes,
// which a service manager would otherwise restart in a loop.
var settleTime = 30 * time.Second

// Supervise applies the release, restarts the service, and confirms the
// update once the service has stayed up for settleTime. A service that does
// not come up gets the replaced files back and is restarted on them. The
// record lives in opts.StateDir, which must be writable only by the account
// running the update, since it names the paths a rollback renames.
func Supervise(ctx context.Context, opts Options, a Artifact, rel *Release, svc Service) error {
	if err := Apply(ctx, opts, a, rel); err != nil {
		return err
	}
	if err := healthy(ctx, svc); err != nil {
		// An update interrupted while it waits stays pending for Resume to
		// judge, rather than being undone because the timer was stopped.
		if ctx.Err() != nil {
			return err
		}
		if rerr := rollback(opts.StateDir, svc); rerr != nil {
			return fmt.Errorf("%s did not start (%w), and putting %s back failed: %v", rel.Version, err, opts.Current, rerr)
		}
		return fmt.Errorf("%s did not start, so %s was put back: %w", rel.Version, opts.Current, err)
	}
	return Confirm(opts.StateDir, rel.Version)
}

// Resume settles a supervised update an earlier run left pending, which
// happens when the host rebooted or the run was stopped while it waited. A
// running service confirms it and a stopped one rolls it back.
func Resume(dir string, svc Service) (Recovery, error) {
	s, err := load(dir)
	if err != nil || s == nil {
		return Recovery{}, err
	}
	rec := Recovery{From: s.From, To: s.To}
	if running, err := svc.Running(); err == nil && running {
		return rec, Confirm(dir, s.To)
	}
	rec.RolledBack = true
	return rec, rollback(dir, svc)
}

// healthy restarts the service and waits for it to stay up.
func healthy(ctx context.Context, svc Service) error {
	if err := svc.Restart(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settleTime):
	}
	running, err := svc.Running()
	if err != nil {
		return err
	}
	if !running {
		return errors.New("the service stopped after starting")
	}
	return nil
}

// rollback puts back the files a pending update replaced, clears the record,
// and restarts the service on them.
func rollback(dir string, svc Service) error {
	s, err := load(dir)
	if err != nil || s == nil {
		return err
	}
	s.restore()
	if err := os.Remove(filepath.Join(dir, stateFile)); err != nil {
		return err
	}
	return svc.Restart()
}
