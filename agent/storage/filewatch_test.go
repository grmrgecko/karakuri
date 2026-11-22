package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// watchTestContext supplies the clock and logger the watch loop reads.
type watchTestContext struct {
	nodeapi.ExecContext
	clock *nodeapi.FakeClock
}

func (c watchTestContext) Clock() nodeapi.Clock { return c.clock }
func (watchTestContext) Logger() *zap.Logger    { return zap.NewNop() }

// TestFileWatchCopySettlesAsCreate pins how a new file's burst settles. Copying
// a file in produces CREATE and then WRITE for the same path (inotify's
// IN_CREATE then IN_MODIFY). The run must report "create", and the write must
// restart the quiet window even when the node does not report writes, or a
// create-only watch fires while the file is still being written.
func TestFileWatchCopySettlesAsCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		onWrite bool
	}{
		{name: "writes reported", onWrite: true},
		{name: "writes not reported", onWrite: false},
	} {
		events := make(chan fsnotify.Event)
		watcher := &fsnotify.Watcher{Events: events, Errors: make(chan error)}
		clock := nodeapi.NewFakeClock(time.Unix(0, 0))
		emitted := make(chan *nodeapi.Msg, 4)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		onWrite := tc.onWrite
		cfg := fileWatchConfig{Pattern: "*.txt", OnWrite: &onWrite}
		go func() {
			done <- watchLoop(ctx, watchTestContext{clock: clock}, watcher, cfg, "", 500*time.Millisecond, func(m *nodeapi.Msg) { emitted <- m })
		}()

		path := filepath.Join(t.TempDir(), "copied.txt")
		// send delivers op, then an event the pattern filters out. The loop
		// takes the second only after finishing the first, so the clock is not
		// advanced while the first is still being handled.
		send := func(op fsnotify.Op) {
			events <- fsnotify.Event{Name: path, Op: op}
			events <- fsnotify.Event{Name: filepath.Join(filepath.Dir(path), "sync.log"), Op: fsnotify.Write}
		}

		// Create at 0ms and write at 400ms; quiet resumes at 400+500 = 900ms.
		send(fsnotify.Create)
		clock.Advance(400 * time.Millisecond)
		send(fsnotify.Write)
		clock.Advance(100 * time.Millisecond)
		require.Truef(t, clock.BlockUntilAfterCalls(2, time.Second), "%s: the loop must wait again for the rest of the window", tc.name)
		assert.Emptyf(t, emitted, "%s: a path written 100ms ago is not quiet", tc.name)

		clock.Advance(400 * time.Millisecond)
		select {
		case msg := <-emitted:
			assert.Equalf(t, "create", msg.Meta["event"], "%s: a new file written during its settle window is still a create", tc.name)
		case <-time.After(time.Second):
			t.Errorf("%s: the change must be emitted once the path is quiet", tc.name)
		}
		cancel()
		require.NoError(t, <-done)
	}
}

// TestFileWatchSkipsSymlinkCreatedLater verifies a recursive watch treats a
// symlinked directory created after arming the way the arming walk does: it is
// not followed. Following it let a link such as /srv/in/link -> /usr add up to
// fileWatchMaxDirs watches outside the watched tree.
func TestFileWatchSkipsSymlinkCreatedLater(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	t.Cleanup(func() { watcher.Close() })
	require.NoError(t, addWatchDirs(watcher, root, true), "the watched tree must arm")

	emitted := make(chan *nodeapi.Msg, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- watchLoop(ctx, watchTestContext{clock: nodeapi.NewFakeClock(time.Unix(0, 0))}, watcher,
			fileWatchConfig{Recursive: true}, "", 0, func(m *nodeapi.Msg) { emitted <- m })
	}()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	// awaitMarker writes a file in the root and drains messages until it is
	// reported. The loop handles events in order, so every earlier event has
	// been handled by then.
	awaitMarker := func(name string) []string {
		t.Helper()
		marker := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(marker, nil, 0o644))
		var seen []string
		deadline := time.After(5 * time.Second)
		for {
			select {
			case msg := <-emitted:
				path := msg.Payload.(map[string]any)["path"].(string)
				if path == marker {
					return seen
				}
				seen = append(seen, path)
			case <-deadline:
				require.FailNow(t, "the marker must be reported", name)
			}
		}
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("creating a symlink: %v", err)
	}
	awaitMarker("first")
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("x"), 0o644))
	assert.NotContains(t, awaitMarker("second"), filepath.Join(link, "file.txt"),
		"a file behind a symlink created after arming must not be reported")
}
