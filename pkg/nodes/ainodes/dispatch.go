package ainodes

import (
	"context"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/integrations/mcpconn"
)

// defaultBranchTimeout bounds a graph-backed tool call whose node declared no
// timeout; the engine applies the same floor as defense.
const defaultBranchTimeout = 60 * time.Second

// mcpConn is the slice of the MCP driver the nodes need.
type mcpConn interface {
	Tools(ctx context.Context) ([]mcpconn.Tool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (*mcpconn.ToolResult, error)
}

var _ mcpConn = (*mcpconn.Driver)(nil)

// resolveMCPConn resolves a connection ID to an MCP server driver.
func resolveMCPConn(ec engine.ExecContext, id string) (mcpConn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(mcpConn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an MCP server connection", id)
	}
	return c, nil
}

// dispatch executes one tool call by its invoke kind, returning the observation
// value and any images the tool produced. The error carries what went wrong in
// words a model can act on; the agent turns it into an observation, and
// mcp-call turns it into a node failure.
func dispatch(ctx context.Context, ec engine.ExecContext, inv invokeSpec, args map[string]any) (any, []ai.Image, error) {
	switch inv.Kind {
	case invokeMCP:
		conn, err := resolveMCPConn(ec, inv.Connection)
		if err != nil {
			return nil, nil, err
		}
		res, err := conn.CallTool(ctx, inv.Tool, args)
		if err != nil {
			return nil, nil, err
		}
		if res.IsError {
			return nil, nil, fmt.Errorf("%s", stringifyResult(res.Value))
		}
		return res.Value, toAIImages(res.Images), nil

	case invokeRemember, invokeRecall:
		value, err := dispatchMemory(ctx, inv, args)
		return value, nil, err

	case invokeBranch:
		timeout := time.Duration(inv.TimeoutSec * float64(time.Second))
		if timeout <= 0 {
			timeout = defaultBranchTimeout
		}
		msg, err := ec.CallTool(ctx, inv.Node, args, timeout)
		if err != nil {
			return nil, nil, err
		}
		// A branch that returns bytes returns them for the model to see: a
		// tool wrapping the screenshot node is a vision tool. Text-encoding
		// them instead would hand the model a wall of base64.
		if img, isBinary, err := payloadImage(msg); isBinary {
			if err != nil {
				return nil, nil, fmt.Errorf("tool returned binary the model cannot read: %w", err)
			}
			return "", []ai.Image{img}, nil
		}
		return msg.Payload, nil, nil

	default:
		return nil, nil, fmt.Errorf("tool has unknown invoke kind %q", inv.Kind)
	}
}

// toAIImages converts MCP image blocks into the chat vocabulary's image parts.
func toAIImages(imgs []mcpconn.Image) []ai.Image {
	if len(imgs) == 0 {
		return nil
	}
	out := make([]ai.Image, 0, len(imgs))
	for _, im := range imgs {
		out = append(out, ai.Image{MIME: im.MIME, Data: im.Data})
	}
	return out
}
