package ainodes

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/mcpconn"
)

// MCPCallNode invokes one MCP tool directly from the flow, no model involved:
// an MCP server named once as a connection is also just an API a flow can
// call. The tool-level error flag fails the node onto its error branch here —
// unlike the agent, there is no model to read the failure as an observation.
type MCPCallNode struct{}

// Meta describes the mcp-call node type.
func (MCPCallNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "mcp-call",
		Label:          "MCP Call",
		Folder:         flow.FolderAI,
		Description:    "Calls one tool on an MCP server and emits its result",
		Icon:           "las la-plug",
		ConnectionKind: mcpconn.Kind,
		Retryable:      true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "connection", Label: "Connection", Type: flow.FieldConnection,
				Required: true, ConnectionKind: mcpconn.Kind,
				Help: "MCP server connection to call.",
			},
			{
				ID: "tool", Label: "Tool", Type: flow.FieldString, Required: true,
				Placeholder: "search_issues",
				Help:        "Tool name as the server lists it.",
			},
			{
				ID: "args", Label: "Arguments", Type: flow.FieldJSON,
				Placeholder: `{"query": "{{ payload.query }}"}`,
				Help:        "Arguments object. String values support {{ }} expression segments rendered against the message.",
			},
		},
	}
}

// mcpCallConfig is the decoded mcp-call node configuration.
type mcpCallConfig struct {
	Connection string         `json:"connection"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
}

// Execute renders the arguments against the message and calls the tool.
func (MCPCallNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg mcpCallConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	tool := strings.TrimSpace(cfg.Tool)
	if tool == "" {
		return nil, fmt.Errorf("mcp-call node has no tool")
	}
	conn, err := resolveMCPConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}

	args, err := interpolateArgs(ec, cfg.Args, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering arguments: %w", err)
	}
	res, err := conn.CallTool(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, fmt.Errorf("tool %q failed: %s", tool, stringifyResult(res.Value))
	}

	meta := nodeutil.CloneMeta(msg.Meta)
	meta["tool"] = tool
	// A tool's images ride the metadata as base64 data URLs — mcp-call has no
	// model to show them to, but a flow can route or store them rather than
	// have them vanish.
	if len(res.Images) > 0 {
		urls := make([]any, 0, len(res.Images))
		for _, im := range res.Images {
			mime := im.MIME
			if mime == "" {
				mime = "image/png"
			}
			urls = append(urls, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(im.Data))
		}
		meta["images"] = urls
	}
	return engine.One("out", &engine.Msg{Payload: res.Value, Topic: tool, Meta: meta, Group: msg.Group}), nil
}

// interpolateArgs renders every string leaf of the arguments document against
// the message, so an argument reads {{ payload.query }} the way every other
// templated field does. Keys and non-string values pass through untouched.
func interpolateArgs(ec engine.ExecContext, doc map[string]any, msg *engine.Msg) (map[string]any, error) {
	if doc == nil {
		return map[string]any{}, nil
	}
	rendered, err := interpolateValue(ec, any(doc), msg)
	if err != nil {
		return nil, err
	}
	return rendered.(map[string]any), nil
}

func interpolateValue(ec engine.ExecContext, v any, msg *engine.Msg) (any, error) {
	switch t := v.(type) {
	case string:
		return ec.Interpolate(t, msg)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			r, err := interpolateValue(ec, inner, msg)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, inner := range t {
			r, err := interpolateValue(ec, inner, msg)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}
