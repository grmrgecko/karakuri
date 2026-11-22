package exec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOutputCaptureKeepsTheCompleteStream covers the disk boundary used for
// child output. A stream larger than the former 256 KiB cap must reach both the
// returned result and an operator-named file byte for byte.
func TestOutputCaptureKeepsTheCompleteStream(t *testing.T) {
	want := bytes.Repeat([]byte("0123456789abcdef"), 32*1024) // 512 KiB.
	destination := filepath.Join(t.TempDir(), "stdout.log")
	capture, err := newOutputCapture(destination, "return")
	require.NoError(t, err, "the output spool and named copy must open")
	t.Cleanup(capture.cleanup)

	written, err := capture.Writer().Write(want)
	require.NoError(t, err, "the complete simulated process write must be accepted")
	assert.Equal(t, len(want), written, "the process must not observe a short write")

	got, err := capture.finish()
	require.NoError(t, err, "the spool must be readable after the process finishes")
	assert.Equal(t, want, got, "returned stdout must not be capped")
	onDisk, err := os.ReadFile(destination)
	require.NoError(t, err, "the configured stdout file must be readable")
	assert.Equal(t, want, onDisk, "the configured file must receive the same complete stream")
}
