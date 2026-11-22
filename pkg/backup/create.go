package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Secrets are the configuration values an archive carries: without the
// encryption key no stored credential or agent CA key opens, and without the
// JWT secret every session and API token is void.
type Secrets struct {
	EncryptionKey string `json:"encryption_key"`
	JWTSecret     string `json:"jwt_secret"`
}

// Source is the installation a backup copies.
type Source struct {
	DB           *gorm.DB
	DatabaseType string
	DataDir      string
	FileRoots    []string
	Secrets      Secrets
	Plugins      []Plugin
	Version      string
	Installation string
}

// archivePattern matches archive file names and nothing else, so routes that
// take a name can never reach another file.
var archivePattern = regexp.MustCompile(`^karakuri-([a-z0-9-]+)-(\d{8}T\d{6}Z)\.tar\.zst\.age$`)

const stampLayout = "20060102T150405Z"

// ArchiveName returns the file name of an archive of the installation taken
// at t.
func ArchiveName(installation string, t time.Time) string {
	return "karakuri-" + installation + "-" + t.UTC().Format(stampLayout) + ".tar.zst.age"
}

// ParseArchiveName returns the installation and time an archive name holds.
func ParseArchiveName(name string) (string, time.Time, bool) {
	m := archivePattern.FindStringSubmatch(name)
	if m == nil {
		return "", time.Time{}, false
	}
	t, err := time.Parse(stampLayout, m[2])
	if err != nil {
		return "", time.Time{}, false
	}
	return m[1], t, true
}

// InstallationName turns a host name into the name archives carry: lowercase,
// with anything outside a-z, 0-9, and - replaced by -.
func InstallationName(host string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "karakuri"
	}
	return name
}

// Write streams a backup of src to w and returns its manifest. work is a
// directory for the table files staged on their way into the archive; it
// should be on the same disk as the archive, not a small /tmp.
func Write(ctx context.Context, w io.Writer, src Source, passphrase, work string) (*Manifest, error) {
	a, err := newArchiveWriter(w, passphrase)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			a.abort()
		}
	}()
	stage, err := os.MkdirTemp(work, ".karakuri-backup-")
	if err != nil {
		return nil, fmt.Errorf("creating a staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	created := time.Now().UTC()
	tables, counts, err := dumpDatabase(ctx, src.DB, stage, a.addFile)
	if err != nil {
		return nil, err
	}

	secrets, err := json.Marshal(src.Secrets)
	if err != nil {
		return nil, err
	}
	if err := a.addBytes("secrets.json", secrets, created); err != nil {
		return nil, err
	}

	for _, part := range dataParts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := writeTree(a, filepath.Join(src.DataDir, part), part, !slices.Contains(userParts, part)); err != nil {
			return nil, fmt.Errorf("copying %s: %w", part, err)
		}
	}
	roots, skipped := splitFileRoots(src.DataDir, src.FileRoots)
	fileRoots := []string{}
	for _, root := range roots {
		fileRoots = append(fileRoots, strings.TrimPrefix(root.prefix, "files/"))
		if err := writeTree(a, root.dir, root.prefix, false); err != nil {
			return nil, fmt.Errorf("copying flow files in %s: %w", root.dir, err)
		}
	}

	m := &Manifest{
		Version:      src.Version,
		CreatedAt:    created,
		Installation: src.Installation,
		Database:     src.DatabaseType,
		Counts:       counts,
		Tables:       tables,
		Plugins:      src.Plugins,
		FileRoots:    fileRoots,
		Skipped:      append(append([]string{}, skippedParts...), skipped...),
	}
	if m.Plugins == nil {
		m.Plugins = []Plugin{}
	}
	if err := a.finish(m); err != nil {
		return nil, fmt.Errorf("finishing the archive: %w", err)
	}
	ok = true
	return m, nil
}

// Verify reads a whole archive and checks it, returning its manifest. Every
// failure names what did not match; a wrong passphrase and a damaged header
// both answer ErrPassphrase.
func Verify(path, passphrase string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return walkArchive(f, passphrase, nil)
}
