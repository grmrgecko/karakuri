package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withStorageConfig installs a storage configuration for one test.
func withStorageConfig(t *testing.T, cfg config.StorageConfig) {
	t.Helper()
	prev := config.C
	config.C = &config.Config{Storage: cfg}
	t.Cleanup(func() { config.C = prev })
}

// withOpenStorage lifts the storage confinement for one test, the way an
// operator does with storage.roots: ["/"]. Tests about the confinement itself
// set their own roots instead.
func withOpenStorage(t *testing.T) {
	t.Helper()
	withStorageConfig(t, config.StorageConfig{Roots: []string{"/"}})
}

// writeTemp creates a file with the given content and returns its path.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoErrorf(t, os.WriteFile(path, []byte(content), 0o644), "the test needs %s to exist", path)
	return path
}

// TestFileReadFormats covers the three shapes a file can arrive in, against a
// real file on disk. The format is declared rather than sniffed, which is the
// rule that matters: the same bytes come back as text, as an array of lines, or
// as real bytes purely because the author said which, so nothing downstream has
// to guess what it was handed.
func TestFileReadFormats(t *testing.T) {
	withOpenStorage(t)
	dir := t.TempDir()
	path := writeTemp(t, dir, "notes.txt", "first\nsecond\n")

	t.Run("text", func(t *testing.T) {
		ec := newTestCtx(`{"path":"`+path+`"}`, nil)
		out, err := FileReadNode{}.Execute(context.Background(), ec, nil)
		require.NoError(t, err, "reading an existing file must succeed")

		msg := only(out, "out")
		assert.Equal(t, "first\nsecond\n", msg.Payload, "the default format is the file's whole text, newlines included")
		assert.Equal(t, path, msg.Meta["path"], "the path belongs in metadata so a flow can act on the file it read")
		assert.Equal(t, 13, msg.Meta["size"], `"first\nsecond\n" is 13 bytes`)
	})

	t.Run("lines", func(t *testing.T) {
		ec := newTestCtx(`{"path":"`+path+`","format":"lines"}`, nil)
		out, err := FileReadNode{}.Execute(context.Background(), ec, nil)
		require.NoError(t, err, "reading as lines must succeed")
		assert.Equal(t, []any{"first", "second"}, only(out, "out").Payload,
			"the trailing newline ends the second line rather than starting a third, empty one")
	})

	// The format promises an array. A nil slice would encode as null in run
	// history, scripts, and stored waits.
	t.Run("lines of a file with no lines are an empty array", func(t *testing.T) {
		for name, content := range map[string]string{"empty": "", "only a newline": "\n"} {
			blank := writeTemp(t, t.TempDir(), "queue.txt", content)
			ec := newTestCtx(`{"path":"`+blank+`","format":"lines"}`, nil)
			out, err := FileReadNode{}.Execute(context.Background(), ec, nil)
			require.NoErrorf(t, err, "%s: a file with no lines is readable", name)
			assert.Equalf(t, []any{}, only(out, "out").Payload, "%s: no lines is [], not null", name)
		}
	})

	// Binary is handed downstream as bytes. Encoding it as text is a base64
	// node's job, which is what lets the port say it carries binary.
	t.Run("binary", func(t *testing.T) {
		binary := writeTemp(t, dir, "blob.bin", "\x00\x01\x02")
		ec := newTestCtx(`{"path":"`+binary+`","format":"binary"}`, nil)
		out, err := FileReadNode{}.Execute(context.Background(), ec, nil)
		require.NoError(t, err, "reading as binary must succeed")

		got, ok := only(out, "out").Payload.([]byte)
		require.Truef(t, ok, "payload is %T, want real bytes", only(out, "out").Payload)
		assert.Equal(t, []byte{0x00, 0x01, 0x02}, got,
			"binary must arrive as bytes; encoding it as text is a base64 node's job, which is what lets the port say binary")
	})
}

// TestFileReadMissingFile covers a file that is not there, both ways. Failing
// is the default because a flow reading a file it expected usually cannot
// continue without it; allow_missing is for the case where absence is
// meaningful, and it has to be reported in metadata so the graph can branch on
// it rather than guess from a nil payload.
func TestFileReadMissingFile(t *testing.T) {
	withOpenStorage(t)
	path := filepath.Join(t.TempDir(), "absent.txt")

	ec := newTestCtx(`{"path":"`+path+`"}`, nil)
	_, err := FileReadNode{}.Execute(context.Background(), ec, nil)
	require.Error(t, err, "by default a file that is not there fails the node onto its error branch")

	ec = newTestCtx(`{"path":"`+path+`","allow_missing":true}`, nil)
	out, err := FileReadNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err, "allow_missing turns absence into information rather than a failure")

	msg := only(out, "out")
	assert.Nil(t, msg.Payload, "there is nothing to read, so the payload is nil rather than an empty string")
	assert.Equal(t, true, msg.Meta["file_missing"],
		"absence must be stated in metadata so a graph can branch on it, not inferred from a nil payload")
}

// TestFileReadRejectsDirectoryAndOversizedFile covers the two reads the node
// refuses. The size cap is the operational one: a flow pointed at a log file
// that has grown to gigabytes would otherwise pull the whole thing into memory
// and take the server down with it.
func TestFileReadRejectsDirectoryAndOversizedFile(t *testing.T) {
	dir := t.TempDir()
	withStorageConfig(t, config.StorageConfig{Roots: []string{"/"}, MaxReadSize: 4})

	ec := newTestCtx(`{"path":"`+dir+`"}`, nil)
	_, err := FileReadNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err, "a directory has no contents to read as a payload")

	// Twenty bytes against a four-byte cap.
	path := writeTemp(t, dir, "big.txt", "more than four bytes")
	ec = newTestCtx(`{"path":"`+path+`"}`, nil)
	_, err = FileReadNode{}.Execute(context.Background(), ec, nil)
	assert.Error(t, err,
		"a file over max_read_size must be refused rather than pulled into memory whole")
}

// TestFileReadPathIsInterpolated covers a templated path. The node is an
// output-only pure source with no input message, so there is no payload to
// render against — the segments read the variable scopes instead, which is what
// makes a per-room or per-device path possible at all.
func TestFileReadPathIsInterpolated(t *testing.T) {
	withOpenStorage(t)
	dir := t.TempDir()
	writeTemp(t, dir, "kitchen.txt", "22.5")

	ec := newTestCtx(`{"path":"`+dir+`/{{ global.room }}.txt"}`, nil)
	ec.globalVars["room"] = "kitchen"
	out, err := FileReadNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err, "the interpolated path must resolve to the file that exists")
	assert.Equal(t, "22.5", only(out, "out").Payload,
		"the global variable must fill the path, or a per-room file cannot be addressed")
}

// TestFileWriteModes covers the three write modes in sequence against one real
// path. Overwrite creates missing parent directories, so a flow writing to a
// new location does not have to prepare it first; append adds rather than
// replaces; and deleting something already gone succeeds, because a delete that
// failed on an absent file would make cleanup flows fail every run after the
// first.
func TestFileWriteModes(t *testing.T) {
	withOpenStorage(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "log.txt")

	// Overwrite creates the missing directory and adds a trailing newline.
	ec := newTestCtx(`{"path":"`+path+`"}`, nil)
	in := &engine.Msg{Payload: "one"}
	out, err := FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "writing into a directory that does not exist yet must create it")

	// "one" plus the trailing newline the node adds.
	assert.Equal(t, 4, only(out, "out").Meta["bytes"], "the byte count must include the newline that was written")
	assert.Equal(t, "one\n", readFile(t, path), "the file is re-read from disk rather than trusted from the return value")

	// Appending adds to what is there.
	ec = newTestCtx(`{"path":"`+path+`","mode":"append"}`, nil)
	_, err = FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: "two"}})
	require.NoError(t, err, "appending to an existing file must succeed")
	assert.Equal(t, "one\ntwo\n", readFile(t, path), "appending must add to the file rather than replace it")

	// Deleting removes it, and deleting again still succeeds.
	ec = newTestCtx(`{"path":"`+path+`","mode":"delete"}`, nil)
	out, err = FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "deleting an existing file must succeed")
	assert.Equal(t, true, only(out, "out").Meta["deleted"], "the flow needs to be told the delete happened")

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "the file must actually be gone from disk, got %v", err)

	_, err = FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	assert.NoError(t, err,
		"deleting an absent file must succeed, or a cleanup flow fails on every run after the first")
}

// TestFileWriteContentShapes covers how a payload becomes file content. The
// interesting rule is the newline: text gets one appended so a log file is
// readable, and binary does not, because bytes are not lines and an extra one
// corrupts the file. The last case is the boundary — a property that is not on
// the message is a configuration mistake, and writing an empty file for it
// would destroy whatever was there.
func TestFileWriteContentShapes(t *testing.T) {
	withOpenStorage(t)
	dir := t.TempDir()

	// Objects are written as JSON.
	path := filepath.Join(dir, "state.json")
	ec := newTestCtx(`{"path":"`+path+`","newline":false}`, nil)
	in := &engine.Msg{Payload: map[string]any{"on": true}}
	_, err := FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "writing an object must succeed")

	var decoded map[string]any
	written := readFile(t, path)
	require.NoErrorf(t, json.Unmarshal([]byte(written), &decoded), "an object must be written as JSON: %q", written)
	assert.Equal(t, true, decoded["on"], "the written JSON must hold what the payload did")

	// Binary is written as its bytes, with no trailing newline added: bytes
	// are not lines, and one would corrupt the file.
	binary := filepath.Join(dir, "blob.bin")
	ec = newTestCtx(`{"path":"`+binary+`","property":"payload.data"}`, nil)
	in = &engine.Msg{Payload: map[string]any{"data": []byte("\x00\x01\x02")}}
	_, err = FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "writing bytes must succeed")
	assert.Equal(t, "\x00\x01\x02", readFile(t, binary),
		"bytes are written exactly, with no trailing newline: bytes are not lines and one would corrupt the file")

	ec = newTestCtx(`{"path":"`+filepath.Join(dir, "missing.txt")+`","property":"payload.absent"}`, nil)
	_, err = FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": in})
	assert.Error(t, err,
		"a property that is not on the message is a configuration mistake; writing an empty file would destroy the target")
}

// TestStorageRootsConfinesPaths covers the confinement that makes storage nodes
// safe to expose to flow authors: the storage nodes are the only filesystem
// access karakuri has, and storage.roots is what keeps a flow from reading
// /etc/shadow. The three refusals are three ways out of a root — naming a path
// outside it, traversing up with .., and following a symlink planted inside —
// and each has to be closed independently, since closing only the first two is
// the classic mistake.
func TestStorageRootsConfinesPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	withStorageConfig(t, config.StorageConfig{Roots: []string{root}})

	_, err := checkFilePath(filepath.Join(root, "sub", "file.txt"))
	assert.NoError(t, err, "a path under a configured root must be allowed, or the feature is unusable")

	_, err = checkFilePath(filepath.Join(outside, "file.txt"))
	assert.Error(t, err, "a path outside every root must be refused")

	_, err = checkFilePath(filepath.Join(root, "..", "escaped.txt"))
	assert.Error(t, err, "the path must be cleaned before the check, or .. climbs straight out of the root")

	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = checkFilePath(filepath.Join(link, "file.txt"))
	assert.Error(t, err,
		"the path must be resolved before the check: a symlink inside the root is the way past a purely textual test")
}

// TestStorageRootsConfinesDanglingSymlink locks that a link inside a root whose
// target does not exist yet is checked against where it points. Such a link
// does not resolve, so the check fell back to the link's own directory and
// allowed it, and the write then followed the link and created the file
// outside every root.
func TestStorageRootsConfinesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	withStorageConfig(t, config.StorageConfig{Roots: []string{root}})

	tests := []struct {
		name   string
		target string
		create string
		ok     bool
	}{
		{name: "an absolute target outside the root", target: filepath.Join(outside, "absolute.txt"), create: filepath.Join(outside, "absolute.txt")},
		{name: "a relative target outside the root", target: filepath.Join("..", filepath.Base(outside), "relative.txt"), create: filepath.Join(outside, "relative.txt")},
		{name: "a target inside the root", target: "inside.txt", create: filepath.Join(root, "inside.txt"), ok: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link := filepath.Join(root, "link"+string(rune('a'+i)))
			if err := os.Symlink(tt.target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			ec := newTestCtx(`{"path":"`+link+`"}`, nil)
			_, err := FileWriteNode{}.Execute(context.Background(), ec, map[string]*engine.Msg{"in": {Payload: "data"}})
			_, statErr := os.Stat(tt.create)
			if tt.ok {
				require.NoError(t, err, "A link that stays inside the root must still be writable.")
				assert.NoError(t, statErr, "The write must create the file the link names.")
				return
			}
			assert.Error(t, err, "A link pointing outside every root must be refused before the write.")
			assert.True(t, os.IsNotExist(statErr), "Nothing may be created outside the root.")
		})
	}
}

// TestStorageRootsEmptyRefusesEveryPath pins the direction the check fails in.
// An empty roots list used to mean "no confinement" and now means the file
// nodes are off, so the one policy deciding whether a flow can read /etc/shadow
// cannot be opened by leaving a config key out. config.Load never produces an
// empty list on its own: unset resolves to the data directory, and only an
// operator writing roots: [] reaches this.
func TestStorageRootsEmptyRefusesEveryPath(t *testing.T) {
	withStorageConfig(t, config.StorageConfig{})

	_, err := checkFilePath(filepath.Join(t.TempDir(), "file.txt"))
	assert.Error(t, err, "with no roots configured every path is out of bounds, including one the account could read")

	_, err = checkFilePath("  ")
	assert.Error(t, err, "whitespace names no file and must be refused whatever the roots are")
}

// readFile returns a file's content for assertions.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoErrorf(t, err, "the written file must be readable back from disk: %s", path)
	return string(data)
}
