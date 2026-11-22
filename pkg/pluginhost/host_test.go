package pluginhost_test

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/nodes/hanodes"
	"github.com/grmrgecko/karakuri/pkg/nodes/mqttnodes"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/pluginhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuiltinPluginsRegisterEverything covers the whole registration loop the
// server runs at startup: loading the built-in plugins must yield the node
// types, connection kinds, and field controls the rest of the system depends
// on, all through the plugin surface rather than direct registration.
func TestBuiltinPluginsRegisterEverything(t *testing.T) {
	reg := engine.NewRegistry()
	host := pluginhost.NewHost(reg)
	for _, p := range []plugin.Plugin{core.Plugin(), mqttnodes.Plugin(), hanodes.Plugin()} {
		require.NoErrorf(t, host.Load(p), "the built-in plugin %q must load", p.Manifest().Name)
	}
	require.Len(t, host.Plugins(), 3, "all three built-ins must be loaded before their contributions are checked")

	// One node type per plugin, plus a storage node, so a plugin that
	// registered nothing shows up as a missing type rather than a lower count.
	for _, nodeType := range []string{"manual", "delay", "mqtt-in", "ha-get-state", "file-read"} {
		_, ok := reg.Get(nodeType)
		assert.Truef(t, ok, "node type %q must reach the registry through the plugin surface", nodeType)
	}

	for _, kind := range []string{"mqtt", "homeassistant"} {
		def, ok := integrations.KindFor(kind)
		if !assert.Truef(t, ok, "connection kind %q must be registered by its plugin", kind) {
			continue
		}
		assert.NotEmptyf(t, def.ConfigSpec, "%q must declare a config schema, or the connection form has no fields", kind)
		assert.NotNilf(t, def.Factory, "%q must supply a factory, or a connection of that kind cannot be built", kind)
		assert.NotNilf(t, def.Test, "%q must supply a test hook, or the Test button on the connection form does nothing", kind)
	}

	for _, control := range []string{"password", "list", "map"} {
		_, ok := flow.FieldControlFor(control)
		assert.Truef(t, ok, "field control %q must be registered, or the editor falls back to a plain text box", control)
	}

	// The built-in port types come from flow's own init, through the same
	// registry a plugin's types land in.
	def, ok := flow.PortTypeFor(flow.PortInteger)
	require.True(t, ok, "the built-in port types must be in the same registry a plugin's land in")
	assert.Len(t, def.WidensTo, 2, "an integer widens to float and to number: %+v", def)
}

// TestPluginTypeExtendsCompatibility covers the point of registrable types: a
// plugin type declaring a widening participates in Compatible with no code
// changed anywhere else.
func TestPluginTypeExtendsCompatibility(t *testing.T) {
	host := pluginhost.NewHost(engine.NewRegistry())
	host.RegisterPortType(flow.PortTypeDef{
		Name: "temperature", Label: "Temperature",
		WidensTo: []string{flow.PortNumber}, Value: true,
	})

	assert.True(t, flow.Compatible("temperature", flow.PortNumber),
		"a plugin type's declared widening must satisfy Compatible with no code changed in flow")
	assert.False(t, flow.Compatible(flow.PortNumber, "temperature"),
		"widening is directional: a number is not a temperature, so the reverse must not hold")
	assert.True(t, flow.IsValuePortType("temperature"),
		"a plugin type opting into Value must be bindable over a value edge, like the built-in value types")
}

// halfNode is a node type that exists only to be registered.
type halfNode struct{}

func (halfNode) Meta() engine.NodeMeta { return engine.NodeMeta{Type: "half-node", Label: "Half"} }
func (halfNode) Execute(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return nil, nil
}

// halfPlugin registers a node type, a connection kind, and a valid AI
// provider, then one the registry refuses.
type halfPlugin struct{}

func (halfPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Name: "half-plugin", Version: "1.0.0"}
}

func (halfPlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(halfNode{})
	r.RegisterConnectionKind(integrations.ConnectionKind{Kind: "half-kind", Label: "Half"})
	r.RegisterAIProvider(plugin.AIProviderDef{Name: "half-provider", Wire: "chat-completions"})
	r.RegisterAIProvider(plugin.AIProviderDef{Name: "half-refused", Wire: "no-such-wire"})
	return nil
}

// TestFailedLoadWithdrawsContributions pins that a plugin whose registration
// fails partway leaves nothing behind. The plugin is not in the catalog, so
// nothing could unload it: its node types and kinds stayed in the palette,
// backed by a process the manager had already killed.
func TestFailedLoadWithdrawsContributions(t *testing.T) {
	reg := engine.NewRegistry()
	host := pluginhost.NewHost(reg)
	t.Cleanup(func() {
		integrations.UnregisterKind("half-kind")
		ai.UnregisterProviders("half-plugin")
	})

	require.Error(t, host.Load(halfPlugin{}), "a refused contribution must fail the load")
	assert.False(t, host.IsLoaded("half-plugin"), "a failed load must not enter the catalog")

	_, ok := reg.Get("half-node")
	assert.False(t, ok, "the node type registered before the failure must be withdrawn")
	_, ok = integrations.KindFor("half-kind")
	assert.False(t, ok, "the connection kind registered before the failure must be withdrawn")
	names := make([]string, 0)
	for _, def := range ai.Providers() {
		names = append(names, def.Name)
	}
	assert.NotContains(t, names, "half-provider", "the AI provider registered before the failure must be withdrawn")
}
