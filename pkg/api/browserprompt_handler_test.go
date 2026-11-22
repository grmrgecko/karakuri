package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grmrgecko/karakuri/pkg/api"
	"github.com/grmrgecko/karakuri/pkg/browserprompt"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrowserPromptHTTP isolates capabilities, consumes one answer and preserves
// attachment bytes across the real HTTP boundary without an application login.
func TestBrowserPromptHTTP(t *testing.T) {
	manager := browserprompt.NewManager()
	router := chi.NewRouter()
	api.RegisterBrowserPromptRoutes(router, manager)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	fileData := []byte("<script>document.body.remove()</script>")
	cfg := flow.BrowserPromptConfig{Title: "Repair pump", Markdown: "# Instructions\n\n<script>alert(1)</script>\n\n![Location](attachment:photo.png)\n\n[Guide](attachment:guide.html)", Buttons: flow.DefaultPromptButtons(), Feedback: true, RequireFeedback: true,
		Attachments: map[string]flow.Attachment{
			"photo.png":  {Filename: "photo.png", Type: "image/png", Data: base64.StdEncoding.EncodeToString(imageData.Bytes())},
			"guide.html": {Filename: "guide.html", Type: "text/html", Data: base64.StdEncoding.EncodeToString(fileData)},
		}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	token, pending, err := manager.Create(ctx, cfg, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { manager.Release(token) })
	other, otherPending, err := manager.Create(ctx, cfg, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { manager.Release(other) })
	call := func(method, path, capability, body string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		if capability != "" {
			req.Header.Set("Authorization", "Bearer "+capability)
		}
		res, err := server.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res.StatusCode, res.Header, data
	}
	status, _, _ := call("GET", "/api/browser_prompts/current", "", "")
	assert.Equal(t, http.StatusGone, status, "an anonymous request without its capability cannot inspect instructions")
	status, headers, body := call("GET", "/api/browser_prompts/current", token, "")
	require.Equal(t, http.StatusOK, status)
	var view browserprompt.View
	require.NoError(t, json.Unmarshal(body, &view))
	assert.NotContains(t, view.HTML, "<script>", "raw author HTML must not execute in the prompt")
	assert.Contains(t, view.HTML, "attachment:photo.png", "image references survive safe Markdown rendering")
	assert.Equal(t, "no-store", headers.Get("Cache-Control"), "private questions must not be cached")
	for _, tc := range []struct {
		name, kind, disposition string
		want                    []byte
	}{
		{"photo.png", "image/png", "inline", imageData.Bytes()},
		{"guide.html", "application/octet-stream", "attachment", fileData},
	} {
		status, headers, body = call("GET", "/api/browser_prompts/assets/"+tc.name, token, "")
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, tc.want, body, "attachments must preserve uploaded bytes")
		assert.Equal(t, tc.kind, headers.Get("Content-Type"), "only verified raster images render inline")
		assert.Contains(t, headers.Get("Content-Disposition"), tc.disposition, "HTML documents must download instead of executing")
		assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
	}
	for _, invalid := range []string{`{"button":"retry","text":" "}`, `{"button":"unknown","text":"fixed"}`, `{"button":"retry","text":"` + strings.Repeat("x", 4097) + `"}`} {
		status, _, _ = call("POST", "/api/browser_prompts/response", token, invalid)
		assert.Equal(t, http.StatusBadRequest, status, "invalid feedback must leave the question answerable")
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, _ := call("POST", "/api/browser_prompts/response", token, `{"button":"retry","text":"pump reconnected"}`)
			statuses <- status
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	assert.Equal(t, map[int]int{http.StatusNoContent: 1, http.StatusGone: 1}, counts, "two simultaneous clicks must produce one answer")
	answer, err := pending.Result()
	require.NoError(t, err)
	assert.Equal(t, "retry", answer.ID)
	assert.Equal(t, "pump reconnected", answer.Text)
	assert.NotEmpty(t, answer.RequestID)
	status, _, _ = call("GET", "/api/browser_prompts/assets/photo.png", token, "")
	assert.Equal(t, http.StatusGone, status, "an answered request no longer authorizes its files")
	status, _, _ = call("GET", "/api/browser_prompts/current", other, "")
	assert.Equal(t, http.StatusOK, status, "answering one prompt must leave other prompts pending")
	cancel()
	status, _, _ = call("POST", "/api/browser_prompts/response", other, `{"button":"retry","text":"fixed"}`)
	assert.Equal(t, http.StatusGone, status, "canceling a run must revoke its response capability")
	_, err = otherPending.Result()
	assert.ErrorIs(t, err, context.Canceled)
}
