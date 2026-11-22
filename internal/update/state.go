package update

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

// stateFile names the record of an update awaiting confirmation.
const stateFile = "update-pending.json"

// maxAttempts is how many starts the new version gets to report healthy
// before the replaced files are put back. A service manager restarts a
// crashing process every few seconds, so three attempts is well under a
// minute of a broken install.
const maxAttempts = 3

// resource is one replaced resource directory.
type resource struct {
	Path    string `json:"path"`
	Backup  string `json:"backup"`
	Existed bool   `json:"existed"`
}

// state records an applied update until the new version confirms it.
type state struct {
	From         string     `json:"from"`
	To           string     `json:"to"`
	Binary       string     `json:"binary"`
	BinaryBackup string     `json:"binary_backup"`
	Resources    []resource `json:"resources,omitempty"`
	Attempts     int        `json:"attempts"`
	At           time.Time  `json:"at"`
}

func (s *state) save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, stateFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, stateFile))
}

func load(dir string) (*state, error) {
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// restore puts every replaced file back. On Windows a running binary cannot
// be overwritten but can be renamed, so the new one is moved aside first.
func (s *state) restore() {
	for i := len(s.Resources) - 1; i >= 0; i-- {
		r := s.Resources[i]
		_ = os.RemoveAll(r.Path)
		if r.Existed {
			_ = os.Rename(r.Backup, r.Path)
		}
	}
	if s.BinaryBackup != "" {
		if _, err := os.Stat(s.BinaryBackup); err == nil {
			failed := s.Binary + ".failed"
			_ = os.Remove(failed)
			_ = os.Rename(s.Binary, failed)
			if err := os.Rename(s.BinaryBackup, s.Binary); err == nil {
				_ = os.Remove(failed)
			}
		}
	}
}

// discard removes the backups once the new version is confirmed.
func (s *state) discard() {
	for _, r := range s.Resources {
		if r.Existed {
			_ = os.RemoveAll(r.Backup)
		}
	}
	if s.BinaryBackup != "" {
		_ = os.Remove(s.BinaryBackup)
	}
}

// Recovery is what Recover found at startup.
type Recovery struct {
	// From and To are the update's versions, empty when none was pending.
	From, To string
	// RolledBack is set when the new version failed to start too many times
	// and the replaced files were put back. The caller exits so the service
	// manager starts the restored version.
	RolledBack bool
}

// Recover runs at startup. A pending update the running version belongs to
// counts this start; after too many it restores the replaced files. A pending
// update for another version is stale, from a swap that never took effect,
// and is dropped.
func Recover(dir, current string) (Recovery, error) {
	s, err := load(dir)
	if err != nil || s == nil {
		return Recovery{}, err
	}
	rec := Recovery{From: s.From, To: s.To}
	path := filepath.Join(dir, stateFile)
	if current != s.To {
		s.discard()
		return rec, os.Remove(path)
	}
	s.Attempts++
	if s.Attempts > maxAttempts {
		s.restore()
		rec.RolledBack = true
		return rec, os.Remove(path)
	}
	return rec, s.save(dir)
}

// Confirm marks the running version healthy: the backups of the files it
// replaced are removed and the record is cleared.
func Confirm(dir, current string) error {
	s, err := load(dir)
	if err != nil || s == nil || s.To != current {
		return err
	}
	s.discard()
	return os.Remove(filepath.Join(dir, stateFile))
}

// Loop calls check once a day, first between ten minutes and an hour after
// start, with a few hours of jitter each day so a fleet of installs does not
// ask GitHub at the same moment. It returns when ctx ends.
func Loop(ctx context.Context, check func(context.Context)) {
	wait := 10*time.Minute + rand.N(50*time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		check(ctx)
		wait = 24*time.Hour + rand.N(4*time.Hour)
	}
}
