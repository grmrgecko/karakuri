package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// windowDriver builds a driver around a wire with empty window caches.
func windowDriver(t *testing.T, w wire, configured int) *Driver {
	t.Helper()
	return &Driver{
		kind: KindAI, wire: w, log: zap.NewNop(), contextWindow: configured,
		reported: map[string]reportedWindow{}, learned: map[string]int{},
	}
}

// TestContextWindowDetection pins where providers report a model's window,
// with fixtures shaped like the real responses: OpenRouter's /models
// context_length (z-ai/glm-4.7 listed 204,800 on 2026-11-08), llama-swap's
// per-model llama.cpp props behind its API key, and Gemini's inputTokenLimit.
// It also pins the precedence: a configured window wins, and a learned limit
// below a reported one replaces it.
func TestContextWindowDetection(t *testing.T) {
	openRouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"z-ai/glm-4.7","context_length":204800,"top_provider":{"context_length":202752}}]}`))
	}))
	defer openRouter.Close()
	llamaSwap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer swap-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"unauthorized: invalid or missing API key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gemma-4-26B","object":"model","owned_by":"llama-swap"}]}`))
		case "/upstream/gemma-4-26B/props":
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":32768},"total_slots":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer llamaSwap.Close()
	gemini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash", r.URL.Path)
		_, _ = w.Write([]byte(`{"name":"models/gemini-2.5-flash","inputTokenLimit":1048576,"outputTokenLimit":65536}`))
	}))
	defer gemini.Close()

	ctx := context.Background()
	router := windowDriver(t, &openaiWire{base: openRouter.URL + "/api/v1", probeProps: true}, 0)
	assert.Equal(t, 204800, router.ContextWindow(ctx, "z-ai/glm-4.7"), "OpenRouter lists context_length per model")
	router.LearnContextWindow("z-ai/glm-4.7", 202752)
	assert.Equal(t, 202752, router.ContextWindow(ctx, "z-ai/glm-4.7"), "a learned limit below the listing wins")

	swap := windowDriver(t, &openaiWire{base: llamaSwap.URL + "/v1", key: "swap-key", probeProps: true}, 0)
	assert.Equal(t, 32768, swap.ContextWindow(ctx, "gemma-4-26B"), "llama-swap reports llama.cpp's n_ctx per model")

	assert.Equal(t, 1048576, windowDriver(t, &geminiWire{base: gemini.URL}, 0).ContextWindow(ctx, "gemini-2.5-flash"),
		"Gemini reports inputTokenLimit")

	configured := windowDriver(t, &openaiWire{base: llamaSwap.URL + "/v1", key: "swap-key", probeProps: true}, 16000)
	assert.Equal(t, 16000, configured.ContextWindow(ctx, "gemma-4-26B"), "the connection's window overrides detection")
}
