package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTailReaderFollowsRotation pins the behavior that separates a log tail
// from reading a file: logrotate either renames the file and creates a new one
// or truncates it in place (copytruncate), and both leave a reader holding an
// offset past the end of what it should now be reading. A tail that missed
// either case would go silent after the first nightly rotation, which is the
// worst possible time to stop reporting.
func TestTailReaderFollowsRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("first\nsecond\n"), 0o644),
		"the test needs a log file that already has content")

	// A tail defaults to the end, so lines written before it attached are not
	// replayed. Only the complete line is reported; the partial one waits.
	tail := &tailReader{path: path, maxLine: 1 << 20}
	defer tail.close()
	lines, err := tail.read()
	require.NoError(t, err, "attaching to an existing file must succeed")
	assert.Empty(t, lines, "a tail starting at the end does not replay what is already there")

	appendLine(t, path, "third\npartial")
	lines, err = tail.read()
	require.NoError(t, err, "reading appended content must succeed")
	assert.Equal(t, []string{"third"}, lines,
		"a line without its newline yet is held, or a flow would act on half a log entry")

	appendLine(t, path, " finished\n")
	lines, err = tail.read()
	require.NoError(t, err, "reading the rest of the held line must succeed")
	assert.Equal(t, []string{"partial finished"}, lines, "the held partial line is completed, not duplicated")

	// Rename and recreate, which is what logrotate does by default. The new
	// file is read from its start, since none of its lines have been seen.
	require.NoError(t, os.Rename(path, path+".1"), "the test needs the rotation to happen")
	require.NoError(t, os.WriteFile(path, []byte("after rotation\n"), 0o644), "rotation creates a new file")
	lines, err = tail.read()
	require.NoError(t, err, "the tail must survive its file being renamed away")
	assert.Equal(t, []string{"after rotation"}, lines, "the replacement file is read from the beginning")

	appendLine(t, path, "second after rotation\n")
	lines, err = tail.read()
	require.NoError(t, err, "reading from the replacement file must succeed")
	assert.Equal(t, []string{"second after rotation"}, lines, "the tail keeps following the new file")

	// Truncate in place and write again, which is what logrotate's copytruncate
	// does: same inode, and a file now shorter than the reader's offset.
	require.NoError(t, os.Truncate(path, 0), "copytruncate empties the file it keeps")
	appendLine(t, path, "after truncate\n")
	lines, err = tail.read()
	require.NoError(t, err, "the tail must survive its file being truncated")
	assert.Equal(t, []string{"after truncate"}, lines, "a file shorter than the offset is read again from the start")
}

// TestTailReaderReadsFileCreatedAfterArming locks that a tail armed on a path
// with no file reports everything the file holds once it appears. Skipping to
// the end is only for content that predates the tail; applying it to a file
// created afterwards dropped every line its writer got in before the next poll.
func TestTailReaderReadsFileCreatedAfterArming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	tail := &tailReader{path: path, maxLine: 1 << 20}
	defer tail.close()

	lines, err := tail.read()
	require.NoError(t, err, "a missing file is waited for, not reported as a failure")
	assert.Empty(t, lines, "there is nothing to report before the file exists")

	require.NoError(t, os.WriteFile(path, []byte("started\nlistening\n"), 0o644),
		"the test needs the writer to create its log between two polls")
	lines, err = tail.read()
	require.NoError(t, err, "attaching to the new file must succeed")
	assert.Equal(t, []string{"started", "listening"}, lines,
		"lines written before the tail noticed the new file are still new lines")
}

// TestTailReaderResumesAtCompleteLineBoundary protects the checkpoint rule
// that a trailing partial line must be replayed after restart. Saving the raw
// read offset would silently discard the beginning of that line.
func TestTailReaderResumesAtCompleteLineBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("complete\npartial"), 0o644),
		"the test needs one complete line and one partial line")

	tail := &tailReader{path: path, fromStart: true, maxLine: 1 << 20}
	lines, err := tail.read()
	require.NoError(t, err, "the first reader must consume the available bytes")
	assert.Equal(t, []string{"complete"}, lines)
	checkpoint := tail.savedPosition(time.Now())
	tail.close()

	appendLine(t, path, " finished\n")
	resumed := &tailReader{path: path, maxLine: 1 << 20, checkpoint: checkpoint}
	defer resumed.close()
	lines, err = resumed.read()
	require.NoError(t, err, "the replacement reader must resume from its checkpoint")
	assert.Equal(t, []string{"partial finished"}, lines,
		"restart must not lose the partial line that preceded the saved offset")
}

// TestTailReaderReadsReplacementCreatedWhileStopped pins the identity half of
// the checkpoint. A new file at the same path is a rotation, so its existing
// lines are new even when the original tail started at the end.
func TestTailReaderReadsReplacementCreatedWhileStopped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644), "the original log must exist")

	tail := &tailReader{path: path, maxLine: 1 << 20}
	_, err := tail.read()
	require.NoError(t, err, "the first reader must attach")
	checkpoint := tail.savedPosition(time.Now())
	tail.close()

	require.NoError(t, os.Rename(path, path+".1"), "the original log must rotate")
	require.NoError(t, os.WriteFile(path, []byte("written while stopped\n"), 0o644),
		"the replacement log must exist before restart")

	resumed := &tailReader{path: path, maxLine: 1 << 20, checkpoint: checkpoint}
	defer resumed.close()
	lines, err := resumed.read()
	require.NoError(t, err, "the replacement reader must attach to the new identity")
	assert.Equal(t, []string{"written while stopped"}, lines,
		"a replacement file is read from the beginning after an offline rotation")
}

// TestTailReaderBoundsBatchesAndLongLines covers both backlog safeguards: one
// poll cannot emit an unbounded number of runs, and one unterminated line
// cannot grow memory without limit.
func TestTailReaderBoundsBatchesAndLongLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644), "the backlog must be created")
	tail := &tailReader{path: path, fromStart: true, maxLine: 1 << 20, maxLines: 2}
	defer tail.close()

	lines, err := tail.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, lines)
	lines, err = tail.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"three"}, lines)

	longPath := filepath.Join(dir, "long.log")
	require.NoError(t, os.WriteFile(longPath, []byte("abcdef"), 0o644), "the long line must be created")
	long := &tailReader{path: longPath, fromStart: true, maxLine: 4}
	defer long.close()
	lines, err = long.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"abcd"}, lines)
	appendLine(t, longPath, "gh\nnext\n")
	lines, err = long.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"next"}, lines, "the oversized line's remainder must not become another event")
}

// TestTailReaderDetectsRewrites pins the copytruncate cases a size check
// cannot see. A seven-byte file rewritten on the same inode with a longer line,
// or truncated and refilled past the old offset between polls, used to read as
// an append and skip the first seven bytes. The same holds for a rewrite while
// the agent is stopped, which keeps the checkpoint's file identity.
func TestTailReaderDetectsRewrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644), "the log must exist")

	tail := &tailReader{path: path, fromStart: true, maxLine: 1 << 20}
	lines, err := tail.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"before"}, lines)

	require.NoError(t, os.WriteFile(path, []byte("rewritten in place\n"), 0o644), "the rewrite must keep the inode")
	lines, err = tail.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"rewritten in place"}, lines, "a rewrite longer than the old offset must be read whole")

	require.NoError(t, os.Truncate(path, 0), "copytruncate empties the file")
	appendLine(t, path, "refilled past the offset\nsecond\n")
	lines, err = tail.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"refilled past the offset", "second"}, lines,
		"truncation followed by regrowth before the next poll must not skip the new prefix")
	checkpoint := tail.savedPosition(time.Now())
	tail.close()

	require.NoError(t, os.WriteFile(path, []byte("rewritten while the agent was stopped\n"), 0o644),
		"the offline rewrite must keep the inode")
	resumed := &tailReader{path: path, maxLine: 1 << 20, checkpoint: checkpoint}
	defer resumed.close()
	lines, err = resumed.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"rewritten while the agent was stopped"}, lines,
		"a checkpoint whose bytes no longer match must restart at the beginning")
}

// TestTailReaderBatchesAcrossRotation keeps max_lines_per_poll a limit on the
// whole poll. With a two-line budget, a five-line rotated file and a
// three-line replacement used to return four lines in one poll and drop the
// old fifth line. Every poll stays within two lines and nothing is lost: the
// old file's 5 lines and the new file's 3 arrive as 2+2+2+2.
func TestTailReaderBatchesAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(path, []byte("o1\no2\no3\no4\no5\n"), 0o644), "the old log must exist")
	tail := &tailReader{path: path, fromStart: true, maxLine: 1 << 20, maxLines: 2}
	defer tail.close()

	lines, err := tail.read()
	require.NoError(t, err)
	got := lines
	require.NoError(t, os.Rename(path, path+".1"), "the log must rotate")
	require.NoError(t, os.WriteFile(path, []byte("n1\nn2\nn3\n"), 0o644), "the replacement must exist")
	for range 4 {
		lines, err = tail.read()
		require.NoError(t, err)
		assert.LessOrEqual(t, len(lines), 2, "one poll must not exceed the line budget across files")
		got = append(got, lines...)
	}
	assert.Equal(t, []string{"o1", "o2", "o3", "o4", "o5", "n1", "n2", "n3"}, got,
		"the rotated file must be drained before the replacement is read")
}

// appendLine adds text to an existing file the way a program writing a log
// does, with the file opened for append rather than rewritten.
func appendLine(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoErrorf(t, err, "the test needs to append to %s", path)
	_, err = f.WriteString(text)
	require.NoError(t, err, "the appended text must reach the file")
	require.NoError(t, f.Close(), "the file must be closed for the tail to see a complete write")
}

// TestTailReaderCheckpointsRewriteSeenOnlyAtShutdown locks that a checkpoint
// saved after logrotate copytruncate resumes at the start of the file when no
// poll ran between the truncation and the save. The saved fingerprint was
// taken from the file as it stood at shutdown, so a writer that had already
// logged past the old offset made the new content look like the content that
// was read, and the restart skipped the first lines of the new file.
func TestTailReaderCheckpointsRewriteSeenOnlyAtShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	require.NoError(t, os.WriteFile(path, []byte("2026-11-20T00:00:01 old one\n2026-11-20T00:00:02 old two\n"), 0o644),
		"the test needs a log with two entries")

	tail := &tailReader{path: path, fromStart: true, maxLine: 1 << 20}
	lines, err := tail.read()
	require.NoError(t, err, "the first poll must read the existing entries")
	require.Len(t, lines, 2, "both existing entries are read before the rotation")

	// The old offset is 56 bytes (two 28-byte lines). Three 28-byte entries
	// put 84 bytes in the truncated file, past that offset.
	want := []string{"2026-11-21T00:00:01 new one", "2026-11-21T00:00:02 new two", "2026-11-21T00:00:03 new end"}
	require.NoError(t, os.Truncate(path, 0), "copytruncate empties the file in place")
	for _, line := range want {
		appendLine(t, path, line+"\n")
	}

	checkpoint := tail.savedPosition(time.Now())
	tail.close()
	require.NotNil(t, checkpoint, "a tail that read the file must save a position")

	resumed := &tailReader{path: path, maxLine: 1 << 20, checkpoint: checkpoint}
	defer resumed.close()
	lines, err = resumed.read()
	require.NoError(t, err, "the restarted reader must resume from its checkpoint")
	assert.Equal(t, want, lines, "a file rewritten before the save is read from its beginning after the restart")
}
