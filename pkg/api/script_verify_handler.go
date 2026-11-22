package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
	"github.com/grmrgecko/karakuri/pkg/scriptverify"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// ScriptVerifyInput selects one source document and whether it can be pulled as
// a pure value. Verification never executes it.
type ScriptVerifyInput struct {
	Language string `json:"language" description:"Script language: javascript, python, or bash. Empty selects JavaScript."`
	Source   string `json:"source" required:"true" description:"Source to parse and verify without execution."`
	Pure     bool   `json:"pure,omitempty" description:"Whether the script can be evaluated as a pure value producer."`
}

// ScriptVerifyResult contains localized findings rather than executing source.
type ScriptVerifyResult struct {
	Diagnostics []scriptsdk.Diagnostic `json:"diagnostics" description:"Ordered syntax and literal node-call diagnostics."`
}

// ScriptVerifyHandler parses the selected language and checks literal node
// calls against the active catalog. Dynamic expressions are left for runtime validation.
func ScriptVerifyHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ScriptVerifyInput, out *apiutil.Success[ScriptVerifyResult]) error {
		language := strings.ToLower(strings.TrimSpace(in.Language))
		switch language {
		case "", sandbox.LangJavaScript, sandbox.LangPython, sandbox.LangBash:
		default:
			return apperrors.ErrValidation.WithMessage("unsupported script language; use javascript, python, or bash")
		}
		if len(in.Source) > maxScriptTestBytes {
			return apperrors.ErrValidation.WithMessage("script is too large to verify")
		}
		diagnostics, err := scriptverify.CheckSource(ctx, language, in.Source, in.Pure, engine.Default().List())
		if err != nil {
			if errors.Is(err, sandbox.ErrPythonVerificationUnavailable) {
				return apperrors.ErrServiceUnavailable.WithErrorMessage(err)
			}
			return apperrors.ErrInternal.WithErrorMessage(err)
		}
		if diagnostics == nil {
			diagnostics = []scriptsdk.Diagnostic{}
		}
		out.Data.Diagnostics = diagnostics
		return nil
	})
	u.SetTitle("Verify a Script")
	u.SetDescription("Parses JavaScript, Python, or Bash and checks literal node calls without executing source, nodes, or integrations.")
	u.SetTags("Flows")
	return u
}

// RegisterScriptVerifyRoute adds static verification beside script testing.
func RegisterScriptVerifyRoute(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/scripts/verify",
			nethttp.NewHandler(ScriptVerifyHandler()))
}
