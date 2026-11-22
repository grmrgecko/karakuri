// Package ai implements provider connections for OpenAI, compatible servers,
// Hermes Agent, Anthropic, Google Gemini, and Ollama. Three common wire formats
// cover their HTTP APIs, with a session-history extension for Hermes. A
// driver holds config and a client rather than a socket: there is nothing to
// keep connected, so Start probes the endpoint once for an honest status and
// node calls surface their own failures per run.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"go.uber.org/zap"
)

// ConnectionKinds returns the connection_kind filter for a FieldConnection
// spec that takes an AI provider. One kind now covers every provider, so the
// filter is the kind name.
func ConnectionKinds() string { return KindAI }

// probeTimeout bounds the startup and test-hook model listing.
const probeTimeout = 15 * time.Second

// modelsTTL bounds how long a listed model catalog is served from cache.
// Catalogs change rarely — a provider release, a llama-swap config edit — so
// anything fresher would spend a provider call per editor panel open.
const modelsTTL = 5 * time.Minute

// Config is the driver configuration stored (encrypted) on a connection. One
// shape serves every provider; which fields are required differs per provider
// and is enforced by validate.
type Config struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	// Auth picks the OpenAI provider's credential kind: an API key (the
	// default, and every other provider's only mode) or a ChatGPT account
	// through the Codex backend.
	Auth string `json:"auth,omitempty"`
	// ChatGPTAuth holds the OAuth tokens returned by OpenAI device login for
	// the chatgpt auth mode.
	ChatGPTAuth string `json:"chatgpt_auth,omitempty"`
	// ContextWindow is the prompt size in tokens every model on this
	// connection accepts. Zero detects it where the provider reports one.
	ContextWindow float64 `json:"context_window,omitempty"`
}

// chatGPTAuth reports whether a config uses the ChatGPT-account mode.
func (c Config) chatGPTAuth() bool {
	return c.Provider == ProviderOpenAI && c.Auth == AuthChatGPT
}

// newWire builds the wire a validated, normalized config speaks.
func newWire(cfg Config, persistSecrets func(map[string]string) error) (wire, error) {
	if cfg.chatGPTAuth() {
		return newCodexWire(cfg, persistSecrets)
	}
	def, ok := providerDef(cfg.Provider)
	if !ok {
		return nil, fmt.Errorf("unknown AI provider %q; the providers are %s", cfg.Provider, providerNames())
	}
	switch def.Wire {
	case WireAnthropic:
		return &anthropicWire{base: cfg.BaseURL, key: cfg.APIKey}, nil
	case WireGemini:
		return &geminiWire{base: cfg.BaseURL, key: cfg.APIKey}, nil
	default:
		base := cfg.BaseURL
		if def.AppendV1 {
			base = strings.TrimRight(base, "/") + "/v1"
		}
		// OpenAI retired max_tokens on its newest chat models in favor of
		// max_completion_tokens; several compatible servers reject the newer
		// name, so which one rides is the provider's LegacyMaxTokens.
		maxTokensKey := "max_completion_tokens"
		if def.LegacyMaxTokens {
			maxTokensKey = "max_tokens"
		}
		if cfg.Provider == ProviderHermes {
			return &hermesWire{openaiWire: openaiWire{
				base: base, key: cfg.APIKey, maxTokensKey: maxTokensKey,
			}}, nil
		}
		return &openaiWire{
			base: base, key: cfg.APIKey, maxTokensKey: maxTokensKey,
			promptCache: cfg.Provider == ProviderOpenAI,
			probeProps:  cfg.Provider == ProviderOpenAICompat,
		}, nil
	}
}

// normalize fills the provider's defaults into a config and trims what users
// paste: trailing slashes and whitespace.
func normalize(cfg Config) Config {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.BaseURL == "" {
		if def, ok := providerDef(cfg.Provider); ok {
			cfg.BaseURL = def.BaseURL
		}
	}
	return cfg
}

// validate checks a config against its provider's requirements.
func validate(cfg Config) error {
	cfg = normalize(cfg)
	def, ok := providerDef(cfg.Provider)
	if !ok {
		return fmt.Errorf("unknown AI provider %q; the providers are %s", cfg.Provider, providerNames())
	}
	if cfg.chatGPTAuth() {
		_, err := parseCodexAuth(cfg.ChatGPTAuth)
		return err
	}
	if def.NeedsKey && cfg.APIKey == "" {
		return fmt.Errorf("%s config has no api_key", cfg.Provider)
	}
	if cfg.ContextWindow < 0 {
		return fmt.Errorf("%s context_window must be a positive token count", cfg.Provider)
	}
	if cfg.BaseURL == "" {
		return fmt.Errorf("%s config has no base_url", cfg.Provider)
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s base_url %q is not an http(s) URL", cfg.Provider, cfg.BaseURL)
	}
	return nil
}

// Driver is one AI provider connection. It implements integrations.Driver and
// answers chat and model-list calls for the AI nodes; there is no session to
// supervise, so its lifecycle is a startup probe and a cancel.
type Driver struct {
	id       uuid.UUID
	kind     string
	provider string
	wire     wire

	log           *zap.Logger
	publishStatus func(integrations.Status)

	mu     sync.Mutex
	status integrations.Status
	cancel context.CancelFunc

	// modelsMu guards the model catalog cache, separate from mu so a slow
	// provider listing never blocks a status read.
	modelsMu sync.Mutex
	models   []string
	modelsAt time.Time

	// contextWindow is the connection's configured window; zero detects.
	contextWindow int
	// windowMu guards the per-model reported and learned windows.
	windowMu sync.Mutex
	reported map[string]reportedWindow
	learned  map[string]int
}

// New builds an AI provider driver from a connection model; the config's
// provider field picks the wire.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid ai config: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	cfg = normalize(cfg)
	w, err := newWire(cfg, deps.PersistSecrets)
	if err != nil {
		return nil, err
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:            model.ID,
		kind:          model.Kind,
		provider:      cfg.Provider,
		wire:          w,
		log:           log,
		publishStatus: deps.PublishStatus,
		status:        integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		contextWindow: int(cfg.ContextWindow),
		reported:      map[string]reportedWindow{},
		learned:       map[string]int{},
	}, nil
}

// ID returns the connection's database identifier.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return d.kind }

// SupportsTools reports whether models from this provider accept tool
// definitions. Hermes owns its tool loop and therefore returns false.
func (d *Driver) SupportsTools() bool {
	def, ok := providerDef(d.provider)
	return ok && def.SupportsTools
}

// Status returns the connection's current lifecycle state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// setStatus records a state transition and relays it to the manager.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	publish := d.publishStatus
	d.mu.Unlock()

	d.log.Info("ai connection status", zap.String("kind", d.kind), zap.String("state", state), zap.String("error", errMsg))
	if publish != nil {
		publish(status)
	}
}

// Start probes the endpoint once in the background so the status reflects a
// reachable, authenticated provider rather than a config that merely parsed.
// It never re-probes: the API is stateless HTTP, listing models costs a call,
// and a node's own failure is the meaningful signal after startup.
func (d *Driver) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	go func() {
		probeCtx, done := context.WithTimeout(runCtx, probeTimeout)
		defer done()
		// The probe lists models anyway, so it seeds the catalog cache: the
		// editor's first completion usually costs nothing.
		if _, err := d.RefreshModels(probeCtx); err != nil {
			if runCtx.Err() == nil {
				d.setStatus(integrations.StateError, err.Error())
			}
			return
		}
		d.setStatus(integrations.StateConnected, "")
	}()
	return nil
}

// Stop marks the connection disabled.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// Chat runs one model invocation. A failure flips the status to error so the
// connections panel reports what a run just learned; a success flips it back.
// Token spend and call outcomes land in the Prometheus counters here, at the
// one point every chat crosses whichever node made it.
func (d *Driver) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	resp, err := d.wire.Chat(ctx, req)
	return d.finishChat(ctx, req, resp, err)
}

// Stream runs one model invocation and emits visible text deltas. Providers
// without a streaming wire emit the completed answer once.
func (d *Driver) Stream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	return d.StreamEvents(ctx, req, func(event StreamEvent) error {
		if event.Type != StreamEventContentDelta {
			return nil
		}
		return emit(event.Delta)
	})
}

// StreamEvents runs one model invocation and emits text and provider progress.
func (d *Driver) StreamEvents(ctx context.Context, req ChatRequest, emit func(StreamEvent) error) (*ChatResponse, error) {
	var resp *ChatResponse
	var err error
	if streaming, ok := d.wire.(eventStreamingWire); ok {
		resp, err = streaming.ChatEventStream(ctx, req, emit)
	} else if streaming, ok := d.wire.(streamingWire); ok {
		resp, err = streaming.ChatStream(ctx, req, func(delta string) error {
			return emit(StreamEvent{Type: StreamEventContentDelta, Delta: delta})
		})
	} else {
		resp, err = d.wire.Chat(ctx, req)
		if err == nil && resp.Text != "" {
			err = emit(StreamEvent{Type: StreamEventContentDelta, Delta: resp.Text})
		}
	}
	return d.finishChat(ctx, req, resp, err)
}

func (d *Driver) finishChat(ctx context.Context, req ChatRequest, resp *ChatResponse, err error) (*ChatResponse, error) {
	if err != nil {
		metrics.AIRequestsTotal.WithLabelValues(d.id.String(), req.Model, "chat", "error").Inc()
		if ctx.Err() == nil {
			d.setStatus(integrations.StateError, err.Error())
		}
		return nil, err
	}
	metrics.AIRequestsTotal.WithLabelValues(d.id.String(), req.Model, "chat", "ok").Inc()
	resp.PromptTokens = resp.Usage.InputTokens
	if _, ok := d.wire.(*anthropicWire); ok {
		// Anthropic's input count excludes cache reads and writes.
		resp.PromptTokens += resp.Usage.CachedInputTokens + resp.Usage.CacheWriteInputTokens
	}
	metrics.AITokensTotal.WithLabelValues(d.id.String(), req.Model, "input").Add(float64(resp.Usage.InputTokens))
	metrics.AITokensTotal.WithLabelValues(d.id.String(), req.Model, "output").Add(float64(resp.Usage.OutputTokens))
	metrics.AITokensTotal.WithLabelValues(d.id.String(), req.Model, "cached_input").Add(float64(resp.Usage.CachedInputTokens))
	metrics.AITokensTotal.WithLabelValues(d.id.String(), req.Model, "cache_write_input").Add(float64(resp.Usage.CacheWriteInputTokens))
	d.setStatus(integrations.StateConnected, "")
	return resp, nil
}

// Models lists the model IDs the provider serves, answered from the cache
// while it is younger than modelsTTL and refreshed from the provider when it
// is not. Callers get a copy, so the cache is never aliased.
func (d *Driver) Models(ctx context.Context) ([]string, error) {
	d.modelsMu.Lock()
	if d.models != nil && time.Since(d.modelsAt) < modelsTTL {
		cached := append([]string(nil), d.models...)
		d.modelsMu.Unlock()
		return cached, nil
	}
	d.modelsMu.Unlock()
	return d.RefreshModels(ctx)
}

// RefreshModels lists models from the provider and replaces the cache. A
// failed listing leaves the previous catalog in place and reports the error,
// so the caller decides whether stale is acceptable.
func (d *Driver) RefreshModels(ctx context.Context) ([]string, error) {
	models, err := d.wire.Models(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(models)
	d.modelsMu.Lock()
	d.models = models
	d.modelsAt = time.Now()
	d.modelsMu.Unlock()
	return append([]string(nil), models...), nil
}

// TestConnection verifies a config by listing the endpoint's models with a
// throwaway wire, used as the kind's Test hook.
func TestConnection() func(ctx context.Context, id uuid.UUID, cfgJSON []byte) error {
	return func(ctx context.Context, _ uuid.UUID, cfgJSON []byte) error {
		var cfg Config
		if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
			return fmt.Errorf("invalid ai config: %w", err)
		}
		if err := validate(cfg); err != nil {
			return err
		}
		w, err := newWire(normalize(cfg), nil)
		if err != nil {
			return err
		}
		probeCtx, done := context.WithTimeout(ctx, probeTimeout)
		defer done()
		_, err = w.Models(probeCtx)
		return err
	}
}
