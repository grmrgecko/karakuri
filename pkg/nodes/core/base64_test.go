package core

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// binaryPayload asserts the payload really is bytes and returns them. The
// distinction matters: binary is real bytes in the payload, not base64 text
// that happens to look like it.
func binaryPayload(t *testing.T, msg *engine.Msg) []byte {
	t.Helper()
	raw, ok := msg.Payload.([]byte)
	require.Truef(t, ok, "payload is %T, want real bytes: base64 decodes to binary, not to text", msg.Payload)
	return raw
}

// TestBase64DecodesToBinary covers the decoder's output type. It produces real
// bytes rather than a string, which is what makes the port's declared binary
// type honest and what lets the next node write them to a file unchanged.
func TestBase64DecodesToBinary(t *testing.T) {
	msg := runParser(t, Base64Node{}, `{}`, &engine.Msg{Payload: "AAEC"})
	assert.Equal(t, []byte{0x00, 0x01, 0x02}, binaryPayload(t, msg),
		"the decoded bytes must be the payload, including bytes no string could carry")
}

// TestBase64DecodesWithAndWithoutPadding pins a fact about the outside world:
// encoders disagree about trailing padding, and a value arriving from an MQTT
// device or a JSON API may carry it or not. Both must decode to the same bytes,
// or half the senders on a network fail.
func TestBase64DecodesWithAndWithoutPadding(t *testing.T) {
	padded := runParser(t, Base64Node{}, `{}`, &engine.Msg{Payload: "aGVsbG8="})
	assert.Equal(t, "hello", string(binaryPayload(t, padded)), "a padded value must decode")

	unpadded := runParser(t, Base64Node{}, `{}`, &engine.Msg{Payload: "aGVsbG8"})
	assert.Equal(t, "hello", string(binaryPayload(t, unpadded)),
		"an unpadded value must decode to the same bytes: senders disagree about padding")
}

// TestBase64EncodesBinaryAndText covers what the encoder accepts. Encoding text
// is as valid as encoding bytes, which is why the input port cannot declare
// binary outright — a node that rejected a string would refuse the commonest
// case, encoding a password or a token for a header.
func TestBase64EncodesBinaryAndText(t *testing.T) {
	fromBinary := runParser(t, Base64EncodeNode{}, `{}`, &engine.Msg{Payload: []byte("\x00\x01\x02")})
	assert.Equal(t, "AAEC", stringPayload(t, fromBinary), "real bytes encode to their base64 text")

	fromText := runParser(t, Base64EncodeNode{}, `{}`, &engine.Msg{Payload: "hello"})
	assert.Equal(t, "aGVsbG8=", stringPayload(t, fromText),
		"text encodes as its own bytes, which is why the input port cannot declare binary")
}

// TestBase64EncodeDropsPaddingWhenAsked covers the padding switch, which exists
// because some receivers reject the "=" characters outright.
func TestBase64EncodeDropsPaddingWhenAsked(t *testing.T) {
	msg := runParser(t, Base64EncodeNode{}, `{"padding":false}`, &engine.Msg{Payload: "hello"})
	assert.Equal(t, "aGVsbG8", stringPayload(t, msg),
		"turning padding off must drop the trailing = for receivers that reject it")
}

// TestBase64URLAlphabetRoundTrips pins the URL-safe alphabet, which swaps + and
// / for - and _ so a value can travel in a query string or a path. These bytes
// are chosen because they encode to characters that differ between the two
// alphabets: standard base64 would render them with + and /, so a decoder using
// the wrong table would visibly disagree rather than pass by luck.
func TestBase64URLAlphabetRoundTrips(t *testing.T) {
	raw := []byte{0xfb, 0xff, 0xbe}

	encoded := runParser(t, Base64EncodeNode{}, `{"encoding":"url"}`, &engine.Msg{Payload: raw})
	text := stringPayload(t, encoded)
	require.Equal(t, "-_--", text, "the URL-safe table must be used, not the standard one with + and /")

	decoded := runParser(t, Base64Node{}, `{"encoding":"url"}`, &engine.Msg{Payload: text})
	assert.Equal(t, raw, binaryPayload(t, decoded), "the pair must round trip on the same alphabet")
}

// TestBase64RejectsBadInput covers the inputs the pair refuses, against the
// decodes above that must keep working. A source names its format and bytes
// that do not match it fail rather than arriving as something else, so text
// that is not base64 fails the node onto its error branch instead of yielding
// whatever prefix happened to decode.
func TestBase64RejectsBadInput(t *testing.T) {
	parserError(t, Base64Node{}, `{}`, &engine.Msg{Payload: "not base64!"})
	parserError(t, Base64Node{}, `{"encoding":"sideways"}`, &engine.Msg{Payload: "AAEC"})
	// An object has no byte form a flow author would recognize, so encoding one
	// would mean silently picking an encoding on their behalf.
	parserError(t, Base64EncodeNode{}, `{}`, &engine.Msg{Payload: map[string]any{}})
}

// TestBase64ReadsAndWritesProperties covers reading and writing a path rather
// than the whole payload, which is how one base64 field inside a JSON document
// is decoded without disturbing the rest. The source is left in place: a
// converter reads, it does not consume.
func TestBase64ReadsAndWritesProperties(t *testing.T) {
	msg := runParser(t, Base64Node{}, `{"property":"payload.image","target":"payload.bytes"}`,
		&engine.Msg{Payload: map[string]any{"image": "AAEC"}})

	obj := objectPayload(t, msg)
	assert.Equal(t, "AAEC", obj["image"], "the source property must be left alone: a converter reads rather than consumes")

	raw, ok := obj["bytes"].([]byte)
	require.Truef(t, ok, "the target must hold real bytes, got %T", obj["bytes"])
	assert.Equal(t, []byte{0x00, 0x01, 0x02}, raw, "the decoded bytes must land at the configured target path")
}
