package agentnode

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinesOfNothingEncodeAsAnEmptyArray locks the payload a lines read gives
// for no text at all. The payload crosses to the server as JSON, where a nil
// slice is null: an empty file, or a program that printed nothing, reached the
// flow as null instead of the empty array its port declares, and a script
// counting the lines failed on it. The server's own Read File answers [] here.
func TestLinesOfNothingEncodeAsAnEmptyArray(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"no text", "", `[]`},
		{"only the final newline", "\n", `[]`},
		{"one line", "a\n", `["a"]`},
		{"a blank line between two", "a\r\n\r\nb\r\n", `["a","","b"]`},
	} {
		payload, err := DecodeOutput(FormatLines, []byte(tc.text))
		require.NoErrorf(t, err, "lines decoding must accept %s", tc.name)
		encoded, err := json.Marshal(payload)
		require.NoErrorf(t, err, "the payload for %s must encode", tc.name)
		assert.Equalf(t, tc.want, string(encoded), "wire payload mismatch for %s", tc.name)
	}
}
