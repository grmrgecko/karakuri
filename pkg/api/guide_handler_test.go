package api_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuideGet covers the reference the script editor's View docs button opens.
// The markdown is rendered at build time and served as HTML, so the frontend
// needs no markdown parser and the page always matches the build it came from —
// which means a rendering failure surfaces here or nowhere. An unknown id is a
// 404 rather than an empty page, since a blank panel reads as a broken button.
func TestGuideGet(t *testing.T) {
	srv, token := setupTestServer(t)

	status, body := doJSON(t, srv, token, "GET", "/api/guides/scripts", nil)
	require.Equalf(t, 200, status, "the embedded scripting reference must be served: %v", status)

	data, ok := body["data"].(map[string]any)
	require.Truef(t, ok, "the guide comes back under data: %v", body)
	assert.Equal(t, "scripts", data["id"], "the requested guide is the one served")

	html, _ := data["html"].(string)
	assert.Contains(t, html, `id="javascript"`,
		"the editor links straight to a language section, so the heading anchors must survive the rendering")

	status, _ = doJSON(t, srv, token, "GET", "/api/guides/nonexistent", nil)
	assert.Equal(t, 404, status, "an unknown guide must be a 404: a blank panel reads as a broken button")
}

// TestMarkdownRender covers rendering a note's own text. The source comes from
// whoever edited the flow and the result is inserted into the page directly, so
// raw markup must not pass through — that would be stored cross-site scripting
// in every note on the canvas. The size cap is the other half: notes are stored
// in the graph document and copied into every frozen version, so an unbounded
// source is refused rather than parsed.
func TestMarkdownRender(t *testing.T) {
	srv, token := setupTestServer(t)

	status, body := doJSON(t, srv, token, "POST", "/api/guides/markdown", map[string]any{
		"markdown": "# Title\n\nSome **bold** text and <script>alert(1)</script>\n",
	})
	require.Equalf(t, 200, status, "note text must render: %v", body)

	data, ok := body["data"].(map[string]any)
	require.Truef(t, ok, "the rendering comes back under data: %v", body)

	html, _ := data["html"].(string)
	assert.Contains(t, html, "<strong>bold</strong>", "markdown must actually be rendered, not echoed back")
	assert.NotContains(t, html, "<script>",
		"the result is inserted into the page directly, so raw markup would be stored cross-site scripting: %s", html)

	status, _ = doJSON(t, srv, token, "POST", "/api/guides/markdown", map[string]any{
		"markdown": strings.Repeat("a", 70*1024),
	})
	assert.Equal(t, 400, status,
		"notes are copied into every frozen version, so an oversized source must be refused rather than parsed")
}

// TestGuideRequiresAuth covers the gate on the guides. They need a signed-in
// user but no subsystem permission — gating them on api:flows:read would hide
// the MQTT device reference from a dashboard author who cannot open a flow —
// so the check that matters is that an anonymous request is still refused.
func TestGuideRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	resp, err := srv.Client().Get(srv.URL + "/api/guides/scripts")
	require.NoError(t, err, "the request must reach the server")
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, 401, resp.StatusCode,
		"guides need no subsystem permission, but they still need a signed-in user")
}
