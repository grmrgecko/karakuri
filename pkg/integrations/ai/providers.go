package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// KindAI is the one AI provider connection kind. Which provider a connection
// talks to is its config's provider field, picked from the registry below —
// one kind in the connections list, however many providers are registered.
const KindAI = "ai"

// Wire format names a provider definition may pick from. Three JSON-over-HTTP
// formats cover every current provider; a plugin's provider names one of
// these rather than bringing code, because most "new" providers are the
// chat-completions shape at a different address.
const (
	WireChatCompletions = "chat-completions"
	WireAnthropic       = "anthropic"
	WireGemini          = "gemini"
)

// ProviderDef describes one AI provider as data: identity for the picker, the
// wire format it speaks, its default endpoint, and the knobs the
// chat-completions wire varies by.
type ProviderDef struct {
	// Name is the identifier a connection's provider field stores.
	Name string `json:"name"`
	// Label is the display name shown in the provider picker.
	Label string `json:"label"`
	// Wire names the request format: chat-completions, anthropic, or gemini.
	Wire string `json:"wire"`
	// BaseURL is the provider's default endpoint; empty means the operator
	// must supply one.
	BaseURL string `json:"base_url,omitempty"`
	// NeedsKey requires an API key in the config.
	NeedsKey bool `json:"needs_key,omitempty"`
	// SupportsTools allows models from this provider to receive tool
	// definitions and return tool calls.
	SupportsTools bool `json:"supports_tools,omitempty"`
	// LegacyMaxTokens sends max_tokens instead of max_completion_tokens on the
	// chat-completions wire; several compatible servers reject the newer name.
	LegacyMaxTokens bool `json:"legacy_max_tokens,omitempty"`
	// AppendV1 appends /v1 to the base URL, for providers whose conventional
	// address is the server root (Ollama).
	AppendV1 bool `json:"append_v1,omitempty"`
}

// Built-in provider names. ProviderOpenAI is also where ChatGPT-account
// authentication lives, as its auth mode.
const (
	ProviderOpenAI       = "openai"
	ProviderOpenAICompat = "openai-compatible"
	ProviderHermes       = "hermes"
	ProviderAnthropic    = "anthropic"
	ProviderGemini       = "gemini"
	ProviderOllama       = "ollama"
)

// providerRegistry holds the registered providers in registration order,
// built-ins first. Owner tags plugin registrations so an unload withdraws
// exactly what the plugin brought.
var (
	providerMu    sync.RWMutex
	providerOrder []string
	providerByID  = map[string]ProviderDef{}
	providerOwner = map[string]string{}
)

func init() {
	builtins := []ProviderDef{
		{Name: ProviderOpenAI, Label: "OpenAI", Wire: WireChatCompletions,
			BaseURL: "https://api.openai.com/v1", NeedsKey: true, SupportsTools: true},
		{Name: ProviderOpenAICompat, Label: "OpenAI-compatible server", Wire: WireChatCompletions,
			LegacyMaxTokens: true, SupportsTools: true},
		{Name: ProviderHermes, Label: "Hermes Agent", Wire: WireChatCompletions,
			BaseURL: "http://127.0.0.1:8642/v1", NeedsKey: true, LegacyMaxTokens: true},
		{Name: ProviderAnthropic, Label: "Anthropic", Wire: WireAnthropic,
			BaseURL: "https://api.anthropic.com", NeedsKey: true, SupportsTools: true},
		{Name: ProviderGemini, Label: "Google Gemini", Wire: WireGemini,
			BaseURL: "https://generativelanguage.googleapis.com", NeedsKey: true, SupportsTools: true},
		{Name: ProviderOllama, Label: "Ollama", Wire: WireChatCompletions,
			BaseURL: "http://localhost:11434", LegacyMaxTokens: true, AppendV1: true, SupportsTools: true},
	}
	for _, def := range builtins {
		if err := RegisterProvider(def, ""); err != nil {
			panic(err)
		}
	}
}

// RegisterProvider adds a provider to the registry, owner-tagged for
// withdrawal (built-ins use the empty owner and are never withdrawn). When
// the AI connection kind is already registered its schema is re-registered,
// so the provider picker a plugin extends updates without a restart.
func RegisterProvider(def ProviderDef, owner string) error {
	def.Name = strings.TrimSpace(def.Name)
	def.Label = strings.TrimSpace(def.Label)
	if def.Name == "" {
		return fmt.Errorf("ai provider has no name")
	}
	if def.Label == "" {
		def.Label = def.Name
	}
	switch def.Wire {
	case WireChatCompletions, WireAnthropic, WireGemini:
	default:
		return fmt.Errorf("ai provider %q names unknown wire %q; the wires are %s, %s, %s",
			def.Name, def.Wire, WireChatCompletions, WireAnthropic, WireGemini)
	}

	providerMu.Lock()
	if cur, exists := providerOwner[def.Name]; exists && cur != owner {
		providerMu.Unlock()
		return fmt.Errorf("ai provider %q is already registered", def.Name)
	} else if !exists {
		providerOrder = append(providerOrder, def.Name)
	}
	providerByID[def.Name] = def
	providerOwner[def.Name] = owner
	providerMu.Unlock()

	refreshKind()
	return nil
}

// UnregisterProviders withdraws every provider an owner registered, which is
// what a plugin unload calls.
func UnregisterProviders(owner string) {
	if owner == "" {
		return
	}
	providerMu.Lock()
	kept := providerOrder[:0]
	for _, name := range providerOrder {
		if providerOwner[name] == owner {
			delete(providerByID, name)
			delete(providerOwner, name)
			continue
		}
		kept = append(kept, name)
	}
	providerOrder = kept
	providerMu.Unlock()

	refreshKind()
}

// Providers lists the registered providers in registration order.
func Providers() []ProviderDef {
	providerMu.RLock()
	defer providerMu.RUnlock()
	out := make([]ProviderDef, 0, len(providerOrder))
	for _, name := range providerOrder {
		out = append(out, providerByID[name])
	}
	return out
}

// providerDef resolves one provider by name.
func providerDef(name string) (ProviderDef, bool) {
	providerMu.RLock()
	defer providerMu.RUnlock()
	def, ok := providerByID[name]
	return def, ok
}

// providerNames renders the registry as the list an error message offers.
func providerNames() string {
	providerMu.RLock()
	defer providerMu.RUnlock()
	return strings.Join(providerOrder, ", ")
}

// refreshKind re-registers the AI connection kind so its provider picker
// matches the registry. A registration before the kind exists (the built-in
// init, a plugin loading early) is a no-op; the kind registration that
// follows builds from the registry anyway.
func refreshKind() {
	if _, ok := integrations.KindFor(KindAI); !ok {
		return
	}
	integrations.RegisterKind(KindDef())
}

// Auth modes for the OpenAI provider.
const (
	AuthAPIKey  = "api_key"
	AuthChatGPT = "chatgpt"
)

// KindDef returns the AI provider connection kind: one kind whose provider
// field picks from the registry, replacing the kind-per-provider split. The
// per-provider requirements the schema cannot express (which provider needs a
// key, which a base URL) live in Validate, which names what is missing.
func KindDef() integrations.ConnectionKind {
	options := make([]flow.Option, 0, 8)
	for _, def := range Providers() {
		options = append(options, flow.Option{Value: def.Name, Label: def.Label})
	}

	return integrations.ConnectionKind{
		Kind:  KindAI,
		Label: "AI Provider",
		ConfigSpec: []flow.FieldSpec{
			{
				ID: "provider", Label: "Provider", Type: flow.FieldSelect,
				Required: true, Default: ProviderOpenAI, Options: options,
				Help: "Which provider the connection talks to. Plugins can add more.",
			},
			{
				ID: "auth", Label: "Authentication", Type: flow.FieldSelect,
				ShowIf:  &flow.ShowIf{Field: "provider", Equals: ProviderOpenAI},
				Default: AuthAPIKey,
				Options: []flow.Option{
					{Value: AuthAPIKey, Label: "API key"},
					{Value: AuthChatGPT, Label: "ChatGPT account (Codex)"},
				},
				Help: "API key uses platform.openai.com billing. ChatGPT account uses a Plus/Pro subscription through the Codex backend and opens OpenAI sign-in in the browser.",
			},
			{
				ID: "base_url", Label: "Base URL", Type: flow.FieldString,
				ShowIf:      &flow.ShowIf{Field: "auth", Equals: []string{"", AuthAPIKey}},
				Placeholder: "http://localhost:1234/v1",
				Help:        "Endpoint URL; empty uses the provider's default. An OpenAI-compatible server has no default — give its URL including the version prefix (http://localhost:1234/v1 for LM Studio or llama-swap). Ollama takes its address alone; the /v1 is appended.",
			},
			{
				ID: "api_key", Label: "API key", Type: flow.FieldString,
				Control: "password",
				ShowIf:  &flow.ShowIf{Field: "auth", Equals: []string{"", AuthAPIKey}},
				Help:    "API key from the provider's console. Local servers usually take none; a provider that requires one says so when tested.",
			},
			{
				ID: "context_window", Label: "Context window", Type: flow.FieldNumber,
				Placeholder: "Detect",
				Help:        "Tokens a request may hold, for every model on this connection. Empty reads it from OpenRouter, vLLM, llama.cpp, llama-swap, and Gemini, or learns it from the first overflow error. Ollama needs it set to its num_ctx. AI Model nodes can override it.",
			},
		},
		Secrets: []string{"api_key", "chatgpt_auth"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid ai config: %w", err)
			}
			return validate(c)
		},
		Test: TestConnection(),
	}
}
