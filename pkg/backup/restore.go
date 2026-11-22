package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/instlock"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// RestoreOptions describes one restore into a stopped installation.
type RestoreOptions struct {
	Archive    string
	Passphrase string
	// Replace allows restoring over an installation that holds data; its
	// current state is backed up beside the archive first.
	Replace bool

	DataDir string
	// Database is the target's own database settings. For SQLite, DSN is the
	// database file path.
	Database db.Options
	// Version is this binary's version.
	Version string
	// Plugins are the external plugins installed on this machine.
	Plugins []Plugin
	// StageSecrets prepares the target's config file with the archive's
	// secrets and answers how to commit it, how to discard it, and the keys it
	// sets. Commit keeps a copy of the replaced file and returns its path.
	StageSecrets func(Secrets) (commit func() (string, error), discard func(), keys []string, err error)
	// Current describes the installation as it is now, for the safety backup
	// Replace writes.
	Current func(gdb *gorm.DB) Source
}

// RestoreReport says what a restore did.
type RestoreReport struct {
	Manifest       *Manifest
	Loaded         map[string]int64
	Directories    []string
	KeptAside      []string
	ConfigKeys     []string
	SavedConfig    string
	SafetyArchive  string
	MissingPlugins []Plugin
	Warnings       []string
}

// ErrPopulated reports a target that already holds data.
var ErrPopulated = errors.New("the installation already holds data")

// undo is a list of steps that reverse what a restore did so far.
type undo []func() error

func (u *undo) push(fn func() error) { *u = append(*u, fn) }

// run reverses the steps newest first, returning the first failure.
func (u undo) run() error {
	var first error
	for i := len(u) - 1; i >= 0; i-- {
		if err := u[i](); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Restore puts an archive in place of a stopped installation's database,
// data directories, and config secrets. Everything is unpacked and checked
// before anything changes, and a failure at any later step reverses what was
// done, so the installation is either restored or as it was. Nothing is
// deleted: replaced directories, the SQLite file, and the config are kept
// beside the originals with a .pre-restore-<stamp> suffix.
func Restore(ctx context.Context, opts RestoreOptions) (*RestoreReport, error) {
	release, err := instlock.Acquire(opts.DataDir)
	if err != nil {
		if errors.Is(err, instlock.ErrHeld) {
			return nil, errors.New("the server is running; stop it with systemctl stop karakuri first")
		}
		return nil, err
	}
	defer release()

	stamp := time.Now().UTC().Format(stampLayout)
	aside := ".pre-restore-" + stamp
	report := &RestoreReport{}

	// Unpack and check the whole archive first.
	stage := filepath.Join(filepath.Dir(filepath.Clean(opts.DataDir)), "."+filepath.Base(opts.DataDir)+".restore-"+stamp)
	m, err := unpack(opts.Archive, opts.Passphrase, stage)
	if err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := checkFileRoots(m.FileRoots); err != nil {
		return nil, err
	}
	report.Manifest = m
	if warning, err := checkVersion(m.Version, opts.Version); err != nil {
		return nil, err
	} else if warning != "" {
		report.Warnings = append(report.Warnings, warning)
	}
	var secrets Secrets
	if data, err := os.ReadFile(filepath.Join(stage, "secrets.json")); err != nil {
		return nil, fmt.Errorf("the archive has no secrets: %w", err)
	} else if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("reading the archive's secrets: %w", err)
	}

	// Refuse a populated target unless asked to replace it, and back up what
	// is about to be replaced.
	populated, users, flows, err := targetPopulated(opts.Database)
	if err != nil {
		return nil, err
	}
	if populated && !opts.Replace {
		return nil, fmt.Errorf("%w: it has %d users and %d flows; pass --replace to back it up and replace it", ErrPopulated, users, flows)
	}
	if populated {
		path, err := safetyBackup(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("backing up the current installation before replacing it: %w", err)
		}
		report.SafetyArchive = path
	}

	// Stage the config before touching anything, so an unwritable config
	// refuses now.
	commitConfig, discardConfig, keys, err := opts.StageSecrets(secrets)
	if err != nil {
		return nil, fmt.Errorf("preparing the config file: %w", err)
	}
	defer discardConfig()
	report.ConfigKeys = keys

	// Prepare the database without replacing the live one yet.
	commitDB, err := prepareDatabase(ctx, opts, stage, aside, report)
	if err != nil {
		return nil, err
	}

	// Swap the data directories. Every swap is a rename that can be reversed.
	var steps undo
	fail := func(err error) (*RestoreReport, error) {
		if uerr := steps.run(); uerr != nil {
			return nil, fmt.Errorf("%w; undoing the restore also failed, so check %s for directories ending %s: %v", err, opts.DataDir, aside, uerr)
		}
		return nil, fmt.Errorf("%w; the installation was left as it was", err)
	}
	for _, d := range restoreDirs(stage, opts.DataDir, m) {
		moved, placed, err := swapDir(d.staged, d.live, aside, &steps)
		if err != nil {
			return fail(fmt.Errorf("putting %s in place: %w", d.live, err))
		}
		if moved {
			report.KeptAside = append(report.KeptAside, d.live+aside)
		}
		if placed {
			report.Directories = append(report.Directories, d.live)
		}
	}

	if err := commitDB(&steps); err != nil {
		return fail(fmt.Errorf("putting the database in place: %w", err))
	}

	saved, err := commitConfig()
	if err != nil {
		return fail(fmt.Errorf("writing the config file: %w", err))
	}
	report.SavedConfig = saved
	markRestored(opts, m, populated)

	present := map[string]bool{}
	for _, p := range opts.Plugins {
		present[p.Name] = true
	}
	for _, p := range m.Plugins {
		if !present[p.Name] {
			report.MissingPlugins = append(report.MissingPlugins, p)
		}
	}
	return report, nil
}

// unpack checks an archive and writes its parts under stage. The manifest
// check runs after the last part, so a damaged archive leaves only a staging
// directory to remove.
func unpack(archive, passphrase, stage string) (*Manifest, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, err
	}
	m, err := walkArchive(f, passphrase, func(e Entry) error {
		path := filepath.Join(stage, filepath.FromSlash(e.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("unpacking %s: %w", e.Name, err)
		}
		_, err = io.Copy(out, e.R)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("unpacking %s: %w", e.Name, err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrPassphrase) {
			return nil, err
		}
		return nil, fmt.Errorf("the archive cannot be restored: %w", err)
	}
	return m, nil
}

// checkVersion refuses an archive from a newer release. A development build
// on either side has no order, so it only warns.
func checkVersion(archive, binary string) (string, error) {
	a, aok := parseVersion(archive)
	b, bok := parseVersion(binary)
	if !aok || !bok {
		return fmt.Sprintf("the archive is from karakuri %s and this is %s; versions without a release number are not compared", archive, binary), nil
	}
	for i := range a {
		if a[i] > b[i] {
			return "", fmt.Errorf("the archive is from karakuri %s; this is %s; upgrade before restoring", archive, binary)
		}
		if a[i] < b[i] {
			break
		}
	}
	return "", nil
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// targetPopulated reports whether the target database has users, and how
// many users and flows. A SQLite file that does not exist is empty and is not
// created by asking.
func targetPopulated(opts db.Options) (bool, int64, int64, error) {
	if opts.Type == "sqlite" {
		if _, err := os.Stat(opts.DSN); errors.Is(err, os.ErrNotExist) {
			return false, 0, 0, nil
		}
	}
	gdb, err := db.Open(opts)
	if err != nil {
		return false, 0, 0, err
	}
	defer closeDB(gdb)
	if !gdb.Migrator().HasTable(&models.User{}) {
		return false, 0, 0, nil
	}
	var users, flows int64
	if err := gdb.Model(&models.User{}).Count(&users).Error; err != nil {
		return false, 0, 0, err
	}
	if gdb.Migrator().HasTable(&models.Flow{}) {
		gdb.Model(&models.Flow{}).Count(&flows)
	}
	return users > 0, users, flows, nil
}

func closeDB(gdb *gorm.DB) {
	if sqlDB, err := gdb.DB(); err == nil {
		sqlDB.Close()
	}
}

// safetyBackup writes the current installation beside the archive under the
// same passphrase and verifies it.
func safetyBackup(ctx context.Context, opts RestoreOptions) (string, error) {
	gdb, err := db.Open(opts.Database)
	if err != nil {
		return "", err
	}
	defer closeDB(gdb)
	src := opts.Current(gdb)
	dir := filepath.Dir(opts.Archive)
	name := ArchiveName(src.Installation, time.Now())
	path := filepath.Join(dir, name)
	partial := partialPath(dir, name)
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, err = Write(ctx, f, src, opts.Passphrase, dir)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(partial, path)
	}
	if err != nil {
		os.Remove(partial)
		return "", err
	}
	if _, err := Verify(path, opts.Passphrase); err != nil {
		return "", fmt.Errorf("the safety backup did not verify: %w", err)
	}
	return path, nil
}

// prepareDatabase loads the archive's rows without replacing the live
// database and returns the step that puts them in place.
//
// Any model tables already there are dropped first, not only a populated
// installation's: a server that started once has seeded roles, groups, and
// settings without a user, and the archive's rows would collide with them.
//
// SQLite loads into a new file renamed over the live one at commit.
// PostgreSQL loads inside one transaction, its DDL included, committed at
// commit. MySQL commits DDL as it goes, so it loads at commit, and a failure
// drops what it created and reloads the safety backup.
func prepareDatabase(ctx context.Context, opts RestoreOptions, stage, aside string, report *RestoreReport) (func(*undo) error, error) {
	switch opts.Database.Type {
	case "sqlite":
		return prepareSQLite(ctx, opts, stage, aside, report)
	case "postgres":
		gdb, err := db.Open(opts.Database)
		if err != nil {
			return nil, err
		}
		tx := gdb.WithContext(ctx).Begin()
		if tx.Error != nil {
			closeDB(gdb)
			return nil, tx.Error
		}
		if err := dropModels(tx); err != nil {
			tx.Rollback()
			closeDB(gdb)
			return nil, err
		}
		loaded, err := loadDatabase(ctx, tx, stage)
		if err != nil {
			tx.Rollback()
			closeDB(gdb)
			return nil, err
		}
		report.Loaded = loaded
		return func(*undo) error {
			defer closeDB(gdb)
			return tx.Commit().Error
		}, nil
	case "mysql":
		return func(*undo) error {
			gdb, err := db.Open(opts.Database)
			if err != nil {
				return err
			}
			defer closeDB(gdb)
			if err := dropModels(gdb); err != nil {
				return err
			}
			loaded, err := loadDatabase(ctx, gdb, stage)
			if err == nil {
				report.Loaded = loaded
				return nil
			}
			if derr := dropModels(gdb); derr != nil {
				return fmt.Errorf("%w; dropping the partial load also failed: %v", err, derr)
			}
			if report.SafetyArchive != "" {
				if rerr := reloadArchive(ctx, gdb, report.SafetyArchive, opts.Passphrase, stage+".safety"); rerr != nil {
					return fmt.Errorf("%w; reloading %s also failed: %v", err, report.SafetyArchive, rerr)
				}
			}
			return err
		}, nil
	default:
		return nil, fmt.Errorf("unsupported database type: %s", opts.Database.Type)
	}
}

// prepareSQLite loads into <path>.restoring and returns the rename that puts
// it in place, keeping the live file as <path>.pre-restore-<stamp>.
func prepareSQLite(ctx context.Context, opts RestoreOptions, stage, aside string, report *RestoreReport) (func(*undo) error, error) {
	path := opts.Database.DSN
	restoring := path + ".restoring"
	removeSQLite(restoring)
	newOpts := opts.Database
	newOpts.DSN = restoring
	gdb, err := db.Open(newOpts)
	if err != nil {
		return nil, err
	}
	loaded, err := loadDatabase(ctx, gdb, stage)
	if err == nil {
		err = gdb.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error
	}
	closeDB(gdb)
	if err != nil {
		removeSQLite(restoring)
		return nil, err
	}
	report.Loaded = loaded
	return func(steps *undo) error {
		if _, err := os.Stat(path); err == nil {
			// Fold the live file's WAL into it so the copy kept aside is
			// whole on its own.
			if live, err := db.Open(opts.Database); err == nil {
				live.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
				closeDB(live)
			}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				from, to := path+suffix, path+aside+suffix
				if _, err := os.Stat(from); err != nil {
					continue
				}
				if err := os.Rename(from, to); err != nil {
					return err
				}
				steps.push(func() error { return os.Rename(to, from) })
			}
			report.KeptAside = append(report.KeptAside, path+aside)
		}
		os.Remove(restoring + "-wal")
		os.Remove(restoring + "-shm")
		if err := os.Rename(restoring, path); err != nil {
			removeSQLite(restoring)
			return err
		}
		steps.push(func() error { return os.Rename(path, restoring) })
		return nil
	}, nil
}

func removeSQLite(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(path + suffix)
	}
}

// dropModels drops every model table, children first.
func dropModels(gdb *gorm.DB) error {
	order, err := loadOrder(gdb)
	if err != nil {
		return err
	}
	for i := len(order) - 1; i >= 0; i-- {
		if gdb.Migrator().HasTable(order[i].model) {
			if err := gdb.Migrator().DropTable(order[i].model); err != nil {
				return fmt.Errorf("dropping %s: %w", order[i].schema.Table, err)
			}
		}
	}
	return nil
}

// reloadArchive loads another archive's database into gdb.
func reloadArchive(ctx context.Context, gdb *gorm.DB, archive, passphrase, stage string) error {
	defer os.RemoveAll(stage)
	if _, err := unpack(archive, passphrase, stage); err != nil {
		return err
	}
	_, err := loadDatabase(ctx, gdb, stage)
	return err
}

// checkFileRoots refuses manifest file roots a restore must not move: one
// that leaves the data directory, or one that is or sits inside a directory
// karakuri archives as its own part. The manifest is not checked like the
// tar entry names, and each root names a live directory moved aside.
func checkFileRoots(roots []string) error {
	for _, root := range roots {
		if err := safeName(root); err != nil {
			return fmt.Errorf("the archive's flow file root %q is not allowed", root)
		}
		top := strings.SplitN(root, "/", 2)[0]
		if slices.Contains(reservedDirs, top) {
			return fmt.Errorf("the archive's flow file root %q overlaps karakuri's own %s directory", root, top)
		}
	}
	return nil
}

// restoreDir is one directory a restore puts in place.
type restoreDir struct {
	staged string
	live   string
}

// restoreDirs lists the directories the restore replaces: every archived
// data part, and every flow files root the archive holds, at its path under
// the data directory.
func restoreDirs(stage, dataDir string, m *Manifest) []restoreDir {
	var out []restoreDir
	for _, part := range dataParts {
		out = append(out, restoreDir{staged: filepath.Join(stage, part), live: filepath.Join(dataDir, part)})
	}
	for _, root := range m.FileRoots {
		out = append(out, restoreDir{
			staged: filepath.Join(stage, "files", filepath.FromSlash(root)),
			live:   filepath.Join(dataDir, filepath.FromSlash(root)),
		})
	}
	return out
}

// swapDir puts staged in place of live, moving a live directory aside first.
// A staged directory the archive did not have leaves live moved aside and
// nothing in its place, so the restored state is the archive's. It reports
// whether something was moved aside and whether something was put in place.
func swapDir(staged, live, aside string, steps *undo) (bool, bool, error) {
	moved := false
	if _, err := os.Lstat(live); err == nil {
		if err := os.Rename(live, live+aside); err != nil {
			return false, false, err
		}
		moved = true
		steps.push(func() error { return os.Rename(live+aside, live) })
	}
	if _, err := os.Stat(staged); err != nil {
		return moved, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(live), 0o750); err != nil {
		return moved, false, err
	}
	if err := os.Rename(staged, live); err != nil {
		return moved, false, err
	}
	steps.push(func() error { return os.Rename(live, staged) })
	return moved, true, nil
}

// RestoredSetting is where a restore leaves word of itself for the next
// start to record in the security log, since no server is running to hear
// it.
const RestoredSetting = "backup.restored"

// Restored describes the restore the next start records.
type Restored struct {
	Archive    string    `json:"archive"`
	Version    string    `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	RestoredAt time.Time `json:"restored_at"`
	OSUser     string    `json:"os_user"`
	Replaced   bool      `json:"replaced"`
}

// markRestored writes the restore marker into the restored database. A
// failure only loses the log entry, so it is not a restore failure.
func markRestored(opts RestoreOptions, m *Manifest, replaced bool) {
	gdb, err := db.Open(opts.Database)
	if err != nil {
		return
	}
	defer closeDB(gdb)
	osUser := os.Getenv("USER")
	if osUser == "" {
		osUser = os.Getenv("USERNAME")
	}
	raw, err := json.Marshal(Restored{Archive: filepath.Base(opts.Archive), Version: m.Version, CreatedAt: m.CreatedAt,
		RestoredAt: time.Now().UTC(), OSUser: osUser, Replaced: replaced})
	if err != nil {
		return
	}
	_ = models.SetSettingIn(gdb, RestoredSetting, string(raw))
}
