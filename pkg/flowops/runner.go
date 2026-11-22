package flowops

import (
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// eng is the package-level execution engine wired by the server at startup.
// Operations that need it fail with SERVICE_UNAVAILABLE when it is unset.
var eng *engine.Engine

// SetEngine installs the execution engine used by run operations.
func SetEngine(e *engine.Engine) { eng = e }

// Engine returns the installed execution engine, or nil before startup.
func Engine() *engine.Engine { return eng }

// RequireEngine returns an error when the execution engine is not available.
func RequireEngine() error {
	if eng == nil {
		return apperrors.ErrServiceUnavailable.WithMessage("execution engine not available")
	}
	return nil
}

// TriggerRunner arms and disarms deployed-flow triggers. Implemented by
// engine.Runner; wired by the server at startup.
type TriggerRunner interface {
	Rearm(flowID uuid.UUID)
	Disarm(flowID uuid.UUID)
}

// triggerRunner is the package-level trigger runner. Optional; nil in tests
// that do not exercise arming.
var triggerRunner TriggerRunner

// SetTriggerRunner installs the trigger runner used by flow and connection
// operations.
func SetTriggerRunner(r TriggerRunner) { triggerRunner = r }

// Rearm rearms one flow's triggers when a runner is installed.
func Rearm(flowID uuid.UUID) {
	if triggerRunner != nil {
		triggerRunner.Rearm(flowID)
	}
}

// Disarm disarms one flow's triggers when a runner is installed.
func Disarm(flowID uuid.UUID) {
	if triggerRunner != nil {
		triggerRunner.Disarm(flowID)
	}
}

// RearmDependents rearms every flow whose graph references the connection, so
// triggers pick up a restarted or replaced driver.
func RearmDependents(connID uuid.UUID) {
	if triggerRunner == nil {
		return
	}
	flowIDs, err := models.ListFlowsDependingOnConnection(connID)
	if err != nil {
		return
	}
	for _, id := range flowIDs {
		triggerRunner.Rearm(id)
	}
}
