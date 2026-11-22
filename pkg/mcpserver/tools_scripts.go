package mcpserver

import (
	"context"
	"strings"
	"time"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxScriptTestBytes bounds a script sent for a test run, matching the REST
// endpoint's cap.
const maxScriptTestBytes = 256 << 10

type testScriptInput struct {
	Language string `json:"language" jsonschema:"Script language: javascript, python, or bash."`
	Source   string `json:"source" jsonschema:"The script."`
	// The message. Every field is optional: a script that reads only the
	// payload needs nothing else supplied.
	Payload any            `json:"payload,omitempty" jsonschema:"Message payload."`
	Topic   string         `json:"topic,omitempty" jsonschema:"Message topic."`
	Meta    map[string]any `json:"meta,omitempty" jsonschema:"Message metadata."`
	Inputs  map[string]any `json:"input,omitempty" jsonschema:"Payload per named input port."`
	Vars    map[string]any `json:"vars,omitempty" jsonschema:"Run variables."`
	Flow    map[string]any `json:"flow,omitempty" jsonschema:"Flow variables."`
	Global  map[string]any `json:"global,omitempty" jsonschema:"Global variables."`
}

// addScriptTools registers the sandbox script test tool.
func addScriptTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_script",
		Description: "Run a script against a made-up message in the real sandbox, with the server's own limits and network policy. Variable writes come back in the result rather than being persisted; a script that throws is a result, not an error.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testScriptInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(in.Source) == "" {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("there is no script to run"))
		}
		if len(in.Source) > maxScriptTestBytes {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("script is too large to run"))
		}

		// The server's own limits and network policy, so a test is not more or
		// less capable than the node it is standing in for.
		limits, policy := sandbox.Defaults()
		runCtx, cancel := context.WithTimeout(ctx, limits.Timeout+5*time.Second)
		defer cancel()

		result, err := sandbox.Run(runCtx, sandbox.Request{
			Language: in.Language,
			Source:   in.Source,
			Payload:  in.Payload,
			Topic:    in.Topic,
			Meta:     in.Meta,
			Inputs:   in.Inputs,
			Vars:     in.Vars,
			Flow:     in.Flow,
			Global:   in.Global,
			Limits:   limits,
			Network:  policy,
		})

		out := map[string]any{}
		// Console output survives a failure, because a script that failed
		// usually said why before it did.
		if result != nil {
			out["output"] = result.Output
		}
		if err != nil {
			out["error"] = err.Error()
			return nil, out, nil
		}
		out["value"] = result.Value
		out["vars"] = result.Vars
		out["flow"] = result.Flow
		out["global"] = result.Global
		return nil, out, nil
	})
}
