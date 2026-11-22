package core

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runNode executes a node with one message on "in" and returns the emissions.
func runNode(t *testing.T, node engine.NodeType, cfg string, msg *engine.Msg) map[string][]*engine.Msg {
	t.Helper()
	out, err := node.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "the node is configured for the message it was given")
	return out
}

// TestArchiveRoundTrip locks the pack and unpack pair across every format,
// because an archive is the one thing here whose output is read by other
// software: a zip this writes has to open in a file manager, and a tar.gz it
// reads is usually one something else wrote. The pair is checked in one test
// because a bug that corrupts both directions the same way is invisible to
// either half alone, so the file listing is asserted field by field.
func TestArchiveRoundTrip(t *testing.T) {
	files := []any{
		map[string]any{"name": "readings/today.csv", "data": []byte("time,c\n09:00,19.5\n")},
		map[string]any{"name": "notes.txt", "data": "the boiler ran twice"},
	}

	for _, format := range []string{formatZip, formatTar, formatTarGz} {
		t.Run(format, func(t *testing.T) {
			packed := runNode(t, CompressNode{},
				`{"format":"`+format+`","property":"payload","target":"payload"}`,
				&engine.Msg{Payload: files})
			require.Len(t, packed["out"], 1, "packing emits one message holding the archive")
			archive, ok := packed["out"][0].Payload.([]byte)
			require.True(t, ok, "an archive is bytes, not %T", packed["out"][0].Payload)

			out := runNode(t, DecompressNode{},
				`{"format":"`+format+`","property":"payload","target":"payload"}`,
				&engine.Msg{Payload: archive})
			require.Len(t, out["out"], 1, "unpacking into an array emits one message")
			entries, ok := out["out"][0].Payload.([]any)
			require.True(t, ok, "the unpacked payload is a list of files, not %T", out["out"][0].Payload)
			require.Len(t, entries, 2, "both files survive the round trip")

			// Sorted by name, so notes.txt comes before readings/today.csv.
			first := entries[0].(map[string]any)
			assert.Equal(t, "notes.txt", first["name"], "entries come back in name order, not archive order")
			assert.Equal(t, []byte("the boiler ran twice"), first["data"], "text packs as its bytes and comes back as bytes")
			second := entries[1].(map[string]any)
			assert.Equal(t, "readings/today.csv", second["name"], "a path inside the archive is preserved, not flattened")
			assert.Equal(t, float64(18), second["size"], "size is the byte count of the unpacked file")
		})
	}
}

// TestArchiveGlobMatchesTheBaseName pins the filter rule that is easy to get
// wrong: "*.csv" has no slash in it, so it is matched against the file's own
// name rather than its path, which is what someone unpacking an export expects
// when the files turn out to be in a folder.
func TestArchiveGlobMatchesTheBaseName(t *testing.T) {
	files := []any{
		map[string]any{"name": "export/a.csv", "data": "1"},
		map[string]any{"name": "export/b.txt", "data": "2"},
	}
	packed := runNode(t, CompressNode{}, `{"format":"zip"}`, &engine.Msg{Payload: files})
	archive := packed["out"][0].Payload

	out := runNode(t, DecompressNode{}, `{"format":"zip","match":"*.csv"}`, &engine.Msg{Payload: archive})
	entries := out["out"][0].Payload.([]any)
	require.Len(t, entries, 1, "only the csv matches, though it sits in a folder")
	assert.Equal(t, "export/a.csv", entries[0].(map[string]any)["name"],
		"the entry keeps its full path even though the pattern matched the base name")

	out = runNode(t, DecompressNode{}, `{"format":"zip","match":"export/*"}`, &engine.Msg{Payload: archive})
	assert.Len(t, out["out"][0].Payload.([]any), 2,
		"a pattern containing a slash is matched against the whole path instead")
}

// TestGzipRoundTrip covers the one-value format, including that text goes in
// without a base64 step: a flow gzipping a log or a JSON document should not
// have to encode it first.
func TestGzipRoundTrip(t *testing.T) {
	const text = "2026-08-31 boiler on\n2026-08-31 boiler off\n"
	packed := runNode(t, CompressNode{}, `{"format":"gzip"}`, &engine.Msg{Payload: text})
	compressed, ok := packed["out"][0].Payload.([]byte)
	require.True(t, ok, "gzip output is bytes")

	out := runNode(t, DecompressNode{}, `{"format":"gzip"}`, &engine.Msg{Payload: compressed})
	assert.Equal(t, []byte(text), out["out"][0].Payload, "the bytes come back exactly as they went in")
}

// TestSpreadsheetRoundTrip locks the xlsx pair. The point of the node is that
// something else wrote the file, so the read side has to survive a real
// workbook; writing one here and reading it back is the closest a unit test
// gets, and it pins the column ordering rule, which is the part that would
// otherwise vary run to run because Go map iteration does.
func TestSpreadsheetRoundTrip(t *testing.T) {
	rows := []any{
		map[string]any{"date": "2026-08-30", "kwh": 12.5, "note": "warm"},
		map[string]any{"date": "2026-08-31", "kwh": 9.0, "note": "cool"},
	}
	written := runNode(t, SpreadsheetEncodeNode{},
		`{"sheet":"Readings","columns":"date,kwh,note"}`, &engine.Msg{Payload: rows})
	book, ok := written["out"][0].Payload.([]byte)
	require.True(t, ok, "a workbook is bytes, not %T", written["out"][0].Payload)

	out := runNode(t, SpreadsheetNode{}, `{"sheet":"Readings"}`, &engine.Msg{Payload: book})
	read, ok := out["out"][0].Payload.([]any)
	require.True(t, ok, "the sheet reads back as a list of rows")
	require.Len(t, read, 2, "the header row names the columns rather than becoming a row")

	first := read[0].(map[string]any)
	assert.Equal(t, "2026-08-30", first["date"], "a date column comes back as the text the sheet shows")
	assert.Equal(t, 12.5, first["kwh"], "a numeric cell comes back as a number, so a filter can compare it")
	assert.Equal(t, "warm", first["note"])

	// Reading the same workbook with numbers off is what preserves a leading
	// zero, which is the case that makes the switch worth having.
	out = runNode(t, SpreadsheetNode{}, `{"sheet":"Readings","parse_numbers":false}`, &engine.Msg{Payload: book})
	assert.Equal(t, "12.5", out["out"][0].Payload.([]any)[0].(map[string]any)["kwh"],
		"with number parsing off every cell stays the text the sheet displays")
}

// TestFeedNormalizesTheThreeFormats is the reason the rss node exists as one
// node rather than three: RSS 2.0, Atom, and JSON Feed name the same three
// ideas differently, and a flow should not care which one a site publishes.
// The documents here are minimal but real, each in its own format's shape.
//
// The id fallback is the row that matters most, because dedupe downstream keys
// on it: an Atom entry has an explicit id, an RSS item without a guid falls back
// to its link, and an item with neither would otherwise look new on every poll.
func TestFeedNormalizesTheThreeFormats(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantID   string
		why      string
	}{
		{
			name: "RSS 2.0 with a guid",
			document: `<?xml version="1.0"?><rss version="2.0"><channel>
				<title>House</title><link>https://example.invalid/</link>
				<item><title>Boiler serviced</title><link>https://example.invalid/1</link>
				<guid>tag:example,2026:1</guid><pubDate>Mon, 31 Aug 2026 09:00:00 GMT</pubDate></item>
				</channel></rss>`,
			wantID: "tag:example,2026:1",
			why:    "an explicit guid is the item's identity",
		},
		{
			name: "RSS 2.0 with no guid",
			document: `<?xml version="1.0"?><rss version="2.0"><channel>
				<title>House</title><link>https://example.invalid/</link>
				<item><title>Boiler serviced</title><link>https://example.invalid/1</link></item>
				</channel></rss>`,
			wantID: "https://example.invalid/1",
			why:    "with no guid the link is the only stable handle the item has",
		},
		{
			name: "Atom",
			document: `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
				<title>House</title><link href="https://example.invalid/"/><id>urn:feed</id>
				<entry><title>Boiler serviced</title><link href="https://example.invalid/1"/>
				<id>tag:example,2026:1</id><updated>2026-08-31T09:00:00Z</updated></entry>
				</feed>`,
			wantID: "tag:example,2026:1",
			why:    "Atom calls it id, and it lands in the same field as an RSS guid",
		},
		{
			name: "JSON Feed",
			document: `{"version":"https://jsonfeed.org/version/1.1","title":"House",
				"home_page_url":"https://example.invalid/",
				"items":[{"id":"tag:example,2026:1","title":"Boiler serviced",
				"url":"https://example.invalid/1","date_published":"2026-08-31T09:00:00Z"}]}`,
			wantID: "tag:example,2026:1",
			why:    "JSON Feed is a third syntax for the same three ideas",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := runNode(t, RSSNode{}, `{}`, &engine.Msg{Payload: tc.document})
			feed, ok := out["out"][0].Payload.(map[string]any)
			require.True(t, ok, "the default output is the feed and its items")
			assert.Equal(t, "House", feed["title"], "the feed's own title is kept, not just the items")

			items := feed["items"].([]any)
			require.Len(t, items, 1, "%s holds one item", tc.name)
			item := items[0].(map[string]any)
			assert.Equal(t, "Boiler serviced", item["title"], "%s: titles normalize", tc.name)
			assert.Equal(t, "https://example.invalid/1", item["link"], "%s: links normalize", tc.name)
			assert.Equal(t, tc.wantID, item["id"], "%s: %s", tc.name, tc.why)
		})
	}
}

// TestImageEditFitInsideReachesTheBox covers fitting inside a box whose
// limiting side does not divide evenly. A 704x480 camera substream inside
// 480x480 is 480 wide and 480*480/704 = 327 high, but the float scale 480/704
// multiplied back by 704 lands a hair under 480 and came out 479.
func TestImageEditFitInsideReachesTheBox(t *testing.T) {
	tests := []struct {
		name          string
		srcW, srcH    int
		box           string
		width, height float64
	}{
		{"landscape pins the width", 704, 480, "480", 480, 327},
		{"portrait pins the height", 480, 704, "480", 327, 480},
		{"square pins both sides", 49, 49, "32", 32, 32},
	}
	for _, tc := range tests {
		src := image.NewRGBA(image.Rect(0, 0, tc.srcW, tc.srcH))
		encoded, _, err := encodeImage(src, imageEditConfig{Format: "png"})
		require.NoErrorf(t, err, "%s: the fixture encodes", tc.name)

		out := runNode(t, ImageEditNode{}, `{"fit":"inside","width":"`+tc.box+`","height":"`+tc.box+`","format":"png"}`,
			&engine.Msg{Payload: encoded})
		msg := only(out, "out")
		require.NotNilf(t, msg, "%s: the edit emits the resized image", tc.name)
		assert.Equalf(t, tc.width, msg.Meta["width"], "%s: the limiting side must reach the box, not stop a pixel short", tc.name)
		assert.Equalf(t, tc.height, msg.Meta["height"], "%s: the limiting side must reach the box, not stop a pixel short", tc.name)
	}
}

// TestImageEditFitsInsideWithoutEnlarging pins the two rules that make the
// resize modes different from each other, and the metadata that makes the
// result usable by the AI agent. Fitting inside is a ceiling: a 40x20 source
// asked to fit a 1000 box stays 40x20, because upscaling to fill it would
// invent detail a model would then read as real.
func TestImageEditFitsInsideWithoutEnlarging(t *testing.T) {
	// A 40x20 source, so the aspect ratio is 2:1 and the arithmetic below is
	// visible: fitting inside 20x20 scales by min(0.5, 1.0) to 20x10.
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			src.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 12), B: 128, A: 255})
		}
	}
	encoded, _, err := encodeImage(src, imageEditConfig{Format: "png"})
	require.NoError(t, err, "the fixture encodes")

	tests := []struct {
		name          string
		cfg           string
		width, height float64
		why           string
	}{
		{"fit inside shrinks by the tighter side", `{"fit":"inside","width":"20","height":"20","format":"png"}`, 20, 10,
			"min(20/40, 20/20) is 0.5, so 40x20 becomes 20x10 and the shape is kept"},
		{"fit inside never enlarges", `{"fit":"inside","width":"1000","height":"1000","format":"png"}`, 40, 20,
			"the box is a ceiling; scaling up would invent pixels"},
		{"cover fills the box and crops", `{"fit":"cover","width":"20","height":"20","format":"png"}`, 20, 20,
			"max(0.5, 1.0) is 1.0, so the width overflows and the middle is taken"},
		{"exact stretches", `{"fit":"exact","width":"15","height":"30","format":"png"}`, 15, 30,
			"the requested size wins over the shape"},
		{"one side derives the other", `{"fit":"exact","width":"20","format":"png"}`, 20, 10,
			"a missing height comes from the width and the 2:1 shape"},
		{"rotation swaps the sides", `{"fit":"none","rotate":"90","format":"png"}`, 20, 40,
			"a quarter turn makes the width the height"},
		{"crop takes the named rectangle", `{"fit":"none","crop":"0,0,10,10","format":"png"}`, 10, 10,
			"cropping happens before any resize"},
		{"a crop past the edge is clipped", `{"fit":"none","crop":"30,0,999,999","format":"png"}`, 10, 20,
			"the rectangle is intersected with the image rather than failing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := runNode(t, ImageEditNode{}, tc.cfg, &engine.Msg{Payload: encoded})
			msg := out["out"][0]
			assert.Equal(t, tc.width, msg.Meta["width"], "%s: %s", tc.name, tc.why)
			assert.Equal(t, tc.height, msg.Meta["height"], "%s: %s", tc.name, tc.why)
			assert.Equal(t, "image/png", msg.Meta["mime"],
				"the media type is declared, which is what lets the AI agent attach the result instead of refusing undeclared bytes")
		})
	}
}

// TestTOTPMatchesRFC6238 checks the code generator against the published test
// vectors in RFC 6238 appendix B. The shared secret there is the ASCII string
// "12345678901234567890", which base32 encodes to GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ,
// and the vectors are 8-digit codes on a 30 second step.
//
// This is the one operation whose correctness cannot be checked by round trip:
// a wrong implementation agrees with itself perfectly and is rejected by every
// site that asks for a code.
func TestTOTPMatchesRFC6238(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	tests := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}

	for _, tc := range tests {
		got, err := cryptoDoTOTP(cryptoConfig{
			Secret: secret, Digits: "8", Period: "30s",
		}, time.Unix(tc.unix, 0).UTC())
		require.NoError(t, err, "the RFC's own secret is valid base32")
		assert.Equal(t, tc.want, got, "RFC 6238 appendix B, T=%d", tc.unix)
	}

	// A secret copied out of an authenticator app comes in lowercase, in groups
	// of four, and usually without padding. Rejecting that would make the field
	// unusable for the only source anyone has for it.
	spaced, err := cryptoDoTOTP(cryptoConfig{
		Secret: "gezd gnbv gy3t qojq gezd gnbv gy3t qojq", Digits: "8", Period: "30s",
	}, time.Unix(59, 0).UTC())
	require.NoError(t, err, "a secret as an app displays it must be accepted")
	assert.Equal(t, "94287082", spaced, "spacing and case are presentation, not part of the secret")

	// Six digits is what almost every site uses, and it is the low six of the
	// same truncated number rather than a different computation.
	six, err := cryptoDoTOTP(cryptoConfig{Secret: secret, Period: "30s"}, time.Unix(59, 0).UTC())
	require.NoError(t, err, "digits defaults to 6")
	assert.Equal(t, "287082", six, "the default 6 digits is the tail of the 8-digit vector")
}
