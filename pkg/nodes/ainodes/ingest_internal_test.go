package ainodes

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChunkText locks the chunker's invariants: size is a ceiling, paragraphs
// pack rather than fragment, an overlap repeats boundary text into the next
// chunk, splits never land mid-rune, and every paragraph survives into some
// chunk. Recall quality rests on these — a fact cut in half at a boundary
// with no overlap is a fact no query finds.
func TestChunkText(t *testing.T) {
	pA := "alpha bravo charlie delta echo foxtrot golf"
	pB := "hotel india juliett kilo lima mike november"
	pC := "oscar papa quebec romeo sierra tango uniform"

	cases := []struct {
		name          string
		text          string
		size, overlap int
		minChunks     int
		wantOverlap   bool
	}{
		{name: "short text is one chunk", text: pA, size: 200, overlap: 20, minChunks: 1},
		{name: "paragraphs pack and overlap carries across the boundary",
			text: pA + "\n\n" + pB + "\n\n" + pC, size: 95, overlap: 20, minChunks: 2, wantOverlap: true},
		{name: "an unbroken run splits at the size", text: strings.Repeat("x", 300), size: 100, overlap: 10, minChunks: 3},
		{name: "splits stay off rune boundaries", text: strings.Repeat("é", 100), size: 51, overlap: 0, minChunks: 3},
		// The split here lands after "éclair " with the space included; the
		// overlap is measured on the trimmed text, so counting it back from
		// the untrimmed cut used to start the next piece inside the é.
		{name: "overlap after a cut that ends in whitespace stays on a rune boundary",
			text: "aaaa bbbb éclair  dddd eeee ffff gggg", size: 20, overlap: 10, minChunks: 2},
	}
	for _, tc := range cases {
		chunks, err := chunkText(context.Background(), tc.text, tc.size, tc.overlap)
		require.NoErrorf(t, err, "chunking must not fail for %s", tc.name)
		require.GreaterOrEqualf(t, len(chunks), tc.minChunks, "chunk count for %s", tc.name)
		joined := strings.Join(chunks, "\n")
		for i, chunk := range chunks {
			assert.LessOrEqualf(t, len(chunk), tc.size, "chunk %d exceeds the size ceiling for %s", i, tc.name)
			assert.Truef(t, utf8.ValidString(chunk), "chunk %d is cut mid-rune for %s", i, tc.name)
			assert.NotEmptyf(t, strings.TrimSpace(chunk), "chunk %d is blank for %s", i, tc.name)
		}
		for _, para := range strings.Split(tc.text, "\n\n") {
			// Every word that fits a chunk survives into some chunk whole; a
			// word longer than the size is exactly what hard splits carve up.
			for _, word := range strings.Fields(para)[:min(3, len(strings.Fields(para)))] {
				if len(word) > tc.size {
					continue
				}
				assert.Containsf(t, joined, word, "word %q lost by chunking in %s", word, tc.name)
			}
		}
		if tc.wantOverlap {
			last := chunks[len(chunks)-1]
			assert.Truef(t, strings.Contains(last, pC) && !strings.HasPrefix(last, pC),
				"the final chunk must open with overlap from the previous one in %s", tc.name)
		}
	}

	blank, err := chunkText(context.Background(), "  \n\n  ", 100, 10)
	require.NoError(t, err, "blank text must chunk")
	assert.Nil(t, blank, "blank text ingests as no chunks, which is how a source is forgotten")
}

// TestChunkTextCarriesSplitOverlapOnce locks that a paragraph split across
// chunks repeats its boundary text once. The splitter already opens each
// continuation with the previous piece's tail, and the packer used to prepend
// that tail again whenever the last piece was short enough to leave room, so
// the final chunk read "w032 w033 w034 w035" twice. The words are unique, so
// any word counted twice in a chunk is repeated text. 46 five-byte words at
// size 100 and overlap 20 split into three pieces, the last one short.
func TestChunkTextCarriesSplitOverlapOnce(t *testing.T) {
	var words []string
	for i := 0; i < 46; i++ {
		words = append(words, fmt.Sprintf("w%03d", i))
	}

	chunks, err := chunkText(context.Background(), strings.Join(words, " "), 100, 20)
	require.NoError(t, err, "chunking must not fail")
	require.Len(t, chunks, 3, "the paragraph must split into three chunks")
	for i, chunk := range chunks {
		seen := map[string]bool{}
		for _, w := range strings.Fields(chunk) {
			assert.Falsef(t, seen[w], "chunk %d repeats %q: %q", i, w, chunk)
			seen[w] = true
		}
	}
	assert.Truef(t, strings.HasPrefix(chunks[2], "w032 w033 w034 w035 w036"),
		"the final chunk must still open with the overlap from the one before it: %q", chunks[2])
}

// TestChunkTextSplitAddsTextPastOverlap locks that every piece of a split
// paragraph holds text the one before it lacks. A continuation opens with the
// previous piece's tail, and the only break inside its budget can be the one
// that ends that tail: prose followed by a token longer than a chunk, such as
// a data URI or a minified line. Cutting there stored the tail as a chunk of
// its own, a memory that repeats its neighbor and takes a recall slot.
// Twenty five-byte words are 99 bytes, so the first cut lands on the space at
// byte 99 and the 30-byte overlap carries "w014" through "w019", 29 bytes.
func TestChunkTextSplitAddsTextPastOverlap(t *testing.T) {
	var words []string
	for i := 0; i < 20; i++ {
		words = append(words, fmt.Sprintf("w%03d", i))
	}
	var token strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&token, "%04d", i)
	}

	chunks, err := chunkText(context.Background(), strings.Join(words, " ")+" "+token.String(), 100, 30)
	require.NoError(t, err, "chunking must not fail")
	require.GreaterOrEqual(t, len(chunks), 3, "the token alone spans more than one chunk")
	for i := 1; i < len(chunks); i++ {
		assert.NotContainsf(t, chunks[i-1], chunks[i], "chunk %d only repeats the end of chunk %d", i, i-1)
		assert.LessOrEqualf(t, len(chunks[i]), 100, "chunk %d exceeds the size ceiling", i)
	}
	assert.Truef(t, strings.HasPrefix(chunks[1], "w014 w015 w016 w017 w018 w019 0000"),
		"the second chunk must carry the overlap into the token: %q", chunks[1])

	// A cut made at a run of two spaces leaves one space between the carried
	// "vdz" and the next word. The break after that space lies past the carried
	// bytes but adds no text, and cutting there stored "vdz" as its own piece.
	text := "zy4w8ilyu9i5upi250sqyqdrm vdz  236ai28bu2k3ymfaiefz3onpvo"
	chunks, err = chunkText(context.Background(), text, 24, 57)
	require.NoError(t, err, "chunking must not fail")
	for i := 1; i < len(chunks); i++ {
		assert.NotContainsf(t, chunks[i-1], chunks[i], "chunk %d only repeats the end of chunk %d", i, i-1)
		assert.LessOrEqualf(t, len(chunks[i]), 24, "chunk %d exceeds the size ceiling", i)
	}
	for _, chunk := range chunks {
		assert.NotContainsf(t, chunk, "vdz\n\nvdz", "a chunk repeats the carried tail as its own piece: %q", chunk)
	}
	assert.Contains(t, strings.Join(chunks, ""), "onpvo", "the end of the text must survive chunking")
}

// TestChunkTextAlwaysAdvances pins the hang the AI guide recorded: a budget
// smaller than the first character, two bytes for é, made the split point zero
// and the loop never advanced. The character is now kept whole as its own
// chunk, and a cancelled run stops instead of finishing the document.
func TestChunkTextAlwaysAdvances(t *testing.T) {
	chunks, err := chunkText(context.Background(), "éé", 1, 0)
	require.NoError(t, err, "a budget under one character must still finish")
	assert.Equal(t, []string{"é", "é"}, chunks, "each character wider than the budget becomes its own chunk")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = chunkText(ctx, strings.Repeat("x", 1000), 10, 0)
	assert.ErrorIs(t, err, context.Canceled, "a cancelled run must stop chunking")
}

// TestTruncateKeepsWholeRunes locks that a capped string ends on a character
// boundary. The cut was made at a byte count, so a guardrail match or a tool
// result holding multi-byte text ended in half a character, which run history
// and the model then received as a replacement character.
func TestTruncateKeepsWholeRunes(t *testing.T) {
	tests := []struct {
		name string
		text string
		max  int
		want string
	}{
		{name: "text under the cap is untouched", text: "héllo", max: 10, want: "héllo"},
		{name: "a cut on a boundary keeps every byte", text: "héllo", max: 3, want: "hé…"},
		{name: "a cut inside a two-byte character drops it", text: "héllo", max: 2, want: "h…"},
		{name: "a cut inside a three-byte character drops it", text: "日本語", max: 5, want: "日…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.text, tt.max)
			assert.True(t, utf8.ValidString(got), "The capped text must be valid UTF-8.")
			assert.Equal(t, tt.want, got, "The cut must fall back to the last whole character within the cap.")
		})
	}
}
