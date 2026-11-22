package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestFrontendHandlerServesTheBundle locks the HTTP contract of the embedded
// web interface, laid out as Vite writes it: index.html at the root and
// content-hashed files under assets/. Client routes get index.html, a hashed
// asset is cached for good, and a hashed asset that is not in this build is a
// 404, since a stale tab asking for an old chunk would otherwise get HTML back
// and fail with a MIME error. Embedded files have no modification time, so the
// ETag is what turns a reload into a 304.
func TestFrontendHandlerServesTheBundle(t *testing.T) {
	bundle := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>app</title>")},
		"favicon.svg":          {Data: []byte("<svg/>")},
		"assets/index-Ab12.js": {Data: []byte("console.log(1)")},
		".gitkeep":             {Data: nil},
	}
	h := frontendHandler(zap.NewNop(), bundle)

	cases := []struct {
		path, wantBody, wantCache string
		wantStatus                int
	}{
		{"/", "<!doctype html><title>app</title>", "no-cache", 200},
		{"/flows/42/edit", "<!doctype html><title>app</title>", "no-cache", 200},
		{"/favicon.svg", "<svg/>", "no-cache", 200},
		{"/assets/index-Ab12.js", "console.log(1)", "public, max-age=31536000, immutable", 200},
		{"/assets/index-Old9.js", "404 page not found\n", "", 404},
		{"/.gitkeep", "<!doctype html><title>app</title>", "no-cache", 200},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, tc.path, nil))
		assert.Equal(t, tc.wantStatus, rec.Code, "status for %s", tc.path)
		assert.Equal(t, tc.wantBody, rec.Body.String(), "body for %s", tc.path)
		assert.Equal(t, tc.wantCache, rec.Header().Get("Cache-Control"), "Cache-Control for %s", tc.path)
	}

	t.Run("a matching ETag answers 304", func(t *testing.T) {
		first := httptest.NewRecorder()
		h.ServeHTTP(first, httptest.NewRequest(nethttp.MethodGet, "/", nil))
		etag := first.Header().Get("ETag")
		require.NotEmpty(t, etag, "index.html must carry an ETag to revalidate against")

		req := httptest.NewRequest(nethttp.MethodGet, "/dashboards", nil)
		req.Header.Set("If-None-Match", etag)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, nethttp.StatusNotModified, rec.Code, "a client route serves index.html, so its ETag revalidates")
	})

	t.Run("a binary built without the frontend says how to build it", func(t *testing.T) {
		rec := httptest.NewRecorder()
		frontendHandler(zap.NewNop(), fstest.MapFS{".gitkeep": {}}).ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/", nil))
		assert.Equal(t, nethttp.StatusServiceUnavailable, rec.Code, "an empty bundle must not serve a blank page")
		assert.Contains(t, rec.Body.String(), "make", "the message names the build command")
	})
}
