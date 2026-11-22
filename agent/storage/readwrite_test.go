package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// TestStatPathReportsMetadataAndDigest checks the useful contract at the real
// filesystem boundary, including the digest used to detect content changes.
func TestStatPathReportsMetadataAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value.txt")
	require.NoError(t, os.WriteFile(path, []byte("karakuri\n"), 0o640), "the fixture file must be created")

	details, err := statPath(path, true)
	require.NoError(t, err, "a regular file must be inspectable")
	assert.Equal(t, true, details["exists"])
	assert.Equal(t, "file", details["type"])
	assert.Equal(t, int64(9), details["size"])
	assert.Equal(t, "0640", details["permissions"])
	assert.Equal(t, "74f536611b56357520fa5d9316749882df15703444323c72b52605ad0e7c3681", details["sha256"])
}

// TestGuardedFileWrites verifies content and permission contracts through real staged replacements.
func TestGuardedFileWrites(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		expected               string
		preserve, create, fail bool
	}{
		{name: "matching digest", expected: fmt.Sprintf("%x", sha256.Sum256([]byte("old"))), preserve: true},
		{name: "mismatched digest", expected: fmt.Sprintf("%x", sha256.Sum256([]byte("different"))), preserve: true, fail: true},
		{name: "exclusive collision", create: true, fail: true},
		{name: "reset permissions", preserve: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "value")
			require.NoError(t, os.WriteFile(path, []byte("old"), 0600))
			err := writeGuarded(context.Background(), path, []byte("new"), 0644, tc.preserve, tc.create, tc.expected)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			want := "new"
			if tc.fail {
				want = "old"
			}
			assert.Equal(t, want, string(contents))
			if runtime.GOOS != "windows" {
				info, err := os.Stat(path)
				require.NoError(t, err)
				mode := os.FileMode(0644)
				if tc.preserve || tc.fail {
					mode = 0600
				}
				assert.Equal(t, mode, info.Mode().Perm())
			}
		})
	}
}

// TestReplacementRefusesDirectory pins that a replacing write never installs
// over a directory. A templated file name that renders empty cleans the path
// to its parent directory; replacing it would move the directory to the backup
// name and then remove the backup with everything in it.
func TestReplacementRefusesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reports")
	kept := filepath.Join(dir, "kept.txt")
	require.NoError(t, os.MkdirAll(dir, 0o755), "the fixture directory must be created")
	require.NoError(t, os.WriteFile(kept, []byte("keep"), 0o644), "the fixture file must be created")

	err := writeGuarded(context.Background(), dir, []byte("new"), 0o644, true, false, "")
	assert.Error(t, err, "replacing a directory with a file must be refused")
	contents, err := os.ReadFile(kept)
	require.NoError(t, err, "the directory's contents must survive the refused write")
	assert.Equal(t, "keep", string(contents))
	entries, err := os.ReadDir(filepath.Dir(dir))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the refused write must not leave a staging file beside the directory")
}

// TestExclusiveFileCreation prevents concurrent staged writers from overwriting the winner or an existing symlink.
func TestExclusiveFileCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "created")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, content := range []string{"first", "second"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			errs <- writeGuarded(context.Background(), path, []byte(value), 0644, false, true, "")
		}(content)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes, "exactly one creator must install the complete file")
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, []string{"first", "second"}, string(contents))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "both staging files must be removed")
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, "link")
		require.NoError(t, os.Symlink(path, link))
		err := writeGuarded(context.Background(), link, []byte("overwrite"), 0644, false, true, "")
		assert.Error(t, err)
		target, err := os.Readlink(link)
		require.NoError(t, err)
		assert.Equal(t, path, target)
	}
}

// TestReplacementWritesThroughSymlink locks that a replacing write to a
// symlinked path changes the file the link names, as an append does. The
// staged file was renamed over the link itself, so the link became a regular
// file and the file it pointed at kept its old content.
func TestReplacementWritesThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege a Windows test run may not hold")
	}
	for _, tc := range []struct {
		name string
		path string
		real string
	}{
		{name: "a plain path", path: "store/config.yaml", real: "store/config.yaml"},
		{name: "a link to the file", path: "link.yaml", real: "store/config.yaml"},
		{name: "a link to a link", path: "second.yaml", real: "store/config.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			real := filepath.Join(root, filepath.FromSlash(tc.real))
			require.NoError(t, os.MkdirAll(filepath.Dir(real), 0o755), "the fixture directory must be created")
			require.NoError(t, os.WriteFile(real, []byte("old"), 0o600), "the fixture file must be created")
			require.NoError(t, os.Symlink(real, filepath.Join(root, "link.yaml")), "the test needs a link to the file")
			require.NoError(t, os.Symlink("link.yaml", filepath.Join(root, "second.yaml")), "the test needs a relative link to the link")

			path := filepath.Join(root, filepath.FromSlash(tc.path))
			require.NoError(t, writeGuarded(context.Background(), path, []byte("new"), 0o644, true, false, ""),
				"A replacing write through a link must succeed.")

			contents, err := os.ReadFile(real)
			require.NoError(t, err)
			assert.Equal(t, "new", string(contents), "The file the path leads to must hold the new content.")
			for _, link := range []string{"link.yaml", "second.yaml"} {
				info, err := os.Lstat(filepath.Join(root, link))
				require.NoError(t, err)
				assert.NotZero(t, info.Mode()&os.ModeSymlink, "The write must leave %s a link.", link)
			}
			entries, err := os.ReadDir(filepath.Dir(real))
			require.NoError(t, err)
			assert.Len(t, entries, 1, "The write must leave no staging or backup file beside the target.")
		})
	}
}

// writeTestContext supplies a Write File configuration and renders templates
// as written.
type writeTestContext struct {
	nodeapi.ExecContext
	cfg fileWriteConfig
}

func (c writeTestContext) Config(out any) error {
	raw, err := json.Marshal(c.cfg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (writeTestContext) Interpolate(s string, _ *nodeapi.Msg) (string, error) { return s, nil }

// TestDeleteRefusesDirectory locks that delete mode removes files only.
// os.Remove takes an empty directory as well, so a templated file name that
// rendered empty cleaned the path to its parent and removed that directory
// once the files in it were gone. The server's Write File refuses the same
// path. A file in the directory is still deleted.
func TestDeleteRefusesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	require.NoError(t, os.MkdirAll(dir, 0o755), "the fixture directory must be created")
	file := filepath.Join(dir, "done.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644), "the fixture file must be created")
	input := map[string]*nodeapi.Msg{"in": {}}

	_, err := (fileWriteNode{}).Execute(context.Background(),
		writeTestContext{cfg: fileWriteConfig{Path: file, Mode: fileModeDelete}}, input)
	require.NoError(t, err, "deleting a file must succeed")
	assert.NoFileExists(t, file, "the file is gone")

	// The directory is empty now, which is when os.Remove would take it.
	_, err = (fileWriteNode{}).Execute(context.Background(),
		writeTestContext{cfg: fileWriteConfig{Path: dir + string(os.PathSeparator), Mode: fileModeDelete}}, input)
	require.Error(t, err, "deleting a directory must be refused")
	assert.Contains(t, err.Error(), "is a directory", "the error says why nothing was deleted")
	assert.DirExists(t, dir, "the empty directory must survive")
}
