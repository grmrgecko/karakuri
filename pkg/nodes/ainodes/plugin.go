package ainodes

import (
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/integrations/mcpconn"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// aiPlugin registers the AI node set, the provider and MCP connection kinds,
// and the AI capability port types.
type aiPlugin struct{}

// Plugin returns the built-in AI plugin.
func Plugin() plugin.Plugin { return aiPlugin{} }

// Manifest describes the plugin.
func (aiPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "ai",
		Version:     "1.0.0",
		Description: "AI provider connections, agent nodes, and MCP tools",
		Builtin:     true,
	}
}

// Register registers the plugin's contributions.
func (aiPlugin) Register(r plugin.Registrar) error {
	// The capability types are neither value parameters nor script port
	// types: a model handle or tool set only ever comes from a node built to
	// produce one, which is what makes the agent's inputs checkable.
	r.RegisterPortType(flow.PortTypeDef{
		Name: PortAIModel, Label: "AI Model",
		Accepts: "a model handle from an AI Model node",
	})
	r.RegisterPortType(flow.PortTypeDef{
		Name: PortAITool, Label: "AI Tools",
		Accepts: "a tool set from an MCP Tools or Tool node",
	})
	r.RegisterPortType(flow.PortTypeDef{
		Name: PortAIMemory, Label: "AI Memory",
		Accepts: "a memory handle from an AI Memory node",
	})
	r.RegisterPortType(flow.PortTypeDef{
		Name: PortAIStream, Label: "AI Stream",
		Accepts: "a streamed response from AI Agent or AI Basic Chain",
	})

	r.RegisterConnectionKind(ai.KindDef())
	r.RegisterConnectionKind(mcpconn.KindDef())

	// The model field completes on the connection's listed catalog; a frontend
	// without the control falls back to the plain string editor.
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "ai-model", Base: flow.FieldString,
		Description: "Model identifier, completed from the selected AI connection's model list",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "memory-store", Base: flow.FieldString,
		Description: "Memory store, picked from the stores defined in settings",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "transcript", Base: flow.FieldJSON,
		Description: "Conversation entries edited as role and message rows, stored in the agent's transcript format",
	})

	RegisterAll(plugin.NodeRegistry{R: r})
	return nil
}
