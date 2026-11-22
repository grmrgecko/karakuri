//go:build linux

package wldata

import (
	"encoding/binary"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestReadEventRetriesInterruptedReceive locks that a signal does not end a
// read. Linux never restarts a receive on a socket with SO_RCVTIMEO set after
// a signal handler runs, whatever SA_RESTART says (signal(7)), and every
// connection here sets that timeout. serveSelection reads for as long as the
// selection is owned, so an EINTR reported as an error dropped the clipboard.
func TestReadEventRetriesInterruptedReceive(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	require.NoError(t, err, "the test needs a socket pair to stand in for the compositor")
	defer unix.Close(pair[1])
	c := &conn{fd: pair[0], nextID: 2}
	defer c.close()
	c.setDeadline(time.Now().Add(10 * time.Second))

	type result struct {
		ev  event
		err error
	}
	tid := make(chan int, 1)
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid <- unix.Gettid()
		ev, err := c.readEvent()
		done <- result{ev, err}
	}()

	// Let the reader block in recvmsg, then interrupt its thread with SIGURG,
	// which the Go runtime uses for preemption and handles harmlessly.
	reader := <-tid
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, unix.Tgkill(os.Getpid(), reader, unix.SIGURG), "the reader thread must be signalled")
	time.Sleep(100 * time.Millisecond)

	// A data source's cancelled event: object 5, opcode 1, no arguments.
	msg := make([]byte, 8)
	binary.LittleEndian.PutUint32(msg, 5)
	binary.LittleEndian.PutUint32(msg[4:], 8<<16|sourceEventCancelled)
	_, err = unix.Write(pair[1], msg)
	require.NoError(t, err, "the event must reach the socket")

	select {
	case got := <-done:
		require.NoError(t, got.err, "an interrupted receive must be retried, not reported")
		assert.Equal(t, uint32(5), got.ev.object, "the event after the signal must arrive intact")
		assert.Equal(t, sourceEventCancelled, got.ev.opcode, "the event after the signal must keep its opcode")
	case <-time.After(5 * time.Second):
		require.Fail(t, "readEvent did not return the event sent after the signal")
	}
}
