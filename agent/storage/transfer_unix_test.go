//go:build !windows

package storage

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// TestCopyRefusesSpecialFiles locks that a copy fails on a FIFO instead of
// opening it. Opening a FIFO for reading blocks until a writer appears, so a
// tree holding one hung the copy for good, with its staging directory left
// behind.
func TestCopyRefusesSpecialFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeTree(t, source, map[string]string{"plain.txt": "text"})
	require.NoError(t, syscall.Mkfifo(filepath.Join(source, "queue"), 0o600), "the test needs a FIFO in the tree")

	done := make(chan error, 1)
	go func() {
		_, err := copyPath(context.Background(), source, filepath.Join(root, "copy"))
		done <- err
	}()
	select {
	case err := <-done:
		assert.ErrorContains(t, err, "queue", "the error names the entry that cannot be copied")
	case <-time.After(5 * time.Second):
		// Release the blocked open so the goroutine does not outlive the test.
		if w, err := os.OpenFile(filepath.Join(source, "queue"), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("copying a tree with a FIFO in it must fail, not wait for a writer")
	}
}

// TestTransferRemovesReadOnlyStagingAndBackup locks that a transfer leaves no
// hidden staging or backup tree beside the destination when the tree holds a
// read-only directory. Such a directory cannot have its children unlinked, so
// a refused copy left its whole staged copy behind, and a repeated replacing
// copy failed after installing because the prior destination stayed as a backup.
func TestTransferRemovesReadOnlyStagingAndBackup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes the children of a read-only directory, so nothing is left to find")
	}
	for _, tc := range []struct {
		name      string
		overwrite bool
		wantErr   string
		want      string
	}{
		{name: "replacing an earlier copy", overwrite: true, want: "new"},
		{name: "refused by an existing destination", wantErr: "already exists", want: "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// An existing directory receives the source under its own name.
			source := filepath.Join(root, "copy")
			destination := filepath.Join(root, "target", "copy")
			writeTree(t, source, map[string]string{"locked/id.txt": "new"})
			writeTree(t, destination, map[string]string{"locked/id.txt": "old"})
			locked := []string{filepath.Join(source, "locked"), filepath.Join(destination, "locked")}
			for _, dir := range locked {
				require.NoError(t, os.Chmod(dir, 0o555), "the test needs read-only directories on both sides")
			}
			t.Cleanup(func() {
				// TempDir cannot remove the children of a read-only directory.
				_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
					if err == nil && d.IsDir() {
						_ = os.Chmod(path, 0o755)
					}
					return nil
				})
			})

			ec := transferTestContext{cfg: transferConfig{Source: source, Destination: filepath.Dir(destination), Overwrite: tc.overwrite}}
			_, err := runTransfer(context.Background(), ec, map[string]*nodeapi.Msg{"in": {}}, false)
			if tc.wantErr == "" {
				require.NoError(t, err, "A replacing copy over a tree with a read-only directory must succeed.")
			} else {
				require.ErrorContains(t, err, tc.wantErr, "The copy must be refused for the destination, not for cleanup.")
			}

			content, readErr := os.ReadFile(filepath.Join(destination, "locked", "id.txt"))
			require.NoError(t, readErr, "The destination must hold a complete tree either way.")
			assert.Equal(t, tc.want, string(content), "The destination holds the tree the outcome names.")

			entries, readErr := os.ReadDir(filepath.Dir(destination))
			require.NoError(t, readErr)
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			assert.Equal(t, []string{"copy"}, names, "No staging or backup tree may remain beside the destination.")
		})
	}
}

// TestReplacementRefusesSpecialFiles locks that a replacing write leaves a
// FIFO in place. The staged file was renamed over the pipe, so Write File
// reported success, the name became a regular file, and the program reading
// the pipe never received the payload. A regular file beside it is still
// replaced.
func TestReplacementRefusesSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "control.fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600), "the test needs a FIFO")
	plain := filepath.Join(dir, "state.txt")
	require.NoError(t, os.WriteFile(plain, []byte("old"), 0o600), "the test needs a regular file")

	err := writeGuarded(context.Background(), fifo, []byte("pause"), 0o644, true, false, "")
	require.Error(t, err, "a replacing write to a FIFO must be refused")
	assert.Contains(t, err.Error(), "append mode", "the error names the mode that writes into a pipe")
	info, err := os.Lstat(fifo)
	require.NoError(t, err, "the FIFO must still exist")
	assert.NotZero(t, info.Mode()&os.ModeNamedPipe, "the path is still a named pipe")

	require.NoError(t, writeGuarded(context.Background(), plain, []byte("new"), 0o644, true, false, ""),
		"a regular file in the same directory is replaced")
	data, err := os.ReadFile(plain)
	require.NoError(t, err, "the replaced file must be readable")
	assert.Equal(t, "new", string(data), "the regular file holds the new content")
}

// TestSymlinkedDirectoryIsWalked locks that List Files and a recursive Watch
// Files descend into a directory named through a symlink, which is how /tmp,
// /var, and /etc are named on macOS. filepath.WalkDir does not follow a root
// that is a link, so the listing was empty and only the top level was watched.
// Reported paths keep the linked spelling the node was configured with.
func TestSymlinkedDirectoryIsWalked(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	writeTree(t, real, map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(real, link), "the test needs a link to the directory")

	for _, dir := range []string{real, link} {
		entries, err := walkListing(context.Background(), dir, fileListConfig{Recursive: true})
		require.NoErrorf(t, err, "listing %s must succeed", dir)
		var paths []string
		for _, entry := range entries {
			paths = append(paths, entry.value["path"].(string))
		}
		assert.ElementsMatchf(t, []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "sub", "b.txt")}, paths,
			"listing %s reports both files under the path it was given", dir)
	}

	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err, "the test needs a file watcher")
	defer watcher.Close()
	require.NoError(t, addWatchDirs(watcher, link, true), "a linked directory must be watchable")
	require.NoError(t, os.WriteFile(filepath.Join(real, "sub", "new.txt"), []byte("x"), 0o644),
		"the test needs a change one level down")
	select {
	case event := <-watcher.Events:
		assert.Equal(t, filepath.Join(link, "sub", "new.txt"), event.Name,
			"a change in a subdirectory is reported under the linked path")
	case <-time.After(2 * time.Second):
		assert.Fail(t, "a recursive watch on a linked directory must cover its subdirectories")
	}
}

// TestCopyKeepsModeUnderUmask locks that a copied file takes its source's
// permission bits whatever the agent's umask is. The copy was created with
// the source mode as the open(2) mode, which the kernel masks: under the
// usual 022 a group-writable 0664 file arrived as 0644, while a directory
// beside it kept its mode exactly.
func TestCopyKeepsModeUnderUmask(t *testing.T) {
	previous := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(previous) })

	root := t.TempDir()
	source := filepath.Join(root, "shared.txt")
	require.NoError(t, os.WriteFile(source, []byte("text"), 0o600), "the test needs a source file")
	require.NoError(t, os.Chmod(source, 0o664), "the test needs a group-writable source")

	destination := filepath.Join(root, "copy.txt")
	_, err := copyPath(context.Background(), source, destination)
	require.NoError(t, err, "copying a regular file must succeed")

	info, err := os.Stat(destination)
	require.NoError(t, err, "the copy must exist")
	assert.Equal(t, os.FileMode(0o664), info.Mode().Perm(), "the copy keeps the group write bit the umask would clear")
}

// TestWriteReplacementKeepsGroup locks that replacing a file keeps the group
// it belonged to. The replacement is a new file renamed into place, so it
// took the agent's primary group: a root agent rewriting a 0640 root:www-data
// configuration file left it root:root, and the service reading it through
// the group lost access. The test uses a supplementary group because changing
// the owner needs root.
func TestWriteReplacementKeepsGroup(t *testing.T) {
	groups, err := os.Getgroups()
	require.NoError(t, err, "the test needs the account's groups")
	group := -1
	for _, gid := range groups {
		if gid != os.Getegid() {
			group = gid
			break
		}
	}
	if group < 0 {
		t.Skip("the account has no supplementary group to give the file")
	}

	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o640), "the test needs an existing file")
	require.NoError(t, os.Chown(path, -1, group), "the test needs the file in a supplementary group")

	require.NoError(t, writeReplacement(path, []byte("new"), 0o644), "the replacement must succeed")

	info, err := os.Stat(path)
	require.NoError(t, err, "the replaced file must have metadata")
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok, "the platform reports ownership")
	assert.Equal(t, uint32(group), stat.Gid, "the replaced file stays in the group the old one was in")
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "the mode is kept beside the group")
}
