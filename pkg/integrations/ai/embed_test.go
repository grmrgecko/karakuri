package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenAIWireEmbed covers the shared /embeddings endpoint, including a
// response whose vectors arrive out of input order — the index field is
// authoritative when the server sends one.
func TestOpenAIWireEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "embed-model", body.Model)
		require.Len(t, body.Input, 2)
		w.Write([]byte(`{"data": [
			{"index": 1, "embedding": [0.5, 0.5]},
			{"index": 0, "embedding": [1, 0]}
		]}`))
	}))
	defer srv.Close()

	w := &openaiWire{base: srv.URL + "/v1"}
	vecs, err := w.Embed(context.Background(), "embed-model", []string{"first", "second"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	assert.Equal(t, []float32{1, 0}, vecs[0], "the index field decides placement, not arrival order")
	assert.Equal(t, []float32{0.5, 0.5}, vecs[1])
}

// TestGeminiWireEmbed covers the batchEmbedContents encoding.
func TestGeminiWireEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1beta/models/text-embedding-004:batchEmbedContents", r.URL.Path)
		var body struct {
			Requests []struct {
				Model   string `json:"model"`
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"requests"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Len(t, body.Requests, 2)
		assert.Equal(t, "models/text-embedding-004", body.Requests[0].Model)
		assert.Equal(t, "first", body.Requests[0].Content.Parts[0].Text)
		w.Write([]byte(`{"embeddings": [{"values": [1, 0]}, {"values": [0, 1]}]}`))
	}))
	defer srv.Close()

	w := &geminiWire{base: srv.URL}
	vecs, err := w.Embed(context.Background(), "text-embedding-004", []string{"first", "second"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	assert.Equal(t, []float32{1, 0}, vecs[0])
}

// TestAnthropicHasNoEmbeddings pins the driver-level error: an Anthropic
// connection wired into a memory store must say why it cannot embed rather
// than fail per request.
func TestAnthropicHasNoEmbeddings(t *testing.T) {
	d := &Driver{provider: ProviderAnthropic, wire: &anthropicWire{}}
	_, err := d.Embed(context.Background(), "any", []string{"text"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no embeddings API")
}
