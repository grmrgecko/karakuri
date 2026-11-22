package flow

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngURI builds a data URI whose base64 payload decodes to n bytes.
func pngURI(n int) string {
	return "data:image/png;base64," + strings.Repeat("A", (n+2)/3*4)
}

// TestValidateImagesAccepts checks the shapes an attached image is allowed to
// take: the four types every browser renders, named the way a markdown
// reference in a note can name them.
func TestValidateImagesAccepts(t *testing.T) {
	problems := ValidateImages(map[string]any{
		"diagram.png": "data:image/png;base64,iVBORw0KGgo=",
		"photo_1.jpg": "data:image/jpeg;base64,/9j/4AAQSkZJRg==",
		"anim-2.gif":  "data:image/gif;base64,R0lGODlhAQ",
		"shot.webp":   "data:image/webp;base64,UklGRhIA",
	})
	assert.Empty(t, problems, "the four renderable types under ordinary names must all be accepted")
}

// TestValidateImagesRejects holds the boundary that matters: the editor puts
// these values in an img src, so anything that is not plainly an image data
// URI has to be refused at the save rather than at the render. Every row here
// is a way to reach script execution or a remote fetch from a note.
func TestValidateImagesRejects(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
		why   string
	}{
		{
			name: "not a map", value: "data:image/png;base64,AA==", want: "set of named images",
			why: "a bare string has no name to reference it by",
		},
		{
			name: "script URL", value: map[string]any{"x.png": "javascript:alert(1)"}, want: "not a PNG",
			why: "an img src that runs script is the whole reason this check exists",
		},
		{
			name: "foreign data URI", value: map[string]any{"x.svg": "data:image/svg+xml;base64,AAA="}, want: "not a PNG",
			why: "SVG carries script, so it is not one of the renderable four",
		},
		{
			name: "remote URL", value: map[string]any{"x.png": "https://example.com/x.png"}, want: "not a PNG",
			why: "an attached image is embedded, never fetched from someone else's host",
		},
		{
			name:  "quote in payload",
			value: map[string]any{"x.png": `data:image/png;base64,AA"onerror=alert(1)`},
			want:  "not a PNG",
			why:   "a quote closes the src attribute and opens an event handler",
		},
		{
			name: "unusable name", value: map[string]any{"my image.png": "data:image/png;base64,AA=="}, want: "image name",
			why: "a space breaks the markdown reference that names it",
		},
		{
			name: "non-string value", value: map[string]any{"x.png": 12}, want: "not a string",
		},
	}
	for _, c := range cases {
		problems := ValidateImages(c.value)
		require.NotEmptyf(t, problems, "%s must be rejected at the save: %s", c.name, c.why)
		assert.Containsf(t, problems[0], c.want, "the problem reported for %s must name what is wrong", c.name)
	}
}

// TestValidateImagesLimits checks the size and count caps, which are what keep
// a note from growing every frozen version of a flow without bound: images are
// stored in the graph document, so each one is copied into every deploy.
func TestValidateImagesLimits(t *testing.T) {
	assert.Empty(t, ValidateImages(map[string]any{"ok.png": pngURI(MaxImageBytes - 1024)}),
		"an image just under the cap must be accepted")
	assert.NotEmpty(t, ValidateImages(map[string]any{"big.png": pngURI(MaxImageBytes + 1024)}),
		"an image just over the cap must be refused, or one note bloats every version")

	// One more than the cap allows, named so the keys stay distinct.
	many := map[string]any{}
	for i := 0; i <= MaxImagesPerNode; i++ {
		many[string(rune('a'+i%26))+string(rune('a'+i/26))+".png"] = "data:image/png;base64,AA=="
	}
	assert.Len(t, many, MaxImagesPerNode+1, "the fixture must hold exactly one image over the cap")
	assert.NotEmpty(t, ValidateImages(many), "a node one image over the count cap must be refused")
}
