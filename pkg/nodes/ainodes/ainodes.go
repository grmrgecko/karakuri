// Package ainodes implements the AI node set: a model node naming which
// provider and model to use, an agent node running a templated prompt through
// a tool-calling loop, a basic chain for calls without tools or memory,
// tool provider nodes (an MCP server's catalog, and the
// graph-backed Tool node whose body is a branch of ordinary nodes), a direct
// MCP call node, the guardrail pair — a check node routing text to pass or
// fail and a sanitizer redacting it — the structured-output pair (an
// extractor filling declared attributes and a classifier routing by
// category), and the memory nodes, ingest included, that make a store a RAG
// knowledge base. The agent is a cluster of nodes built on
// karakuri's own machinery: capabilities are payloads on typed value edges
// rather than a special mechanism — a model handle and a tool set are pulled
// from pure source nodes exactly the way any parameter is, the agent's model
// input taking one wire and its tools input taking many.
package ainodes

import (
	"encoding/json"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/engine"
)

// Port type names the AI nodes register. Capability types rather than data
// types: neither is a subflow value parameter nor declarable on a script
// port, so a tool set can only ever come from a node that builds one.
const (
	PortAIModel  = "ai-model"
	PortAITool   = "ai-tool"
	PortAIMemory = "ai-memory"
	PortAIStream = "ai-stream"
)

// modelHandle is the ai-model payload: which connection to talk through,
// which model to ask for, and the sampling settings that ride every request.
type modelHandle struct {
	Connection      string   `json:"connection"`
	Model           string   `json:"model"`
	SupportsTools   bool     `json:"supports_tools"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	ReasoningBudget int      `json:"reasoning_budget,omitempty"`
	ContextWindow   int      `json:"context_window,omitempty"`
	CompactionOff   bool     `json:"compaction_off,omitempty"`
	CompactionBuf   string   `json:"compaction_buffer,omitempty"`
}

// toolSet is the ai-tool payload: the tools one provider node contributes.
// One node may carry many (an MCP server's whole catalog), so the payload is
// always a set and the agent concatenates the sets it pulls.
type toolSet struct {
	Tools []toolSpec `json:"tools"`
}

// toolSpec is one tool offered to the model: what the model sees (name,
// description, argument schema) plus how the agent runs it when called.
type toolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema,omitempty"`
	Invoke      invokeSpec     `json:"invoke"`
}

// invokeSpec says how a tool call is executed. Kind picks the dispatcher and
// the other fields belong to it: an MCP tool names its connection and remote
// name, a graph-backed tool names its node and the timeout one call may run,
// and a memory tool names its store and the session it is scoped to.
type invokeSpec struct {
	Kind       string `json:"kind"`
	Connection string `json:"connection,omitempty"`
	Tool       string `json:"tool,omitempty"`

	Node       string  `json:"node,omitempty"`
	TimeoutSec float64 `json:"timeout_sec,omitempty"`

	Store       string `json:"store,omitempty"`
	Session     string `json:"session,omitempty"`
	RecallTurns bool   `json:"recall_turns,omitempty"`
	Rerank      string `json:"rerank,omitempty"`
}

// Invoke kinds.
const (
	invokeMCP      = "mcp"
	invokeBranch   = "branch"
	invokeRemember = "memory-remember"
	invokeRecall   = "memory-recall"
)

// RegisterAll registers the AI node set on a node registry.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(ModelNode{})
	reg.Register(AgentNode{})
	reg.Register(BasicChainNode{})
	reg.Register(MCPToolsNode{})
	reg.Register(ToolNode{})
	reg.Register(MCPCallNode{})
	reg.Register(GuardrailNode{})
	reg.Register(SanitizeNode{})
	reg.Register(ExtractNode{})
	reg.Register(ClassifyNode{})
	reg.Register(SummarizeNode{})
	reg.Register(MemoryNode{})
	reg.Register(IngestNode{})
	reg.Register(MemoryRememberNode{})
	reg.Register(MemoryRecallNode{})
}

// jsonShaped returns v as the decoded JSON it encodes to: objects as
// map[string]any, lists as []any, and numbers as float64. Message metadata
// holds that shape because a property path, a switch, and a split read
// nothing else. A struct or a typed slice set there was invisible to them in
// the run that produced it and readable only on a rerun, when history handed
// it back as JSON. A value that does not encode is returned as it is.
func jsonShaped(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var shaped any
	if err := json.Unmarshal(raw, &shaped); err != nil {
		return v
	}
	return shaped
}

// stringifyResult renders a tool result as the text a model reads: strings
// pass through, everything else is its JSON encoding.
func stringifyResult(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(encoded)
}
