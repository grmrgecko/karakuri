package pluginhost

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// providerPlugin is a plugin contributing one AI provider and nothing else.
type providerPlugin struct{}

func (providerPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Name: "groq-provider", Version: "1.0.0"}
}

func (providerPlugin) Register(r plugin.Registrar) error {
	r.RegisterAIProvider(plugin.AIProviderDef{
		Name: "groq", Label: "Groq", Wire: "chat-completions",
		BaseURL: "https://api.groq.com/openai/v1", NeedsKey: true,
		SupportsTools: true, LegacyMaxTokens: true,
	})
	return nil
}

// aiProviderOptions reads the provider picker options out of the registered
// AI connection kind's schema, the way the settings form would.
func aiProviderOptions(t *testing.T) []string {
	t.Helper()
	kind, ok := integrations.KindFor(ai.KindAI)
	require.True(t, ok)
	for _, f := range kind.ConfigSpec {
		if f.ID != "provider" {
			continue
		}
		names := make([]string, 0, len(f.Options))
		for _, o := range f.Options {
			names = append(names, o.Value)
		}
		return names
	}
	t.Fatal("the ai kind has no provider field")
	return nil
}

// TestPluginAIProviderLifecycle covers the extension point the single AI kind
// exists for: a plugin's data-only provider lands in the registry, the kind's
// provider picker re-registers to offer it, and unloading withdraws exactly
// it — the built-ins stay.
func TestPluginAIProviderLifecycle(t *testing.T) {
	// The kind must be registered for the picker refresh to have anywhere to
	// land, the way the AI plugin registers it at boot.
	integrations.RegisterKind(ai.KindDef())
	t.Cleanup(func() { integrations.UnregisterKind(ai.KindAI) })

	host := NewHost(engine.NewRegistry())
	require.NoError(t, host.Load(providerPlugin{}))
	t.Cleanup(func() { ai.UnregisterProviders("groq-provider") })

	assert.Contains(t, aiProviderOptions(t), "groq", "the picker offers the plugin's provider without a restart")
	contrib, ok := host.ContributionsOf("groq-provider")
	require.True(t, ok)
	assert.Equal(t, []string{"groq"}, contrib.AIProviders)

	require.NoError(t, host.Unload("groq-provider"))
	options := aiProviderOptions(t)
	assert.NotContains(t, options, "groq", "unloading withdraws the provider and refreshes the picker")
	assert.Contains(t, options, "openai", "built-ins survive a plugin unload")

	// A second plugin claiming a taken name must fail the load, not silently
	// replace someone else's provider.
	require.NoError(t, host.Load(providerPlugin{}))
	err := host.Load(claimingPlugin{})
	require.Error(t, err, "a name collision across plugins is a load failure")
}

// claimingPlugin tries to register a provider name another plugin owns.
type claimingPlugin struct{}

func (claimingPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Name: "other-plugin", Version: "1.0.0"}
}

func (claimingPlugin) Register(r plugin.Registrar) error {
	r.RegisterAIProvider(plugin.AIProviderDef{Name: "groq", Label: "Groq again", Wire: "chat-completions"})
	return nil
}
