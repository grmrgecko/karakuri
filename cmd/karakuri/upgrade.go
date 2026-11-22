package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/backup"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// upgradeStart describes how the release now starting relates to the one
// that last ran on the database. It is filled in before migrations, when the
// event log is not running yet, and reported once the server serves.
type upgradeStart struct {
	// From is the release that last ran, when it is older than this one.
	From string
	// Backup is where the copy taken before migrating was written, if any.
	Backup string
	// BackupErr is why no copy was taken, when one was wanted.
	BackupErr error
	// Downgrade is set when an older release starts by allow_downgrade.
	Downgrade string
}

// startUpgrade is this process's upgradeStart.
var startUpgrade upgradeStart

// startLog is where the start-up checks log. The server sets it before it
// opens the database; the command-line tools leave it quiet.
var startLog = zap.NewNop()

// preUpdateMarker records the copy taken for one upgrade, so a release that
// fails and is restarted by its service manager copies the database once,
// not on every attempt.
const preUpdateMarker = "pre-update-backup.json"

// preUpdateCopies is how many SQLite copies taken before upgrades are kept.
const preUpdateCopies = 3

// preUpdateRecord is the content of the marker.
type preUpdateRecord struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	Path string    `json:"path"`
	At   time.Time `json:"at"`
}

// prepareUpgrade runs before migrations. A release older than the one that
// last ran refuses to start, since the newer release may have changed data
// it cannot read; a newer one copies the database first, so the upgrade can
// be undone. dataDir is the server's data directory.
func prepareUpgrade(log *zap.Logger, dataDir string) error {
	prev, err := migrate.StoredVersion()
	if err != nil {
		return err
	}
	cmp, ok := migrate.CompareVersions(prev, version.Version)
	if !ok || cmp == 0 {
		return nil
	}

	// Check for an older release.
	if cmp > 0 {
		if !config.C.Update.AllowDowngrade {
			return fmt.Errorf("this database was last used by karakuri %s, which is newer than this %s; "+
				"run %s or newer, restore a backup taken before the upgrade, or set update.allow_downgrade to start anyway",
				prev, version.Version, prev)
		}
		log.Warn("starting an older release on a database a newer one has used",
			zap.String("database", prev), zap.String("running", version.Version))
		startUpgrade.Downgrade = prev
		return nil
	}

	// Copy the database before this release migrates it.
	startUpgrade.From = prev
	path, err := preUpdateBackup(log, dataDir, prev)
	startUpgrade.Backup, startUpgrade.BackupErr = path, err
	if err != nil {
		log.Error("could not copy the database before upgrading", zap.String("from", prev), zap.Error(err))
	} else if path != "" {
		log.Info("copied the database before upgrading", zap.String("from", prev), zap.String("path", path))
	}
	return nil
}

// errNoBackupMethod reports an installation with no way to copy its database
// unattended.
var errNoBackupMethod = errors.New("no scheduled backup passphrase is set, so no archive could be written; set up scheduled backups to have one taken before each upgrade")

// preUpdateBackup copies the database before an upgrade from prev. With a
// scheduled backup passphrase stored it writes a full archive into the backup
// directory, stamped with prev, so either release can restore it. Without
// one, a SQLite database is copied beside itself; other databases are not
// copied. It returns the copy's path.
func preUpdateBackup(log *zap.Logger, dataDir, prev string) (string, error) {
	var err error
	if rec, rerr := readPreUpdateMarker(dataDir); rerr == nil && rec.From == prev && rec.To == version.Version {
		log.Debug("database already copied for this upgrade", zap.String("path", rec.Path))
		return rec.Path, nil
	}

	var path string
	passphrase, perr := backup.StoredPassphrase(dataDir, config.C.Security.EncryptionKey)
	switch {
	case perr == nil:
		path, err = preUpdateArchive(dataDir, prev, passphrase)
	case db.DB.Name() == "sqlite":
		path, err = preUpdateSQLiteCopy(dataDir, prev)
	case errors.Is(perr, backup.ErrNoPassphrase):
		return "", errNoBackupMethod
	default:
		return "", perr
	}
	if err != nil {
		return "", err
	}

	rec := preUpdateRecord{From: prev, To: version.Version, Path: path, At: time.Now().UTC()}
	if err := writePreUpdateMarker(dataDir, rec); err != nil {
		log.Warn("could not record the pre-update copy", zap.Error(err))
	}
	return path, nil
}

// preUpdateArchive writes and verifies a backup archive of the database as
// the previous release left it.
func preUpdateArchive(dataDir, prev, passphrase string) (string, error) {
	dir := backup.DefaultDirectory(dataDir)
	if s, err := backup.LoadSchedule(dataDir); err == nil && s.Directory != "" {
		dir = s.Directory
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the backup directory: %w", err)
	}
	src := backupSource()
	src.DataDir = dataDir
	src.Version = prev
	path := filepath.Join(dir, backup.ArchiveName(src.Installation, time.Now()))

	// Write to a temporary name, so a failed copy never looks like an archive.
	tmp := path + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	_, werr := backup.Write(context.Background(), f, src, passphrase, dir)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("writing the archive: %w", werr)
	}
	if _, err := backup.Verify(tmp, passphrase); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("verifying the archive: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// preUpdateSQLiteCopy copies a SQLite database into the data directory's
// pre-update folder with VACUUM INTO, which writes one consistent copy, and
// keeps the newest few.
func preUpdateSQLiteCopy(dataDir, prev string) (string, error) {
	dir := filepath.Join(dataDir, "pre-update")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("karakuri-%s-%s.db", strings.TrimPrefix(prev, "v"), time.Now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, name)
	if err := db.DB.Exec("VACUUM INTO ?", path).Error; err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("copying the database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}

	// Keep the newest copies; the names sort by time within one release.
	entries, err := filepath.Glob(filepath.Join(dir, "karakuri-*.db"))
	if err == nil && len(entries) > preUpdateCopies {
		sort.Slice(entries, func(i, j int) bool { return modTime(entries[i]).Before(modTime(entries[j])) })
		for _, old := range entries[:len(entries)-preUpdateCopies] {
			_ = os.Remove(old)
		}
	}
	return path, nil
}

// modTime returns a file's modification time, or the zero time.
func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func readPreUpdateMarker(dataDir string) (preUpdateRecord, error) {
	var rec preUpdateRecord
	data, err := os.ReadFile(filepath.Join(dataDir, preUpdateMarker))
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(data, &rec)
}

func writePreUpdateMarker(dataDir string, rec preUpdateRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, preUpdateMarker), data, 0o600)
}

// recordStart runs once the server serves. It records this release as the
// one that last ran and reports a version change in the event log, whichever
// way the new release arrived: its own updater, a package, or a new image.
func recordStart(log *zap.Logger) {
	if err := migrate.RecordVersion(version.Version); err != nil {
		log.Warn("could not record the running version", zap.Error(err))
	}
	subject := events.Subject{Kind: "server", Name: "karakuri"}

	switch {
	case startUpgrade.From != "":
		msg := "Updated from " + startUpgrade.From + " to " + version.Version
		if startUpgrade.Backup != "" {
			msg += "; the database was copied first to " + startUpgrade.Backup
		}
		log.Info("updated", zap.String("from", startUpgrade.From), zap.String("to", version.Version))
		events.Record(events.Entry{Category: models.EventSystem, Subject: subject, Message: msg})
		if startUpgrade.BackupErr != nil {
			events.Record(events.Entry{Category: models.EventSystem, Level: models.EventWarning, Subject: subject,
				Message: "The database was not copied before updating to " + version.Version + ": " + startUpgrade.BackupErr.Error()})
		}
	case startUpgrade.Downgrade != "":
		events.Record(events.Entry{Category: models.EventSystem, Level: models.EventWarning, Subject: subject,
			Message: "Started " + version.Version + " on a database last used by " + startUpgrade.Downgrade + ", as update.allow_downgrade permits"})
	}
}
