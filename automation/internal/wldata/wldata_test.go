//go:build !windows && !darwin

package wldata

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestSelectionTransfersGiveUpOnStuckPeer locks the bound on a selection
// transfer. Go honors a file deadline only on a non-blocking descriptor
// (os.NewFile registers one with the poller and reports ErrNoDeadline for the
// rest), and both pipes here were blocking, so the five-second deadlines were
// never armed: a frozen application owning the clipboard hung every read, and
// a paster that stopped reading hung the goroutine holding the selection.
func TestSelectionTransfersGiveUpOnStuckPeer(t *testing.T) {
	t.Run("a read ends when the owner never finishes writing", func(t *testing.T) {
		read, writeFD, err := selectionPipe()
		require.NoError(t, err, "The transfer pipe must be created.")
		// Closing the write end frees a reader still blocked when the test ends.
		defer unix.Close(writeFD)
		defer read.Close()

		flags, err := unix.FcntlInt(uintptr(writeFD), unix.F_GETFL, 0)
		require.NoError(t, err, "The write end's flags must be readable.")
		assert.Zero(t, flags&unix.O_NONBLOCK, "The owner is another program and expects a blocking descriptor to write into.")

		// Part of the text arrives and the end never does, as from an owner
		// that froze mid-transfer.
		_, err = unix.Write(writeFD, []byte("partial"))
		require.NoError(t, err, "The owner's first bytes must reach the pipe.")
		type result struct {
			text string
			err  error
		}
		done := make(chan result, 1)
		go func() {
			text, err := readSelection(read, 50*time.Millisecond)
			done <- result{text, err}
		}()
		select {
		case got := <-done:
			assert.ErrorIs(t, got.err, os.ErrDeadlineExceeded, "Half a selection is a failed read, not a shorter clipboard.")
		case <-time.After(5 * time.Second):
			require.Fail(t, "The read did not give up on an owner that never finished.")
		}
	})

	t.Run("a read returns what a finished owner wrote", func(t *testing.T) {
		read, writeFD, err := selectionPipe()
		require.NoError(t, err, "The transfer pipe must be created.")
		defer read.Close()
		_, err = unix.Write(writeFD, []byte("copied text"))
		require.NoError(t, err, "The owner's bytes must reach the pipe.")
		require.NoError(t, unix.Close(writeFD), "The owner closes its end when it is done.")

		text, err := readSelection(read, 5*time.Second)
		require.NoError(t, err, "End of file from the owner is the normal end of a transfer.")
		assert.Equal(t, "copied text", text, "The whole selection must come back.")
	})

	t.Run("a paste ends when the paster never reads", func(t *testing.T) {
		var fds [2]int
		require.NoError(t, unix.Pipe2(fds[:], unix.O_CLOEXEC), "The paster's pipe must be created.")
		// Closing the read end frees a writer still blocked when the test ends.
		defer unix.Close(fds[0])

		// One megabyte is past the 64 KiB a Linux pipe buffers, so the write
		// cannot complete while nothing reads.
		done := make(chan struct{})
		go func() {
			sendSelection(fds[1], strings.Repeat("x", 1<<20), 50*time.Millisecond)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.Fail(t, "The paste did not give up on a paster that never read.")
		}
	})
}
