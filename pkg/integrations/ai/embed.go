package ai

import (
	"context"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/metrics"
)

// Embeddings are the second capability an AI connection can serve: the memory
// stores use them for semantic recall. Not every provider has them — Anthropic
// ships no embeddings API — so the wire method is allowed to answer with a
// plain "this provider cannot" error, and the caller falls back to keyword
// scoring rather than failing the store.

// embedder is the optional wire capability. A wire that cannot embed returns
// an error naming the provider, so the operator learns which connection to
// swap rather than which request failed.
type embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// Embed produces one embedding vector per input text, in input order. Like
// Chat, a failure flips the connection status so the panel reports what a
// store just learned.
func (d *Driver) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	e, ok := d.wire.(embedder)
	if !ok {
		return nil, fmt.Errorf("the %s provider has no embeddings API", d.provider)
	}
	vecs, err := e.Embed(ctx, model, texts)
	if err != nil {
		metrics.AIRequestsTotal.WithLabelValues(d.id.String(), model, "embed", "error").Inc()
		if ctx.Err() == nil {
			d.setStatus(integrations.StateError, err.Error())
		}
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("provider returned %d embeddings for %d texts", len(vecs), len(texts))
	}
	metrics.AIRequestsTotal.WithLabelValues(d.id.String(), model, "embed", "ok").Inc()
	d.setStatus(integrations.StateConnected, "")
	return vecs, nil
}

// Embed implements embedder over POST /embeddings, the endpoint OpenAI and the
// compatible servers share.
func (w *openaiWire) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	body := map[string]any{"model": model, "input": texts}
	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := doJSON(ctx, "POST", w.base+"/embeddings", w.headers(), body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings response has %d vectors for %d inputs", len(resp.Data), len(texts))
	}
	// The API documents result order as input order but carries an index
	// anyway; honoring it costs nothing and survives a server that reorders.
	out := make([][]float32, len(texts))
	for i, d := range resp.Data {
		idx := d.Index
		if idx < 0 || idx >= len(out) {
			idx = i
		}
		out[idx] = d.Embedding
	}
	return out, nil
}

// Embed implements embedder over POST /v1beta/models/{model}:batchEmbedContents.
func (w *geminiWire) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	requests := make([]map[string]any, 0, len(texts))
	for _, text := range texts {
		requests = append(requests, map[string]any{
			"model":   "models/" + model,
			"content": map[string]any{"parts": []map[string]any{{"text": text}}},
		})
	}
	var resp struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	url := w.base + "/v1beta/models/" + model + ":batchEmbedContents"
	if err := doJSON(ctx, "POST", url, w.headers(), map[string]any{"requests": requests}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embeddings response has %d vectors for %d inputs", len(resp.Embeddings), len(texts))
	}
	out := make([][]float32, len(texts))
	for i, e := range resp.Embeddings {
		out[i] = e.Values
	}
	return out, nil
}
