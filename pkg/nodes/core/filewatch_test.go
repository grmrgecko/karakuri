package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileWatchMetaIsATrigger pins the separation between what a node does and
// where it is drawn. Category is behaviour and Folder is palette placement, so
// a trigger can live in a domain folder — this node is the case that proves the
// two are independent, since it fires runs but belongs beside the other storage
// nodes.
func TestFileWatchMetaIsATrigger(t *testing.T) {
	meta := FileWatchNode{}.Meta()

	assert.Equal(t, flow.FolderStorage, meta.Folder, "the node belongs beside the other storage nodes in the palette")
	assert.True(t, meta.IsTrigger(), "the folder is placement only and must not stop the node counting as a trigger")

	_, ok := engine.NodeType(FileWatchNode{}).(engine.TriggerType)
	assert.True(t, ok, "a node that reports itself a trigger must implement the interface that arms one")
}

// TestFileWatchExecuteSynthesizesATestMessage covers what a test run gets from
// a watch node. An author cannot make a file change from the editor, so the
// node synthesizes a sample of the same shape a real event has — and a real
// event must pass through untouched, or a deployed flow would act on the
// sample instead of the file that actually changed.
func TestFileWatchExecuteSynthesizesATestMessage(t *testing.T) {
	withOpenStorage(t)
	dir := t.TempDir()

	// A test run arrives with no payload and must still show the deployed shape.
	ec := newTestCtx(`{"path":"`+dir+`"}`, nil)
	out, err := FileWatchNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{engine.TriggerInput: {Meta: map[string]any{"source": "test"}}})
	require.NoError(t, err, "a test run must be able to exercise the graph without a real file change")

	msg := only(out, "out")
	require.NotNil(t, msg, "a test run must produce something for the rest of the graph to work on")

	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "the sample must be an object, the same shape a real event has")
	assert.Equal(t, "write", payload["event"],
		"the sample must look like a real change, or the author writes rules against a shape that never arrives")
	assert.Equal(t, dir, payload["dir"], "the sample names the directory the node is actually watching")
	assert.Equal(t, "test", msg.Meta["source"], "the seed message's metadata must carry over")

	// A real event passes straight through.
	fired := &engine.Msg{Payload: map[string]any{"event": "create"}}
	out, err = FileWatchNode{}.Execute(context.Background(), ec,
		map[string]*engine.Msg{engine.TriggerInput: fired})
	require.NoError(t, err, "a real event must pass through the node")
	assert.Same(t, fired, only(out, "out"),
		"a real change must not be replaced by the sample, or the flow acts on invented data")
}

// TestFileWatchEmitsOnChange drives the real fsnotify watcher against real
// files in a temp directory, which is the only way to know the watch, the
// pattern, and the emit line up. The pattern is what makes the check
// meaningful: two files are written each round and only the matching one may
// come out.
func TestFileWatchEmitsOnChange(t *testing.T) {
	withOpenStorage(t)
	dir := t.TempDir()

	ec := newTestCtx(`{"path":"`+dir+`","pattern":"*.json","settle":"10ms"}`, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	emitted := make(chan *engine.Msg, 8)
	started := make(chan error, 1)
	go func() {
		started <- FileWatchNode{}.Start(ctx, ec, func(m *engine.Msg) { emitted <- m })
	}()

	// Start registers the watch on its own goroutine, so the writes are retried
	// until one is seen rather than raced against a fixed pause.
	var msg *engine.Msg
	deadline := time.Now().Add(5 * time.Second)
	for msg == nil && time.Now().Before(deadline) {
		writeTemp(t, dir, "ignored.txt", "no")
		writeTemp(t, dir, "state.json", "{}")
		select {
		case msg = <-emitted:
		case <-time.After(100 * time.Millisecond):
		}
	}
	require.NotNil(t, msg, "the watcher never reported the created file")

	payload, ok := msg.Payload.(map[string]any)
	require.True(t, ok, "a change is reported as an object")
	assert.Equal(t, "state.json", payload["name"],
		"only the file matching the pattern may be reported; ignored.txt was written each round too")
	assert.Equal(t, "file-watch", msg.Meta["source"], "the message must say which trigger produced it")
	assert.Equal(t, filepath.Join(dir, "state.json"), msg.Topic,
		"the topic is the full path, so a flow can act on the file without rebuilding it")

	cancel()
	select {
	case err := <-started:
		assert.NoError(t, err, "cancelling an armed trigger is a clean stop, not a failure")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Start did not return after cancellation, so disabling a flow would hang")
	}
}

// TestFileWatchStartRejectsAMissingPath covers arming against a path that is
// not there, beside the working watch above. It has to fail when the flow is
// enabled rather than sit silently watching nothing — an armed trigger that
// never fires looks exactly like one whose files never change.
func TestFileWatchStartRejectsAMissingPath(t *testing.T) {
	withOpenStorage(t)
	ec := newTestCtx(`{"path":"`+filepath.Join(t.TempDir(), "absent")+`"}`, nil)

	err := FileWatchNode{}.Start(context.Background(), ec, func(*engine.Msg) {})
	assert.Error(t, err,
		"arming must fail at enable time: a watcher on a missing path is indistinguishable from one that never fires")
}

// TestFileWatchSettleCoalescesABurst covers the settle window, which exists
// because writing one file produces several filesystem events — an editor
// saving a document can emit half a dozen. Without coalescing, a flow watching
// a config file would run several times per save, and a flow that copies or
// uploads the file would do it repeatedly.
//
// The watcher is driven through its real channel rather than by touching files,
// so the burst is exactly three events with no timing to race.
func TestFileWatchSettleCoalescesABurst(t *testing.T) {
	clock := engine.NewFakeClock(time.Unix(1700000000, 0))
	ec := newTestCtx("", clock)
	events := make(chan fsnotify.Event, 8)
	watcher := &fsnotify.Watcher{Events: events, Errors: make(chan error, 1)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitted := make(chan *engine.Msg, 8)
	go watchLoop(ctx, ec, watcher, fileWatchConfig{}, "", time.Second, func(m *engine.Msg) { emitted <- m })

	// Three writes to the same file inside the settle window are one change.
	for i := 0; i < 3; i++ {
		events <- fsnotify.Event{Name: "/tmp/burst.txt", Op: fsnotify.Write}
	}
	require.True(t, clock.BlockUntilAfterCalls(1, 2*time.Second),
		"the first event must open a settle window on the injectable clock")

	select {
	case msg := <-emitted:
		require.FailNowf(t, "a message went out before the settle window elapsed",
			"the whole point is to wait for the writes to stop: %v", msg.Payload)
	case <-time.After(20 * time.Millisecond):
	}

	clock.Advance(time.Second)
	select {
	case msg := <-emitted:
		payload, ok := msg.Payload.(map[string]any)
		require.True(t, ok, "a change is reported as an object")
		assert.Equal(t, "write", payload["event"], "the coalesced message reports the write that happened")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "nothing was emitted after the settle window, so the burst was swallowed entirely")
	}

	select {
	case msg := <-emitted:
		assert.Fail(t, "the burst produced more than one message",
			"three writes to one file inside the window are one change: %v", msg.Payload)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestFileWatchCopySettlesAsCreate pins how a new file's burst settles. A file
// copied in arrives as CREATE and then WRITE on the same path, and a flow that
// switches on the event name has to see "create" for it.
func TestFileWatchCopySettlesAsCreate(t *testing.T) {
	clock := engine.NewFakeClock(time.Unix(1700000000, 0))
	ec := newTestCtx("", clock)
	events := make(chan fsnotify.Event)
	watcher := &fsnotify.Watcher{Events: events, Errors: make(chan error, 1)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitted := make(chan *engine.Msg, 8)
	go watchLoop(ctx, ec, watcher, fileWatchConfig{}, "", time.Second, func(m *engine.Msg) { emitted <- m })

	events <- fsnotify.Event{Name: "/tmp/copied.txt", Op: fsnotify.Create}
	events <- fsnotify.Event{Name: "/tmp/copied.txt", Op: fsnotify.Write}
	// The loop takes this unreported event only after it has handled the
	// write, so the clock does not move while the write is in flight.
	events <- fsnotify.Event{Name: "/tmp/other.txt", Op: fsnotify.Chmod}
	require.True(t, clock.BlockUntilAfterCalls(1, 2*time.Second),
		"the first event must open a settle window on the injectable clock")

	clock.Advance(time.Second)
	select {
	case msg := <-emitted:
		payload, ok := msg.Payload.(map[string]any)
		require.True(t, ok, "a change is reported as an object")
		assert.Equal(t, "create", payload["event"], "a new file written during its settle window is still a create")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "nothing was emitted after the settle window")
	}
}

// TestFileWatchMatchesFilters covers which events reach a flow. The per-event
// switches default off, so a node that only asked for chmod does not also fire
// on renames — the "default off" row is what pins that, and it is the rule that
// keeps a watch from firing on filesystem noise nobody asked about.
func TestFileWatchMatchesFilters(t *testing.T) {
	off, on := false, true
	cfg := fileWatchConfig{Pattern: "*.json", OnChmod: &on, OnWrite: &off}

	cases := []struct {
		name  string
		only  string
		event fsnotify.Event
		want  bool
	}{
		{"pattern match", "", fsnotify.Event{Name: "/data/a.json", Op: fsnotify.Create}, true},
		{"pattern miss", "", fsnotify.Event{Name: "/data/a.txt", Op: fsnotify.Create}, false},
		{"disabled event", "", fsnotify.Event{Name: "/data/a.json", Op: fsnotify.Write}, false},
		{"enabled event", "", fsnotify.Event{Name: "/data/a.json", Op: fsnotify.Chmod}, true},
		{"default off", "", fsnotify.Event{Name: "/data/a.json", Op: fsnotify.Rename}, false},
		{"single file", "a.json", fsnotify.Event{Name: "/data/a.json", Op: fsnotify.Create}, true},
		{"other file", "a.json", fsnotify.Event{Name: "/data/b.json", Op: fsnotify.Create}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, fileWatchMatches(cfg, tc.only, tc.event),
				"the event must be judged by the pattern, the per-event switches, and the single-file filter together")
		})
	}
}
