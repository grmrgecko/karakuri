package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestZipHonorsTheCompressionLevel locks that the compression setting reaches
// a zip archive. The archive writer deflates at its own default unless told
// otherwise, so Smallest and Fastest produced byte-identical archives.
func TestZipHonorsTheCompressionLevel(t *testing.T) {
	// Text with long-range repeats, where the effort levels differ measurably.
	var text strings.Builder
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&text, "sensor-%d reported %d at step %d\n", i%37, (i*7919)%1013, i%251)
	}
	files := map[string]any{"readings.log": text.String()}

	size := func(level string) int {
		packed := runNode(t, CompressNode{}, `{"format":"zip","level":"`+level+`"}`, &engine.Msg{Payload: files})
		data, ok := packed["out"][0].Payload.([]byte)
		require.True(t, ok, "a zip archive is bytes")
		return len(data)
	}
	assert.Less(t, size("best"), size("fast"), "Smallest must compress harder than Fastest")
}
