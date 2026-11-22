package flowops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/grmrgecko/karakuri/pkg/scriptverify"
)

// validateScripts applies strict static script verification without executing a
// node. Draft validation deliberately does not call this helper.
func validateScripts(graph *flow.Graph) error {
	findings, err := scriptverify.CheckGraph(context.Background(), graph, engine.Default().List())
	if err != nil {
		if errors.Is(err, sandbox.ErrPythonVerificationUnavailable) {
			return apperrors.ErrServiceUnavailable.WithErrorMessage(err)
		}
		return apperrors.ErrInternal.WithErrorMessage(err)
	}
	if len(findings) == 0 {
		return nil
	}
	details := make(map[string]string, len(findings))
	count := 0
	for location, diagnostics := range findings {
		messages := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			messages = append(messages, fmt.Sprintf("[%s] line %d, column %d: %s",
				diagnostic.Code, diagnostic.Line, diagnostic.Column, diagnostic.Message))
		}
		details[location] = strings.Join(messages, "; ")
		count += len(diagnostics)
	}
	return apperrors.ErrValidation.
		WithMessage(fmt.Sprintf("script validation failed: %d problem(s)", count)).
		WithDetails(details)
}
