package ai

import (
	"context"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// windowProbeTimeout bounds one window lookup so a slow listing never holds
// up the chat request that asked for it.
const windowProbeTimeout = 10 * time.Second

// reportedWindow is a provider-reported window and when it was read. A zero
// window records a lookup that found none, so misses are not retried on
// every request.
type reportedWindow struct {
	tokens int
	at     time.Time
}

// windowWire is implemented by wires whose provider reports a model's
// context window.
type windowWire interface {
	contextWindow(ctx context.Context, model string) (int, error)
}

// ContextWindow returns the prompt size in tokens the model accepts: the
// connection's configured window, else the smaller of the window the
// provider reports and one learned from an overflow error. Zero means
// unknown.
func (d *Driver) ContextWindow(ctx context.Context, model string) int {
	if d.contextWindow > 0 {
		return d.contextWindow
	}
	reported := d.reportedContextWindow(ctx, model)
	d.windowMu.Lock()
	learned := d.learned[model]
	d.windowMu.Unlock()
	switch {
	case reported > 0 && learned > 0:
		return min(reported, learned)
	case learned > 0:
		return learned
	default:
		return reported
	}
}

// LearnContextWindow records a window an overflow error named for a model.
func (d *Driver) LearnContextWindow(model string, tokens int) {
	if tokens <= 0 {
		return
	}
	d.windowMu.Lock()
	d.learned[model] = tokens
	d.windowMu.Unlock()
	d.log.Info("ai context window learned from overflow", zap.String("model", model), zap.Int("tokens", tokens))
}

func (d *Driver) reportedContextWindow(ctx context.Context, model string) int {
	ww, ok := d.wire.(windowWire)
	if !ok {
		return 0
	}
	d.windowMu.Lock()
	cached, found := d.reported[model]
	d.windowMu.Unlock()
	if found && time.Since(cached.at) < modelsTTL {
		return cached.tokens
	}
	probeCtx, cancel := context.WithTimeout(ctx, windowProbeTimeout)
	defer cancel()
	tokens, err := ww.contextWindow(probeCtx, model)
	if err != nil {
		d.log.Debug("ai context window lookup failed", zap.String("model", model), zap.Error(err))
	} else if tokens > 0 {
		d.log.Debug("ai context window reported", zap.String("model", model), zap.Int("tokens", tokens))
	}
	if ctx.Err() != nil {
		return 0
	}
	d.windowMu.Lock()
	d.reported[model] = reportedWindow{tokens: tokens, at: time.Now()}
	d.windowMu.Unlock()
	return tokens
}

// contextWindow reads the window from the model listing (OpenRouter's
// context_length, vLLM's max_model_len), then from llama-swap's per-model
// props and llama.cpp's props on generic compatible servers.
func (w *openaiWire) contextWindow(ctx context.Context, model string) (int, error) {
	var listing struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			MaxModelLen   int    `json:"max_model_len"`
		} `json:"data"`
	}
	listErr := doJSON(ctx, "GET", w.base+"/models", w.headers(), nil, &listing)
	if listErr == nil {
		for _, m := range listing.Data {
			if m.ID != model {
				continue
			}
			if m.ContextLength > 0 {
				return m.ContextLength, nil
			}
			if m.MaxModelLen > 0 {
				return m.MaxModelLen, nil
			}
		}
	}
	if !w.probeProps {
		return 0, listErr
	}
	root := strings.TrimSuffix(w.base, "/v1")
	var lastErr error
	for _, path := range []string{"/upstream/" + url.PathEscape(model) + "/props", "/props"} {
		var props struct {
			NCtx     int `json:"n_ctx"`
			Defaults struct {
				NCtx int `json:"n_ctx"`
			} `json:"default_generation_settings"`
		}
		if err := doJSON(ctx, "GET", root+path, w.headers(), nil, &props); err != nil {
			lastErr = err
			continue
		}
		if props.Defaults.NCtx > 0 {
			return props.Defaults.NCtx, nil
		}
		if props.NCtx > 0 {
			return props.NCtx, nil
		}
	}
	return 0, lastErr
}

// contextWindow reads a Gemini model's inputTokenLimit.
func (w *geminiWire) contextWindow(ctx context.Context, model string) (int, error) {
	var resp struct {
		InputTokenLimit int `json:"inputTokenLimit"`
	}
	if err := doJSON(ctx, "GET", w.base+"/v1beta/models/"+url.PathEscape(model), w.headers(), nil, &resp); err != nil {
		return 0, err
	}
	return resp.InputTokenLimit, nil
}
