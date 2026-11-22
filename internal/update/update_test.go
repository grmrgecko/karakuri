package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creativeprojects/go-selfupdate"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarGz builds a release-shaped archive from name/content pairs.
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// TestExtractDropsTheWrappingDirectory pins the release archive layout
// goreleaser writes with wrap_in_directory: every file sits under one
// karakuri-<version>.<os>-<arch>/ directory, which an update unpacks
// without, so the binary and plugins land where the install keeps them.
func TestExtractDropsTheWrappingDirectory(t *testing.T) {
	dir := t.TempDir()
	data := tarGz(t, map[string]string{
		"karakuri-0.2.0.linux-amd64/karakuri":                      "binary",
		"karakuri-0.2.0.linux-amd64/plugins/weather/manifest.yaml": "name: weather",
	})
	require.NoError(t, extract("karakuri-0.2.0.linux-amd64.tar.gz", data, dir))

	body, err := os.ReadFile(filepath.Join(dir, "karakuri"))
	require.NoError(t, err)
	assert.Equal(t, "binary", string(body), "the binary is unpacked at the top of the staging directory")
	_, err = os.Stat(filepath.Join(dir, "plugins", "weather", "manifest.yaml"))
	assert.NoError(t, err, "resource directories keep their layout")
}

// TestExtractRefusesEntriesThatLeaveTheArchive guards the write side of an
// update: a damaged or hostile archive naming ../ must not write outside the
// staging directory, which sits inside the install.
func TestExtractRefusesEntriesThatLeaveTheArchive(t *testing.T) {
	dir := t.TempDir()
	data := tarGz(t, map[string]string{"top/../../escaped": "x"})
	assert.Error(t, extract("evil.tar.gz", data, filepath.Join(dir, "stage")), "an entry climbing out of the archive is refused")
	_, err := os.Stat(filepath.Join(dir, "escaped"))
	assert.True(t, os.IsNotExist(err), "nothing is written outside the staging directory")
}

// pendingInstall lays out an install after Apply: the new binary and plugins
// in place, the old ones beside them as dot-file backups, and the record.
func pendingInstall(t *testing.T) (dir string, binary string, plugins string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "var")
	binary = filepath.Join(root, "karakuri")
	plugins = filepath.Join(root, "plugins")
	require.NoError(t, os.WriteFile(binary, []byte("new"), 0o755))
	require.NoError(t, os.WriteFile(backupPath(binary), []byte("old"), 0o755))
	require.NoError(t, os.MkdirAll(plugins, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugins, "v"), []byte("new"), 0o644))
	require.NoError(t, os.MkdirAll(backupPath(plugins), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(backupPath(plugins), "v"), []byte("old"), 0o644))
	s := &state{From: "0.1.0", To: "0.2.0", Binary: binary, BinaryBackup: backupPath(binary),
		Resources: []resource{{Path: plugins, Backup: backupPath(plugins), Existed: true}}}
	require.NoError(t, s.save(dir))
	return dir, binary, plugins
}

// TestFailedStartsRestoreThePreviousVersion pins the rollback: the new
// version gets maxAttempts starts to confirm, and the start after that puts
// the old binary and resource directories back and reports it, so the caller
// exits and the service manager starts the restored version.
func TestFailedStartsRestoreThePreviousVersion(t *testing.T) {
	dir, binary, plugins := pendingInstall(t)
	for i := 1; i <= maxAttempts; i++ {
		rec, err := Recover(dir, "0.2.0")
		require.NoError(t, err)
		assert.Falsef(t, rec.RolledBack, "start %d of %d stays on the new version", i, maxAttempts)
	}
	rec, err := Recover(dir, "0.2.0")
	require.NoError(t, err)
	assert.True(t, rec.RolledBack, "the start after the last attempt rolls back")
	assert.Equal(t, "0.1.0", rec.From, "the rollback names the version it restored")

	body, _ := os.ReadFile(binary)
	assert.Equal(t, "old", string(body), "the previous binary is back")
	body, _ = os.ReadFile(filepath.Join(plugins, "v"))
	assert.Equal(t, "old", string(body), "the previous plugins are back")
	_, err = os.Stat(filepath.Join(dir, stateFile))
	assert.True(t, os.IsNotExist(err), "the record is cleared, so the restored version starts normally")
}

// TestConfirmKeepsTheNewVersion pins the healthy path: confirming removes the
// backups and the record, and a later start finds nothing to recover.
func TestConfirmKeepsTheNewVersion(t *testing.T) {
	dir, binary, plugins := pendingInstall(t)
	_, err := Recover(dir, "0.2.0")
	require.NoError(t, err)
	require.NoError(t, Confirm(dir, "0.2.0"))

	body, _ := os.ReadFile(binary)
	assert.Equal(t, "new", string(body), "the new binary stays")
	_, err = os.Stat(backupPath(binary))
	assert.True(t, os.IsNotExist(err), "the binary backup is removed")
	_, err = os.Stat(backupPath(plugins))
	assert.True(t, os.IsNotExist(err), "the plugins backup is removed")
	rec, err := Recover(dir, "0.2.0")
	require.NoError(t, err)
	assert.Empty(t, rec.To, "nothing is pending after confirmation")
}

// fakeAsset and fakeRelease serve a release from memory, shaped like the
// GitHub releases goreleaser publishes.
type fakeAsset struct {
	id   int64
	name string
	data []byte
}

func (a fakeAsset) GetID() int64                  { return a.id }
func (a fakeAsset) GetName() string               { return a.name }
func (a fakeAsset) GetSize() int                  { return len(a.data) }
func (a fakeAsset) GetBrowserDownloadURL() string { return "https://example.invalid/" + a.name }

type fakeRelease struct {
	tag    string
	assets []fakeAsset
}

func (r fakeRelease) GetID() int64              { return 1 }
func (r fakeRelease) GetTagName() string        { return r.tag }
func (r fakeRelease) GetDraft() bool            { return false }
func (r fakeRelease) GetPrerelease() bool       { return false }
func (r fakeRelease) GetPublishedAt() time.Time { return time.Now() }
func (r fakeRelease) GetReleaseNotes() string   { return "" }
func (r fakeRelease) GetName() string           { return r.tag }
func (r fakeRelease) GetURL() string            { return "https://example.invalid/" + r.tag }
func (r fakeRelease) GetAssets() []selfupdate.SourceAsset {
	out := make([]selfupdate.SourceAsset, len(r.assets))
	for i, a := range r.assets {
		out[i] = a
	}
	return out
}

type fakeSource struct{ rel fakeRelease }

func (s fakeSource) ListReleases(context.Context, selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	return []selfupdate.SourceRelease{s.rel}, nil
}

func (s fakeSource) DownloadReleaseAsset(_ context.Context, _ *selfupdate.Release, id int64) (io.ReadCloser, error) {
	for _, a := range s.rel.assets {
		if a.id == id {
			return io.NopCloser(bytes.NewReader(a.data)), nil
		}
	}
	return nil, fmt.Errorf("no asset %d", id)
}

// TestAgentUpdateFromRelease runs an agent update against a release laid out
// as goreleaser publishes one: server, agent, and automation archives for the
// same platform side by side, and checksums.txt. The agent must pick its own
// archive, verify it, run the new binary's version command, swap it in, keep
// the old binary, and record the update for the next start to confirm.
func TestAgentUpdateFromRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	script := "#!/bin/sh\necho karakuri-agent 0.2.0\n"
	agentArchive := tarGz(t, map[string]string{"karakuri-agent-0.2.0." + platform + "/karakuri-agent": script})
	serverArchive := tarGz(t, map[string]string{"karakuri-0.2.0." + platform + "/karakuri": "#!/bin/sh\necho karakuri 0.2.0\n"})
	autoArchive := tarGz(t, map[string]string{"karakuri-auto-0.2.0." + platform + "/karakuri-auto": "#!/bin/sh\n"})
	assets := []fakeAsset{
		{1, "karakuri-0.2.0." + platform + ".tar.gz", serverArchive},
		{2, "karakuri-agent-0.2.0." + platform + ".tar.gz", agentArchive},
		{3, "karakuri-auto-0.2.0." + platform + ".tar.gz", autoArchive},
	}
	var sums strings.Builder
	for _, a := range assets {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(a.data), a.name)
	}
	assets = append(assets, fakeAsset{4, "checksums.txt", []byte(sums.String())})
	previous := newSource
	newSource = func() (selfupdate.Source, error) {
		return fakeSource{fakeRelease{tag: "v0.2.0", assets: assets}}, nil
	}
	t.Cleanup(func() { newSource = previous })

	dir := t.TempDir()
	exe := filepath.Join(dir, "karakuri-agent")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\necho karakuri-agent 0.1.0\n"), 0o755))
	artifact := Agent()
	artifact.Executable = exe
	opts := Options{Current: "0.1.0", StateDir: filepath.Join(dir, "state")}

	rel, err := Check(context.Background(), opts, artifact)
	require.NoError(t, err)
	require.NotNil(t, rel, "a newer release is found")
	assert.Equal(t, "0.2.0", rel.Version)

	require.NoError(t, Apply(context.Background(), opts, artifact, rel))
	body, _ := os.ReadFile(exe)
	assert.Equal(t, script, string(body), "the agent's own archive replaced the binary, not the server's or automation's")
	old, _ := os.ReadFile(backupPath(exe))
	assert.Contains(t, string(old), "0.1.0", "the replaced binary is kept until the new one confirms")
	rec, err := Recover(opts.StateDir, "0.2.0")
	require.NoError(t, err)
	assert.Equal(t, "0.1.0", rec.From, "the update is recorded for the next start to confirm")

	opts.Current = "0.2.0"
	rel, err = Check(context.Background(), opts, artifact)
	require.NoError(t, err)
	assert.Nil(t, rel, "the running release is not offered again")

	// A forced update reinstalls the running release, which is how an install
	// that cannot reach a newer one is repaired.
	opts.Force = true
	rel, err = Check(context.Background(), opts, artifact)
	require.NoError(t, err)
	require.NotNil(t, rel, "a forced check offers the running release")
	require.NoError(t, Apply(context.Background(), opts, artifact, rel))
	body, _ = os.ReadFile(exe)
	assert.Equal(t, script, string(body), "the forced update replaces the binary with the release's")
}

// TestServerUpdateReplacesResources runs a server update: besides the binary,
// the plugins directory is replaced from the archive, and the old one is kept
// beside it until the new version confirms.
func TestServerUpdateReplacesResources(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	archive := tarGz(t, map[string]string{
		"karakuri-0.2.0." + platform + "/karakuri":                      "#!/bin/sh\necho karakuri 0.2.0\n",
		"karakuri-0.2.0." + platform + "/plugins/weather/manifest.yaml": "version: 0.2.0",
	})
	name := "karakuri-0.2.0." + platform + ".tar.gz"
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), name)
	previous := newSource
	newSource = func() (selfupdate.Source, error) {
		return fakeSource{fakeRelease{tag: "v0.2.0", assets: []fakeAsset{{1, name, archive}, {2, "checksums.txt", []byte(sums)}}}}, nil
	}
	t.Cleanup(func() { newSource = previous })

	dir := t.TempDir()
	exe := filepath.Join(dir, "karakuri")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\necho karakuri 0.1.0\n"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "plugins", "weather"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugins", "weather", "manifest.yaml"), []byte("version: 0.1.0"), 0o644))
	artifact := Server(dir)
	artifact.Executable = exe
	opts := Options{Current: "0.1.0", StateDir: filepath.Join(dir, "var")}

	rel, err := Check(context.Background(), opts, artifact)
	require.NoError(t, err)
	require.NotNil(t, rel)
	require.NoError(t, Apply(context.Background(), opts, artifact, rel))

	manifest, _ := os.ReadFile(filepath.Join(dir, "plugins", "weather", "manifest.yaml"))
	assert.Equal(t, "version: 0.2.0", string(manifest), "the plugins come from the new release")
	kept, _ := os.ReadFile(filepath.Join(backupPath(filepath.Join(dir, "plugins")), "weather", "manifest.yaml"))
	assert.Equal(t, "version: 0.1.0", string(kept), "the old plugins wait beside them for confirmation")
	_, err = os.Stat(filepath.Join(dir, "themes"))
	assert.True(t, os.IsNotExist(err), "a directory the archive does not carry is left alone")
}

// fakeService stands in for the server's service manager: restarts are
// counted, and each restart reports the next outcome in order.
type fakeService struct {
	restarts int
	fail     []bool
	running  bool
}

func (s *fakeService) Restart() error {
	s.restarts++
	if len(s.fail) > 0 {
		fail := s.fail[0]
		s.fail = s.fail[1:]
		if fail {
			s.running = false
			return fmt.Errorf("job failed")
		}
	}
	s.running = true
	return nil
}

func (s *fakeService) Running() (bool, error) { return s.running, nil }

// serverRelease serves one server release whose binary reports 0.2.0.
func serverRelease(t *testing.T) {
	t.Helper()
	platform := runtime.GOOS + "-" + runtime.GOARCH
	archive := tarGz(t, map[string]string{
		"karakuri-0.2.0." + platform + "/karakuri":                      "#!/bin/sh\necho karakuri 0.2.0\n",
		"karakuri-0.2.0." + platform + "/plugins/weather/manifest.yaml": "version: 0.2.0",
	})
	name := "karakuri-0.2.0." + platform + ".tar.gz"
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), name)
	previous, previousSettle := newSource, settleTime
	newSource = func() (selfupdate.Source, error) {
		return fakeSource{fakeRelease{tag: "v0.2.0", assets: []fakeAsset{{1, name, archive}, {2, "checksums.txt", []byte(sums)}}}}, nil
	}
	settleTime = 0
	t.Cleanup(func() { newSource, settleTime = previous, previousSettle })
}

// TestSupervisedUpdate pins the system install's update, which runs outside
// the server: a service that comes up keeps the new version with no backups
// left, and one whose restart fails gets the old binary and plugins back and
// is restarted on them.
func TestSupervisedUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	tests := []struct {
		name         string
		fail         []bool
		wantErr      bool
		wantVersion  string
		wantRestarts int
	}{
		{name: "healthy", fail: []bool{false}, wantVersion: "0.2.0", wantRestarts: 1},
		{name: "fails to start", fail: []bool{true, false}, wantErr: true, wantVersion: "0.1.0", wantRestarts: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverRelease(t)
			dir := t.TempDir()
			exe := filepath.Join(dir, "karakuri")
			require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\necho karakuri 0.1.0\n"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "plugins", "weather"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "plugins", "weather", "manifest.yaml"), []byte("version: 0.1.0"), 0o644))
			artifact := Server(dir)
			artifact.Executable = exe
			opts := Options{Current: "0.1.0", StateDir: dir}
			svc := &fakeService{fail: tt.fail}

			rel, err := Check(context.Background(), opts, artifact)
			require.NoError(t, err)
			require.NotNil(t, rel)
			err = Supervise(context.Background(), opts, artifact, rel, svc)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			body, _ := os.ReadFile(exe)
			assert.Contains(t, string(body), tt.wantVersion, "the binary in place")
			manifest, _ := os.ReadFile(filepath.Join(dir, "plugins", "weather", "manifest.yaml"))
			assert.Equal(t, "version: "+tt.wantVersion, string(manifest), "the plugins match the binary")
			assert.Equal(t, tt.wantRestarts, svc.restarts)
			for _, leftover := range []string{backupPath(exe), backupPath(filepath.Join(dir, "plugins")), filepath.Join(dir, stateFile)} {
				_, err := os.Stat(leftover)
				assert.Truef(t, os.IsNotExist(err), "%s is cleaned up", filepath.Base(leftover))
			}
		})
	}
}

// TestResumeSettlesAnInterruptedUpdate pins what the next run does with an
// update left pending by a reboot: a running service confirms it, and a
// stopped one rolls it back.
func TestResumeSettlesAnInterruptedUpdate(t *testing.T) {
	dir, binary, _ := pendingInstall(t)
	rec, err := Resume(dir, &fakeService{running: true})
	require.NoError(t, err)
	assert.False(t, rec.RolledBack)
	body, _ := os.ReadFile(binary)
	assert.Equal(t, "new", string(body), "a running service keeps the new version")

	dir, binary, _ = pendingInstall(t)
	svc := &fakeService{}
	rec, err = Resume(dir, svc)
	require.NoError(t, err)
	assert.True(t, rec.RolledBack)
	body, _ = os.ReadFile(binary)
	assert.Equal(t, "old", string(body), "a stopped service gets the previous version back")
	assert.Equal(t, 1, svc.restarts, "and is started on it")
}

// v010Assets is the asset list of the v0.1.0 GitHub release, in the order
// GitHub returns it. The macOS server archive comes first, which is the one
// every host downloaded while the filter named no platform.
var v010Assets = []string{
	"checksums.txt",
	"karakuri-0.1.0.darwin-amd64.tar.gz",
	"karakuri-0.1.0.darwin-arm64.tar.gz",
	"karakuri-0.1.0.linux-amd64.tar.gz",
	"karakuri-0.1.0.linux-arm64.tar.gz",
	"karakuri-0.1.0.windows-amd64.zip",
	"karakuri-agent-0.1.0.darwin-amd64.tar.gz",
	"karakuri-agent-0.1.0.darwin-arm64.tar.gz",
	"karakuri-agent-0.1.0.freebsd-amd64.tar.gz",
	"karakuri-agent-0.1.0.freebsd-arm64.tar.gz",
	"karakuri-agent-0.1.0.linux-amd64.tar.gz",
	"karakuri-agent-0.1.0.linux-arm64.tar.gz",
	"karakuri-agent-0.1.0.linux-armv7.tar.gz",
	"karakuri-agent-0.1.0.windows-amd64.zip",
	"karakuri-agent-0.1.0.windows-arm64.zip",
	"karakuri-assist-0.1.0.zip",
	"karakuri-auto-0.1.0.darwin-amd64.tar.gz",
	"karakuri-auto-0.1.0.darwin-arm64.tar.gz",
	"karakuri-auto-0.1.0.freebsd-amd64.tar.gz",
	"karakuri-auto-0.1.0.freebsd-arm64.tar.gz",
	"karakuri-auto-0.1.0.linux-amd64.tar.gz",
	"karakuri-auto-0.1.0.linux-arm64.tar.gz",
	"karakuri-auto-0.1.0.linux-armv7.tar.gz",
	"karakuri-auto-0.1.0.windows-amd64.zip",
	"karakuri-auto-0.1.0.windows-arm64.zip",
}

// TestPlatformFilterPicksTheHostsArchive locks the asset choice against a
// real release's names. go-selfupdate skips its platform matching when a
// filter is set and takes the first asset the filter matches, so a filter of
// the artifact name alone sent every Linux server and agent the macOS
// archive, which the smoke test then refused every day. Each artifact and
// platform must match exactly its own archive.
func TestPlatformFilterPicksTheHostsArchive(t *testing.T) {
	tests := []struct {
		name, goos, goarch, want string
	}{
		{"karakuri", "linux", "amd64", "karakuri-0.1.0.linux-amd64.tar.gz"},
		{"karakuri", "linux", "arm64", "karakuri-0.1.0.linux-arm64.tar.gz"},
		{"karakuri", "darwin", "arm64", "karakuri-0.1.0.darwin-arm64.tar.gz"},
		{"karakuri", "windows", "amd64", "karakuri-0.1.0.windows-amd64.zip"},
		{"karakuri-agent", "linux", "amd64", "karakuri-agent-0.1.0.linux-amd64.tar.gz"},
		{"karakuri-agent", "linux", "arm", "karakuri-agent-0.1.0.linux-armv7.tar.gz"},
		{"karakuri-agent", "freebsd", "arm64", "karakuri-agent-0.1.0.freebsd-arm64.tar.gz"},
		{"karakuri-agent", "windows", "arm64", "karakuri-agent-0.1.0.windows-arm64.zip"},
		{"karakuri", "linux", "arm", ""},
	}
	for _, tc := range tests {
		re := regexp.MustCompile(platformFilter(tc.name, tc.goos, tc.goarch))
		var matched []string
		for _, asset := range v010Assets {
			if re.MatchString(asset) {
				matched = append(matched, asset)
			}
		}
		if tc.want == "" {
			assert.Emptyf(t, matched, "%s has no %s-%s archive, so nothing may match", tc.name, tc.goos, tc.goarch)
			continue
		}
		assert.Equalf(t, []string{tc.want}, matched, "%s on %s-%s", tc.name, tc.goos, tc.goarch)
	}
}
