package docs

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScriptsRenders checks the embedded reference survives the round trip to
// HTML with the pieces the editor depends on. The page is rendered at build
// time and served as HTML, so the frontend has no markdown parser to fall back
// on: a goldmark extension that stopped being applied would reach the reader
// as literal pipe characters where a table should be.
func TestScriptsRenders(t *testing.T) {
	doc, ok := Get("scripts")
	require.True(t, ok, "the scripting guide must be in the catalog")

	require.Equal(t, "scripts", doc.ID, "the script editor opens this page by id")
	require.NotEmpty(t, doc.Title, "a guide with no title has nothing to head the panel with")
	require.Greater(t, len(doc.HTML), 1000, "the rendered reference is suspiciously short at %d bytes", len(doc.HTML))

	// The editor links straight to a language section, so the heading anchors
	// are addressed from outside this package.
	for _, anchor := range []string{`id="javascript"`, `id="python"`, `id="bash"`} {
		assert.Containsf(t, doc.HTML, anchor, "the reference must expose %s for the editor to jump to", anchor)
	}
	assert.Contains(t, doc.HTML, "<table>", "the page is largely tables; no table means the GFM extension is not applied")
	assert.Contains(t, doc.HTML, "<code>", "inline code spans must render as code")

	// Highlighting emits classes for the viewer to color, never inline styles:
	// the page follows the app's theme, not chroma's, so a dark-theme reader
	// would otherwise get chroma's light palette burned into the markup.
	assert.Contains(t, doc.HTML, `class="chroma"`, "fenced code must be highlighted")
	assert.NotContains(t, doc.HTML, `<pre tabindex="0" class="chroma" style=`,
		"highlighting must emit classes, not inline styles the theme cannot override")
}

// TestRenderDropsRawHTML holds the renderer to goldmark's safe default. The
// frontend inserts this HTML directly and note text comes from whoever edited
// the flow, so passing markup through would be a stored cross-site scripting
// hole in every guide that renders authored content. goldmark drops raw HTML
// rather than escaping it, leaving a comment where the tag was — the tag's
// text content survives, the tag itself does not.
func TestRenderDropsRawHTML(t *testing.T) {
	out := Render("Hello <script>alert(1)</script>\n")
	assert.NotContains(t, out, "<script>", "raw HTML must not reach the page: %s", out)
	assert.Contains(t, out, "raw HTML omitted", "goldmark marks where it dropped the tag")
}

// TestScriptsMarkdownIsSource checks the markdown accessor returns the embedded
// file rather than the rendering, which is what lets a caller re-render it.
func TestScriptsMarkdownIsSource(t *testing.T) {
	source, ok := Markdown("scripts")
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(source, "# Scripting"),
		"the accessor must return the embedded markdown source, not its HTML")
}

// TestCatalogCoversEveryGuide keeps the catalog and the embedded files in step:
// a guide file without an entry would never reach the help index, and an entry
// without a file would list a guide that answers 404. Each entry's title must
// match the guide's own first heading so the index and the page agree.
func TestCatalogCoversEveryGuide(t *testing.T) {
	entries, err := files.ReadDir(".")
	require.NoError(t, err)
	listed := map[string]bool{}
	for _, g := range Guides() {
		listed[g.ID] = true
		source, ok := Markdown(g.ID)
		require.Truef(t, ok, "catalog entry %q has no %s.md", g.ID, g.ID)
		assert.Truef(t, strings.HasPrefix(source, "# "+g.Title+"\n"),
			"catalog title %q must match the first heading of %s.md", g.Title, g.ID)
		assert.NotEmptyf(t, g.Group, "%s needs a group for the index", g.ID)
		assert.NotEmptyf(t, g.Summary, "%s needs a summary for the index", g.ID)
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".md")
		assert.Truef(t, listed[id], "%s is embedded but missing from the catalog", e.Name())
	}
}

// guideLink matches a markdown link to another guide, with an optional anchor.
var guideLink = regexp.MustCompile(`\]\(([a-z0-9-]+)\.md(?:#([a-z0-9-]+))?\)`)

// TestGuideLinksResolve checks every link between guides names a guide in the
// catalog and, when it has an anchor, a heading that renders with that ID. The
// help pages turn these links into navigation, so a stale one is a dead click.
func TestGuideLinksResolve(t *testing.T) {
	for _, g := range Guides() {
		source, _ := Markdown(g.ID)
		for _, m := range guideLink.FindAllStringSubmatch(source, -1) {
			target, ok := Get(m[1])
			if !assert.Truef(t, ok, "%s links to %s.md, which is not a guide", g.ID, m[1]) {
				continue
			}
			if m[2] != "" {
				assert.Containsf(t, target.HTML, `id="`+m[2]+`"`, "%s links to %s.md#%s, which has no such heading", g.ID, m[1], m[2])
			}
		}
	}
}
