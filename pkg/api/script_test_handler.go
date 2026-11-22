package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// Running a script against a made-up message is how an author finds out what it
// does without deploying a flow and waiting for one. It runs the same sandbox
// the script node runs, with the same limits and the same network policy, so a
// script that works here works there.

// ScriptTestInput is one script and the message to run it against. The message
// is the same shape a script node receives, so what is typed here is what
// arrives in the script.
type ScriptTestInput struct {
	Language string `json:"language" required:"true" description:"Script language: javascript, python, or bash."`
	Source   string `json:"source" required:"true" description:"The script."`
	// The message. Every field is optional: a script that reads only the
	// payload needs nothing else typed.
	Payload any            `json:"payload,omitempty" description:"Message payload."`
	Topic   string         `json:"topic,omitempty" description:"Message topic."`
	Meta    map[string]any `json:"meta,omitempty" description:"Message metadata."`
	Inputs  map[string]any `json:"input,omitempty" description:"Payload per named input port."`
	// The variable scopes as the script would see them. Writes are reported
	// back rather than persisted: a test must not change what a flow reads.
	Vars   map[string]any `json:"vars,omitempty" description:"Run variables."`
	Flow   map[string]any `json:"flow,omitempty" description:"Flow variables."`
	Global map[string]any `json:"global,omitempty" description:"Global variables."`
}

// ScriptTestResult is what one run produced. A script that throws is reported
// here rather than as an error status: it is the thing being looked at.
type ScriptTestResult struct {
	Value  any      `json:"value" description:"What the script returned."`
	Output []string `json:"output" description:"Console lines, in order."`
	Error  string   `json:"error,omitempty" description:"Why it failed, when it did."`
	// The scopes as the script left them, so a script that writes variables can
	// be checked without a flow running and without anything being persisted.
	Vars   map[string]any `json:"vars,omitempty" description:"Run variables as the script left them."`
	Flow   map[string]any `json:"flow,omitempty" description:"Flow variables as the script left them."`
	Global map[string]any `json:"global,omitempty" description:"Global variables as the script left them."`
}

// maxScriptTestBytes bounds a script sent for a test run.
const maxScriptTestBytes = 256 << 10

// ScriptTestHandler returns the interactor that runs one script against a
// made-up message.
func ScriptTestHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ScriptTestInput, out *apiutil.Success[ScriptTestResult]) error {
		if strings.TrimSpace(in.Source) == "" {
			return apperrors.ErrValidation.WithMessage("there is no script to run")
		}
		if len(in.Source) > maxScriptTestBytes {
			return apperrors.ErrValidation.WithMessage("script is too large to run")
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

		// Console output survives a failure, because a script that failed
		// usually said why before it did.
		if result != nil {
			out.Data.Output = result.Output
		}
		if err != nil {
			out.Data.Error = err.Error()
			return nil
		}
		out.Data.Value = result.Value
		out.Data.Vars = result.Vars
		out.Data.Flow = result.Flow
		out.Data.Global = result.Global
		return nil
	})
	u.SetTitle("Test a Script")
	u.SetDescription("Runs a script against a made-up message and returns its output and result.")
	u.SetTags("Flows")
	return u
}

// RegisterScriptRoutes registers the script test endpoint. It is gated on flow
// write: running a script is what editing one is for, and someone who may edit
// a script node can already make the engine run it.
func RegisterScriptRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/scripts/test",
			nethttp.NewHandler(ScriptTestHandler()))
	RegisterScriptVerifyRoute(s, bearerDoc)
}
