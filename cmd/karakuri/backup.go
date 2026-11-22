package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/backup"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
	"golang.org/x/term"
	"gorm.io/gorm"
)

// passphraseEnv names the environment variable a passphrase may come from.
const passphraseEnv = "KARAKURI_BACKUP_PASSPHRASE"

// BackupCmd groups backup commands.
type BackupCmd struct {
	Create  BackupCreateCmd  `cmd:"" help:"Write a backup archive of this installation."`
	Verify  BackupVerifyCmd  `cmd:"" help:"Check an archive without restoring it."`
	List    BackupListCmd    `cmd:"" help:"List the archives in a backup directory."`
	Restore BackupRestoreCmd `cmd:"" help:"Restore an archive into this installation while the server is stopped."`
}

// passphraseFlags is how every backup command takes a passphrase. Never as an
// argument: other users can read a process's arguments.
type passphraseFlags struct {
	PassphraseFile string `help:"File holding the passphrase. Otherwise KARAKURI_BACKUP_PASSPHRASE or a prompt." placeholder:"FILE"`
}

// read returns the passphrase from the file, the environment, or a prompt,
// asking twice when confirm is set.
func (p passphraseFlags) read(confirm bool) (string, error) {
	if p.PassphraseFile != "" {
		data, err := os.ReadFile(p.PassphraseFile)
		if err != nil {
			return "", fmt.Errorf("reading the passphrase file: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if v := os.Getenv(passphraseEnv); v != "" {
		return v, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("no passphrase: pass --passphrase-file or set %s", passphraseEnv)
	}
	first, err := prompt("Backup passphrase: ")
	if err != nil {
		return "", err
	}
	if confirm {
		second, err := prompt("Repeat the passphrase: ")
		if err != nil {
			return "", err
		}
		if first != second {
			return "", errors.New("the passphrases do not match")
		}
	}
	return first, nil
}

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// backupSource describes this installation for a backup.
func backupSource() backup.Source {
	host, _ := os.Hostname()
	return backup.Source{
		DB:           db.DB,
		DatabaseType: config.C.Database.Type,
		DataDir:      resources.GetDataPath(),
		FileRoots:    config.C.Storage.Roots,
		Secrets:      backup.Secrets{EncryptionKey: config.C.Security.EncryptionKey, JWTSecret: config.C.Security.JWTSecret},
		Plugins:      externalPlugins(),
		Version:      version.Version,
		Installation: backup.InstallationName(host),
	}
}

// externalPlugins lists the external plugins installed for this server. The
// archive names them because their executables are built for one platform
// and are not copied. A directory the scan refuses is left out, as the server
// leaves it out when loading, so the scan's error is not acted on here.
func externalPlugins() []backup.Plugin {
	found, _ := pluginhost.DiscoverExternal(pluginhost.SearchPaths(config.C.Plugins.Paths))
	out := make([]backup.Plugin, 0, len(found))
	for _, m := range found {
		out = append(out, backup.Plugin{Name: m.Name, Version: m.Version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// backupDirectory is the schedule's directory, or the default under the data
// directory.
func backupDirectory() string {
	s, err := backup.LoadSchedule(resources.GetDataPath())
	if err != nil || s.Directory == "" {
		return backup.DefaultDirectory(resources.GetDataPath())
	}
	return s.Directory
}

// BackupCreateCmd writes an archive. It works with the server running: the
// database copy is one read transaction and every other part is saved by
// rename, so the copy is consistent either way.
type BackupCreateCmd struct {
	Output string `help:"Directory to write the archive to. Defaults to the schedule's directory." placeholder:"DIR"`
	passphraseFlags
}

func (c *BackupCreateCmd) Run() error {
	pass, err := c.read(true)
	if err != nil {
		return err
	}
	if err := backup.CheckPassphrase(pass); err != nil {
		return err
	}
	if err := initDatabaseQuiet(); err != nil {
		return err
	}
	dir := c.Output
	if dir == "" {
		dir = backupDirectory()
	}
	svc := backup.NewService(backupSource, nil)
	run, err := svc.Create(context.Background(), backup.Request{Trigger: models.BackupTriggerCLI, Passphrase: pass, Directory: dir})
	if err != nil {
		return err
	}
	fmt.Printf("%s/%s\n", run.Directory, run.Archive)
	fmt.Printf("size: %s\n", humanBytes(run.Size))
	printCounts(run.Counts)
	return nil
}

// BackupVerifyCmd checks an archive and prints its manifest.
type BackupVerifyCmd struct {
	Archive string `arg:"" help:"Archive file." type:"existingfile"`
	passphraseFlags
}

func (c *BackupVerifyCmd) Run() error {
	pass, err := c.read(false)
	if err != nil {
		return err
	}
	m, err := backup.Verify(c.Archive, pass)
	if err != nil {
		return fmt.Errorf("%s cannot be restored: %w", c.Archive, err)
	}
	printManifest(m)
	fmt.Println("ok")
	return nil
}

// BackupListCmd lists archives in a directory.
type BackupListCmd struct {
	Dir string `help:"Directory to list. Defaults to the schedule's directory." placeholder:"DIR"`
}

func (c *BackupListCmd) Run() error {
	if err := initDatabaseQuiet(); err != nil {
		return err
	}
	dir := c.Dir
	if dir == "" {
		dir = backupDirectory()
	}
	list, err := backup.List(dir)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Printf("no archives in %s\n", dir)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ARCHIVE\tCREATED\tSIZE\tVERIFIED")
	for _, a := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.CreatedAt.Local().Format("2006-01-02 15:04:05"), humanBytes(a.Size), a.Verified)
	}
	return w.Flush()
}

func printManifest(m *backup.Manifest) {
	fmt.Printf("version: %s\n", m.Version)
	fmt.Printf("created: %s\n", m.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("installation: %s\n", m.Installation)
	fmt.Printf("database: %s\n", m.Database)
	printCounts(m.Counts)
	if len(m.Plugins) > 0 {
		names := make([]string, 0, len(m.Plugins))
		for _, p := range m.Plugins {
			names = append(names, p.Name+" "+p.Version)
		}
		fmt.Printf("external plugins: %s\n", strings.Join(names, ", "))
	}
	for _, s := range m.Skipped {
		fmt.Printf("not included: %s\n", s)
	}
}

func printCounts(counts map[string]int64) {
	keys := []string{"flows", "dashboards", "connections", "devices", "controls", "users", "agents"}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	fmt.Printf("holds: %s\n", strings.Join(parts, ", "))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// BackupRestoreCmd restores an archive into this installation. It refuses
// while a server holds the installation, and refuses an installation holding
// data unless --replace is given.
type BackupRestoreCmd struct {
	Archive string `arg:"" help:"Archive file." type:"existingfile"`
	Replace bool   `help:"Back up the current installation beside the archive, then replace it."`
	passphraseFlags
}

func (c *BackupRestoreCmd) Run() error {
	pass, err := c.read(false)
	if err != nil {
		return err
	}
	opts := dbOptions()
	opts.LogLevel = "silent"
	if opts.Type == "sqlite" {
		opts.DSN = config.C.Database.Path
	}
	configPath := resources.GetConfigPath()
	report, err := backup.Restore(context.Background(), backup.RestoreOptions{
		Archive:    c.Archive,
		Passphrase: pass,
		Replace:    c.Replace,
		DataDir:    resources.GetDataPath(),
		Database:   opts,
		Version:    version.Version,
		Plugins:    externalPlugins(),
		StageSecrets: func(s backup.Secrets) (func() (string, error), func(), []string, error) {
			return stageRestoredSecrets(configPath, s)
		},
		Current: func(gdb *gorm.DB) backup.Source {
			src := backupSource()
			src.DB = gdb
			return src
		},
	})
	if err != nil {
		return err
	}
	m := report.Manifest
	fmt.Printf("restored %s from karakuri %s, taken %s\n", filepath.Base(c.Archive), m.Version, m.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"))
	for _, w := range report.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	fmt.Printf("database: %s (archive made on %s)\n", opts.Type, m.Database)
	printCounts(m.Counts)
	for _, d := range report.Directories {
		fmt.Printf("restored: %s\n", d)
	}
	fmt.Printf("config: set %s in %s\n", strings.Join(report.ConfigKeys, " and "), configPath)
	if report.SavedConfig != "" {
		fmt.Printf("previous config, with its encryption key: %s\n", report.SavedConfig)
	}
	for _, a := range report.KeptAside {
		fmt.Printf("kept aside: %s\n", a)
	}
	if report.SafetyArchive != "" {
		fmt.Printf("the replaced installation was backed up to %s\n", report.SafetyArchive)
	}
	for _, p := range report.MissingPlugins {
		fmt.Printf("install plugin %s %s: the archive's installation ran it and this machine does not have it\n", p.Name, p.Version)
	}
	fmt.Println("start the server to finish: it applies any database upgrades on start")
	return nil
}

// stageRestoredSecrets prepares the config file with the archive's secrets.
// Committing keeps the replaced file as <config>.pre-restore-<stamp>, since
// it holds the only copy of the encryption key the replaced data used.
func stageRestoredSecrets(configPath string, s backup.Secrets) (func() (string, error), func(), []string, error) {
	values := map[string]string{"encryption_key": s.EncryptionKey, "jwt_secret": s.JWTSecret}
	pending, err := stageConfigValues(configPath, values, ".restoring")
	if err != nil {
		return nil, nil, nil, err
	}
	commit := func() (string, error) {
		original, err := os.ReadFile(pending.target)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(pending.target)
		if err != nil {
			return "", err
		}
		saved := pending.target + ".pre-restore-" + time.Now().UTC().Format("20060102T150405Z")
		if err := os.WriteFile(saved, original, info.Mode().Perm()); err != nil {
			return "", fmt.Errorf("keeping a copy of %s: %w", pending.target, err)
		}
		if err := preserveOwner(saved, info); err != nil {
			return "", err
		}
		return saved, pending.commit()
	}
	return commit, pending.cleanup, []string{"security.encryption_key", "security.jwt_secret"}, nil
}
