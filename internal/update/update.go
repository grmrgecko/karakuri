// Package update installs newer Karakuri releases from GitHub over the running
// install: the server binary with the plugins, themes, and runtimes beside it,
// or the agent binary alone. It verifies each archive against the release's
// checksums, runs the new binary before swapping it in, keeps the replaced
// files until the new version reports a healthy start, and puts them back if
// it does not.
package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
	selfapply "github.com/creativeprojects/go-selfupdate/update"
)

// Repository is where releases are published.
const (
	Owner = "grmrgecko"
	Repo  = "karakuri"
)

// Artifact describes one thing a release updates.
type Artifact struct {
	// Name is the executable's name without an extension, and the start of
	// its archive's name: karakuri or karakuri-agent.
	Name string
	// Resources are the directories the archive carries beside the binary
	// that are replaced with it, such as plugins and themes. Empty for the
	// agent, whose archive is one binary.
	Resources []string
	// ResourceRoot is the directory holding Resources in this install.
	ResourceRoot string
	// Executable is the binary to replace. Empty means the running one.
	Executable string
}

// Server and Agent are the two artifacts a release updates.
func Server(resourceRoot string) Artifact {
	return Artifact{Name: "karakuri", Resources: []string{"plugins", "themes", "runtimes"}, ResourceRoot: resourceRoot}
}

// Agent is the remote agent's artifact: its binary alone.
func Agent() Artifact { return Artifact{Name: "karakuri-agent"} }

// Options configure a check or an update.
type Options struct {
	// Current is the running version.
	Current string
	// Prerelease admits releases GitHub marks as pre-releases.
	Prerelease bool
	// StateDir holds the record of an update awaiting confirmation.
	StateDir string
	// Force offers the latest release even when it is not newer than the
	// running one, to reinstall it over a damaged or altered install.
	Force bool
}

// Release is a newer release found by Check.
type Release struct {
	Version string
	URL     string
	release *selfupdate.Release
	updater *selfupdate.Updater
	source  selfupdate.Source
}

// newSource returns where releases are read from: GitHub. Tests serve
// release archives from memory instead.
var newSource = func() (selfupdate.Source, error) {
	return selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
}

// ErrNotWritable reports an install the running account cannot replace.
var ErrNotWritable = errors.New("install is not writable")

// assetFilter picks the artifact's archive for this host among the release's.
func assetFilter(a Artifact) string {
	return platformFilter(a.Name, runtime.GOOS, runtime.GOARCH)
}

// platformFilter matches one artifact's archive for one platform, named as
// the release publishes it: <name>-<version>.<os>-<arch>.tar.gz or .zip.
// go-selfupdate does no platform matching of its own once a filter is given,
// so the filter has to name the platform; without it every host took the
// first archive listed, the macOS one. The digit after the name keeps
// karakuri apart from karakuri-agent and karakuri-auto. 32-bit ARM is
// published for ARMv7 only, as armv7.
func platformFilter(name, goos, goarch string) string {
	arch := goarch
	if arch == "arm" {
		arch = "armv7"
	}
	return "^" + regexp.QuoteMeta(name) + `-[0-9][^/]*\.` + regexp.QuoteMeta(goos+"-"+arch) + `\.(tar\.gz|zip)$`
}

// Check looks for a release newer than the running version, returning nil
// when there is none. With opts.Force it returns the latest release whatever
// its version.
func Check(ctx context.Context, opts Options, a Artifact) (*Release, error) {
	source, err := newSource()
	if err != nil {
		return nil, err
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:     source,
		Validator:  &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
		Filters:    []string{assetFilter(a)},
		Prerelease: opts.Prerelease,
	})
	if err != nil {
		return nil, err
	}
	rel, found, err := updater.DetectLatest(ctx, selfupdate.NewRepositorySlug(Owner, Repo))
	if err != nil {
		return nil, fmt.Errorf("looking up the latest release: %w", err)
	}
	if !found || (!opts.Force && !rel.GreaterThan(strings.TrimPrefix(opts.Current, "v"))) {
		return nil, nil
	}
	return &Release{Version: rel.Version(), URL: rel.URL, release: rel, updater: updater, source: source}, nil
}

// Writable reports ErrNotWritable, naming the path, when the running account
// cannot replace the binary or a resource directory of this install.
func Writable(a Artifact) error {
	exe, err := a.executable()
	if err != nil {
		return err
	}
	dirs := []string{filepath.Dir(exe)}
	if len(a.Resources) > 0 {
		dirs = append(dirs, a.ResourceRoot)
	}
	for _, dir := range dirs {
		f, err := os.CreateTemp(dir, ".karakuri-update-check-*")
		if err != nil {
			return fmt.Errorf("%w: %s", ErrNotWritable, dir)
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
	return nil
}

// executable is the binary to replace: the configured one, or the running
// binary, with symbolic links resolved so the file replaced is the one that
// runs.
func (a Artifact) executable() (string, error) {
	exe := a.Executable
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(exe)
}

// Apply downloads and verifies the release, runs its binary, then replaces
// the running binary and the artifact's resource directories, keeping the
// replaced files and recording the update so the next start can confirm or
// undo it. The running process keeps the old version until it restarts.
func Apply(ctx context.Context, opts Options, a Artifact, rel *Release) error {
	if err := Writable(a); err != nil {
		return err
	}
	exe, err := a.executable()
	if err != nil {
		return err
	}

	// A backup left by an earlier update would otherwise be what a failed
	// swap puts back.
	_ = os.Remove(backupPath(exe))

	// Download and verify.
	data, err := download(ctx, rel.source, rel.release, rel.release.AssetID)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", rel.release.AssetName, err)
	}
	sums, err := download(ctx, rel.source, rel.release, rel.release.ValidationAssetID)
	if err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}
	if err := (&selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"}).Validate(rel.release.AssetName, data, sums); err != nil {
		return fmt.Errorf("verifying %s: %w", rel.release.AssetName, err)
	}

	// Unpack beside the install, so moving the resources into place is a
	// rename on one filesystem.
	stageParent := filepath.Dir(exe)
	if len(a.Resources) > 0 {
		stageParent = a.ResourceRoot
	}
	stage, err := os.MkdirTemp(stageParent, ".karakuri-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := extract(rel.release.AssetName, data, stage); err != nil {
		return fmt.Errorf("unpacking %s: %w", rel.release.AssetName, err)
	}
	newBinary := filepath.Join(stage, binaryName(a.Name))
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("%s has no %s", rel.release.AssetName, binaryName(a.Name))
	}

	// A binary that does not run on this host, or reports another version,
	// never replaces the one that does.
	if err := smokeTest(ctx, newBinary, rel.Version); err != nil {
		return err
	}

	// Swap the resources, then the binary, recording each replaced file so a
	// failed start can put it back.
	st := state{From: opts.Current, To: rel.Version, Binary: exe, BinaryBackup: backupPath(exe), At: time.Now().UTC()}
	for _, name := range a.Resources {
		src := filepath.Join(stage, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(a.ResourceRoot, name)
		backup := backupPath(dst)
		_ = os.RemoveAll(backup)
		moved := false
		if _, err := os.Stat(dst); err == nil {
			if err := os.Rename(dst, backup); err != nil {
				st.restore()
				return fmt.Errorf("setting aside %s: %w", dst, err)
			}
			moved = true
		}
		if err := os.Rename(src, dst); err != nil {
			if moved {
				_ = os.Rename(backup, dst)
			}
			st.restore()
			return fmt.Errorf("installing %s: %w", dst, err)
		}
		st.Resources = append(st.Resources, resource{Path: dst, Backup: backup, Existed: moved})
	}
	f, err := os.Open(newBinary)
	if err != nil {
		st.restore()
		return err
	}
	err = selfapply.Apply(f, selfapply.Options{TargetPath: exe, OldSavePath: st.BinaryBackup})
	_ = f.Close()
	if err != nil {
		st.restore()
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	if err := st.save(opts.StateDir); err != nil {
		return fmt.Errorf("recording the update: %w", err)
	}
	return nil
}

// download reads one release asset.
func download(ctx context.Context, source selfupdate.Source, rel *selfupdate.Release, id int64) ([]byte, error) {
	r, err := source.DownloadReleaseAsset(ctx, rel, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// binaryName adds the platform's executable extension.
func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// backupPath is where a replaced file waits for the new version to confirm:
// a dot file beside it.
func backupPath(path string) string {
	dir, base := filepath.Split(path)
	return filepath.Join(dir, "."+base+".old")
}

// smokeTest runs the new binary's version command, which needs no
// configuration, and expects it to report the release's version.
func smokeTest(ctx context.Context, binary, want string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary did not run on this host: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), want) {
		return fmt.Errorf("the new binary reports %q, not version %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}
