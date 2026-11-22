package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// countingWire records how often the provider is asked for its catalog, which
// is the observable the cache exists to bound.
type countingWire struct {
	models []string
	err    error
	calls  int
}

func (f *countingWire) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return nil, errors.New("not used")
}

func (f *countingWire) Models(ctx context.Context) ([]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.models...), nil
}

// TestModelsCatalogCache pins the cache contract the editor's autocomplete
// depends on: a fresh catalog is served without a provider call, sorted for a
// stable picker; RefreshModels always pays one; expiry pays one on the next
// read; and a failed refresh reports the error instead of silently serving
// the previous list.
func TestModelsCatalogCache(t *testing.T) {
	wire := &countingWire{models: []string{"llama3.2", "gpt-oss-20b"}}
	d := &Driver{kind: KindAI, wire: wire, log: zap.NewNop()}
	ctx := context.Background()

	first, err := d.Models(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-oss-20b", "llama3.2"}, first, "the catalog is served sorted whatever order the provider lists in")
	assert.Equal(t, 1, wire.calls)

	_, err = d.Models(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, wire.calls, "a fresh catalog answers from cache, not the provider")

	_, err = d.RefreshModels(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, wire.calls, "an explicit refresh always asks the provider")

	d.modelsMu.Lock()
	d.modelsAt = time.Now().Add(-modelsTTL - time.Second)
	d.modelsMu.Unlock()
	_, err = d.Models(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, wire.calls, "an expired catalog is refreshed on the next read")

	wire.err = errors.New("provider down")
	_, err = d.RefreshModels(ctx)
	assert.Error(t, err, "a failed listing reports rather than serving the stale catalog as fresh")
}

// TestModelsReadsEveryPage locks that a model listing follows the provider's
// paging to its end. Gemini answers 50 models a page and Anthropic 20, and
// only the first page was read, so an account with more lost the rest from
// the model picker: on Gemini the newest models, which it lists last.
func TestModelsReadsEveryPage(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wire    func(base string) wire
	}{
		{
			name: "gemini",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1beta/models" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Query().Get("pageToken") == "next" {
					_, _ = w.Write([]byte(`{"models":[{"name":"models/second"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"models":[{"name":"models/first"}],"nextPageToken":"next"}`))
			},
			wire: func(base string) wire { return &geminiWire{base: base, key: "k"} },
		},
		{
			name: "anthropic",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Query().Get("after_id") == "first" {
					_, _ = w.Write([]byte(`{"data":[{"id":"second"}],"has_more":false,"last_id":"second"}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`))
			},
			wire: func(base string) wire { return &anthropicWire{base: base, key: "k"} },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			models, err := tc.wire(srv.URL).Models(context.Background())
			require.NoError(t, err)
			assert.Equal(t, []string{"first", "second"}, models,
				"models past the first page must be listed too")
		})
	}
}
