package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// TestListingWalkDepthAndHidden pins what a listing reaches, against a real
// tree rather than a description of one. Two rules decide every answer and
// neither is obvious from the config: a directory sitting at the depth limit is
// listed but not descended, and a hidden directory is skipped whole rather than
// walked for the visible files inside it. A drop folder with a .git or a
// .stversions in it is the case that made the second one matter.
func TestListingWalkDepthAndHidden(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"report.csv":            "one",
		"notes.txt":             "two",
		".hidden.txt":           "three",
		"sub/nested.csv":        "four",
		"sub/deep/deepest.csv":  "five",
		".versions/archive.csv": "six",
	})

	tests := []struct {
		name string
		cfg  fileListConfig
		want []string
		why  string
	}{
		{
			name: "the directory itself",
			cfg:  fileListConfig{},
			want: []string{"notes.txt", "report.csv"},
			why:  "the default is files one level down, so nothing hidden and nothing nested",
		},
		{
			name: "hidden entries included",
			cfg:  fileListConfig{Hidden: true},
			want: []string{".hidden.txt", "notes.txt", "report.csv"},
			why:  "a dot file is listed only when it was asked for",
		},
		{
			name: "a pattern all the way down",
			cfg:  fileListConfig{Pattern: "*.csv", Recursive: true},
			want: []string{"deepest.csv", "nested.csv", "report.csv"},
			why: "the glob matches the name and a recursive walk reaches every level, but " +
				"archive.csv is left out: it is visible and sits inside a hidden directory",
		},
		{
			name: "depth limited to one level",
			cfg:  fileListConfig{Recursive: true, MaxDepth: 1},
			want: []string{"notes.txt", "report.csv"},
			why:  "sub is at the limit, so it is not descended and its files are out of reach",
		},
		{
			name: "directories only",
			cfg:  fileListConfig{Include: listDirectories, Recursive: true},
			want: []string{"deep", "sub"},
			why:  "a directory listing reports the directories and none of the files in them",
		},
	}

	for _, tc := range tests {
		entries, err := walkListing(context.Background(), root, tc.cfg)
		require.NoErrorf(t, err, "%s: the walk must succeed", tc.name)
		assert.Equalf(t, tc.want, listedNames(entries), "%s: %s", tc.name, tc.why)
	}
}

// TestTransferDestination pins how a destination is read, which is the rule an
// author relies on without stating: a path ending in a separator, or naming a
// directory that is already there, means "inside it under the same name", and
// anything else is the new name. Getting it the other way round turns a move
// into a rename that quietly replaces the target folder.
func TestTransferDestination(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "archive")
	require.NoError(t, os.MkdirAll(existing, 0o755), "the test needs a directory that is already there")
	source := filepath.Join(root, "report.csv")

	tests := []struct {
		name  string
		given string
		want  string
		why   string
	}{
		{
			name:  "a trailing separator",
			given: filepath.Join(root, "outbox") + string(os.PathSeparator),
			want:  filepath.Join(root, "outbox", "report.csv"),
			why:   "the separator says it is a directory even though nothing is there yet",
		},
		{
			name:  "an existing directory",
			given: existing,
			want:  filepath.Join(existing, "report.csv"),
			why:   "a directory that is already there takes the file inside rather than being replaced by it",
		},
		{
			name:  "a plain path",
			given: filepath.Join(root, "renamed.csv"),
			want:  filepath.Join(root, "renamed.csv"),
			why:   "anything else is the name the file takes",
		},
	}
	for _, tc := range tests {
		got, err := resolveDestination(tc.given, source)
		require.NoErrorf(t, err, "%s: the destination must resolve", tc.name)
		assert.Equalf(t, tc.want, got, "%s: %s", tc.name, tc.why)
	}
}

// TestCopyTree pins that a copy reaches the whole tree and keeps the modes it
// found, which is what separates it from a caller writing files one at a time:
// a script copied without its executable bit is a copy that does not run. A
// read-only directory is the case that needs ordering: its mode can only be
// applied after its children are written, or a non-root agent cannot fill it.
func TestCopyTree(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeTree(t, source, map[string]string{
		"run.sh":          "#!/bin/sh\necho hello\n",
		"conf/app.yaml":   "name: karakuri\n",
		"conf/sub/id.txt": "42",
	})
	require.NoError(t, os.Chmod(filepath.Join(source, "run.sh"), 0o755),
		"the test needs a file whose mode is worth carrying")
	readOnly := filepath.Join(source, "conf", "sub")
	require.NoError(t, os.Chmod(readOnly, 0o555), "the test needs a read-only directory")
	destination := filepath.Join(root, "copy")
	t.Cleanup(func() {
		// TempDir cannot remove the children of a read-only directory.
		_ = os.Chmod(readOnly, 0o755)
		_ = os.Chmod(filepath.Join(destination, "conf", "sub"), 0o755)
	})

	written, err := copyPath(context.Background(), source, destination)
	require.NoError(t, err, "copying a directory must reach every level of it")

	// 21 bytes of script, 15 of yaml, and 2 of id file.
	assert.Equal(t, int64(21+15+2), written, "the byte count is every file copied, not just the top level")

	content, err := os.ReadFile(filepath.Join(destination, "conf", "sub", "id.txt"))
	require.NoError(t, err, "the deepest file must exist in the copy")
	assert.Equal(t, "42", string(content), "a copied file holds what the original held")

	info, err := os.Stat(filepath.Join(destination, "run.sh"))
	require.NoError(t, err, "the copied script must exist")
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "an executable stays executable through a copy")

	if runtime.GOOS != "windows" {
		info, err = os.Stat(filepath.Join(destination, "conf", "sub"))
		require.NoError(t, err, "the copied read-only directory must exist")
		assert.Equal(t, os.FileMode(0o555), info.Mode().Perm(), "a read-only directory stays read-only through a copy")
	}
}

// transferTestContext supplies a transfer's configuration and renders
// templates as written.
type transferTestContext struct {
	nodeapi.ExecContext
	cfg transferConfig
}

func (c transferTestContext) Config(out any) error {
	raw, err := json.Marshal(c.cfg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (transferTestContext) Interpolate(s string, _ *nodeapi.Msg) (string, error) { return s, nil }

// TestTransferRefusesAliasedPaths locks that the source-is-the-destination and
// tree-into-itself checks compare where the paths lead, not how they are
// spelled. Through a symlinked directory a replacing move copied the file over
// itself and then deleted the source, which was the same file, and a tree
// copied into its own alias descended into its own staging directory.
func TestTransferRefusesAliasedPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege a Windows test run may not hold")
	}
	for _, tc := range []struct {
		name        string
		source      string
		destination string
		move        bool
		why         string
	}{
		{
			name:        "a replacing move onto the same file",
			source:      "data/report.csv",
			destination: "alias/",
			move:        true,
			why:         "moving a file onto itself must not end with the file deleted",
		},
		{
			name:        "a tree copied into itself",
			source:      "data/tree",
			destination: "alias/tree/nested/copy",
			why:         "a destination inside the source is refused however it is spelled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			data := filepath.Join(root, "data")
			writeTree(t, data, map[string]string{"report.csv": "kept", "tree/inner.txt": "inner"})
			require.NoError(t, os.Symlink(data, filepath.Join(root, "alias")),
				"the test needs a second name for the directory")

			ec := transferTestContext{cfg: transferConfig{
				Source:      filepath.Join(root, filepath.FromSlash(tc.source)),
				Destination: filepath.Join(root, filepath.FromSlash(tc.destination)),
				Overwrite:   true,
			}}
			if strings.HasSuffix(tc.destination, "/") {
				ec.cfg.Destination += string(os.PathSeparator)
			}
			_, err := runTransfer(context.Background(), ec, map[string]*nodeapi.Msg{"in": {}}, tc.move)
			assert.Error(t, err, tc.why)

			content, readErr := os.ReadFile(filepath.Join(data, "report.csv"))
			require.NoError(t, readErr, "the source file must survive a refused transfer")
			assert.Equal(t, "kept", string(content), "a refused transfer leaves the content alone")
			assert.NoDirExists(t, filepath.Join(data, "tree", "nested"), "a refused transfer creates nothing")
		})
	}
}

// TestReplacementKeepsPriorDestination pins the failure boundary of an
// overwrite. A staged copy can disappear or become unreadable before its final
// rename; the destination must still contain the prior data after that error.
func TestReplacementKeepsPriorDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "report.txt")
	require.NoError(t, os.WriteFile(destination, []byte("prior\n"), 0o640),
		"the test needs an existing destination to protect")

	err := commitReplacement(filepath.Join(root, "missing-stage"), destination, true)
	require.Error(t, err, "installing a missing stage must fail")

	content, readErr := os.ReadFile(destination)
	require.NoError(t, readErr, "the failed replacement must restore the destination")
	assert.Equal(t, "prior\n", string(content), "the prior bytes must survive a failed installation")
}

// TestWriteReplacementPreservesMode covers the on-disk contract of file
// overwrite. Replacing configuration content must not silently remove a mode
// the operator set on the existing file.
func TestWriteReplacementPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600), "the test needs an existing file")
	require.NoError(t, writeReplacement(path, []byte("new"), 0o644), "the replacement must succeed")

	content, err := os.ReadFile(path)
	require.NoError(t, err, "the replaced file must be readable")
	assert.Equal(t, "new", string(content), "the complete replacement must be installed")
	info, err := os.Stat(path)
	require.NoError(t, err, "the replaced file must have metadata")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "an overwrite must retain the existing permissions")
}

// writeTree creates files at paths relative to root, making the directories
// they need, the way the real thing is laid out on disk.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoErrorf(t, os.MkdirAll(filepath.Dir(path), 0o755), "the test needs the directory for %s", name)
		require.NoErrorf(t, os.WriteFile(path, []byte(content), 0o644), "the test needs the file %s", name)
	}
}

// listedNames reports the names a walk found, sorted so a table row does not
// depend on the order the filesystem hands them back.
func listedNames(entries []listing) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.name)
	}
	sort.Strings(names)
	return names
}
