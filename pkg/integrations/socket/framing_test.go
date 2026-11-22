package socket

import (
	"bufio"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWireFormats pins JSON scalar/array handling, binary ownership and escaped
// delimiter bytes used by TCP devices; malformed encodings must not be coerced.
func TestWireFormats(t *testing.T) {
	for _, tc := range []struct {
		name, format, wire string
		want               any
	}{
		{"text", "text", "device", "device"},
		{"binary", "binary", "\x00\xff", []byte{0, 255}},
		{"base64", "base64", "\x00\xff", base64.StdEncoding.EncodeToString([]byte{0, 255})},
		{"JSON scalar", "json", "true", true},
		{"JSON array", "json", "[1,2]", []any{float64(1), float64(2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.wire)
			v, e := Decode(raw, tc.format)
			require.NoError(t, e)
			assert.Equal(t, tc.want, v)
			encoded, _, e := Encode(v, tc.format)
			require.NoError(t, e)
			assert.Equal(t, raw, encoded)
			raw[0] = 42
			assert.Equal(t, tc.want, v, "received payloads must not share mutable transport buffers")
		})
	}
	for _, tc := range []struct{ format, wire string }{{"json", "{"}, {"base64", "not base64"}} {
		if tc.format == "json" {
			_, e := Decode([]byte(tc.wire), tc.format)
			assert.Error(t, e)
		} else {
			_, _, e := Encode(tc.wire, tc.format)
			assert.Error(t, e)
		}
	}
	delimiter, e := Delimiter(`\r\n`)
	require.NoError(t, e)
	assert.Equal(t, []byte{13, 10}, delimiter)
	_, e = Delimiter(`\x`)
	assert.Error(t, e)
	r := bufio.NewReader(strings.NewReader("ok\r\nnext\r\n"))
	f := Framing{Mode: "delimiter", Delimiter: `\r\n`, MaxBytes: 6}
	b, e := readFrame(r, f)
	require.NoError(t, e)
	assert.Equal(t, "ok", string(b))
	b, e = readFrame(r, f)
	require.NoError(t, e)
	assert.Equal(t, "next", string(b))
	r = bufio.NewReader(strings.NewReader("large\r\n"))
	_, e = readFrame(r, f)
	assert.ErrorIs(t, e, errFrameLimit)
}
