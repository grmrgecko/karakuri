package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// FlowTestInput identifies the flow to test-run and optionally which trigger
// node to start from, or a start node for partial run-from-node runs.
type FlowTestInput struct {
	ID            string `path:"id" format:"uuid" description:"Flow UUID."`
	TriggerNodeID string `json:"trigger_node_id,omitempty" description:"Trigger node to start from; required when the graph has multiple triggers."`
	StartNodeID   string `json:"start_node_id,omitempty" description:"Run only the subgraph reachable from this node, seeding its inputs from the last persisted run's node outputs."`
	// Payload replaces the message the run would otherwise be seeded with, so a
	// flow can be tried against a value without editing the node first. It
	// applies to every trigger kind: a webhook flow can be exercised with a
	// body, a schedule with whatever payload the author wants to see.
	Payload json.RawMessage `json:"payload,omitempty" description:"Overrides the seed message payload for this run."`
	Topic   string          `json:"topic,omitempty" description:"Overrides the seed message topic for this run."`
}

// RunStarted is the response payload of a test-run request.
type RunStarted struct {
	RunID string `json:"run_id" description:"ID of the started run."`
}

// FlowTestHandler returns the interactor that starts a test run of the draft graph.
func FlowTestHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowTestInput, out *apiutil.Success[RunStarted]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Start the run.
		runID, err := flowops.TestRun(f, flowops.TestOptions{
			RunAs:         callerID(ctx),
			TriggerNodeID: in.TriggerNodeID,
			StartNodeID:   in.StartNodeID,
			Payload:       in.Payload,
			Topic:         in.Topic,
		})
		if err != nil {
			return err
		}

		// Build response.
		out.Data = RunStarted{RunID: runID.String()}
		out.Message = "Test run started"
		return nil
	})
	u.SetTitle("Test Run Flow")
	u.SetDescription("Starts a test run of the flow's draft graph, optionally from a specific node seeded by the last run.")
	u.SetTags("Runs")
	return u
}

// FlowRerunInput identifies the flow whose last run is re-fired.
type FlowRerunInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowRerunHandler returns the interactor that re-fires a flow's last
// persisted run: the stored trigger payload is replayed through the stored
// trigger node against the current deployed graph.
func FlowRerunHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowRerunInput, out *apiutil.Success[RunStarted]) error {
		// Load flow from database.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}

		// Replay the last run against the deployed graph.
		runID, err := flowops.Rerun(f, callerID(ctx))
		if err != nil {
			return err
		}

		// Build response.
		out.Data = RunStarted{RunID: runID.String()}
		out.Message = "Re-run started"
		return nil
	})
	u.SetTitle("Re-run Flow")
	u.SetDescription("Re-fires the flow's last persisted run using its stored trigger payload against the current deployed graph.")
	u.SetTags("Runs")
	return u
}

// FlowRunGetInput identifies the flow whose last run is requested.
type FlowRunGetInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowRunGetHandler returns the interactor that retrieves a flow's last run.
func FlowRunGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowRunGetInput, out *apiutil.Success[*flowops.RunDetail]) error {
		// Load flow and its persisted run with derived node states.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return err
		}
		detail, err := flowops.LastRunDetail(f.ID)
		if err != nil {
			return err
		}

		// Build response.
		out.Data = detail
		return nil
	})
	u.SetTitle("Get Last Run")
	u.SetDescription("Returns the flow's last persisted run with its events and per-node latest states.")
	u.SetTags("Runs")
	return u
}

// RunCancelInput identifies an active run to cancel.
type RunCancelInput struct {
	ID string `path:"id" format:"uuid" description:"Run UUID."`
}

// RunCancelHandler returns the interactor that cancels an active run.
func RunCancelHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in RunCancelInput, out *apiutil.Success[any]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid run ID")
		}
		if err := flowops.CancelRunFor(ctx, callerID(ctx), id); err != nil {
			return err
		}
		out.Message = "Run canceled"
		return nil
	})
	u.SetTitle("Cancel Run")
	u.SetDescription("Cancels an active run.")
	u.SetTags("Runs")
	return u
}

// RunsActiveHandler returns the interactor that lists in-memory active runs.
func RunsActiveHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]engine.RunSnapshot]) error {
		runs, err := flowops.ActiveRunsFor(ctx, callerID(ctx))
		if err != nil {
			return err
		}
		out.Data = runs
		return nil
	})
	u.SetTitle("List Active Runs")
	u.SetDescription("Returns snapshots of the runs currently executing in memory for flows the caller may read.")
	u.SetTags("Runs")
	return u
}

// RegisterRunRoutes registers run execution endpoints.
func RegisterRunRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/test",
			nethttp.NewHandler(FlowTestHandler(), nethttp.SuccessStatus(http.StatusAccepted)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsWrite), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/rerun",
			nethttp.NewHandler(FlowRerunHandler(), nethttp.SuccessStatus(http.StatusAccepted)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRunsRead), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/run",
			nethttp.NewHandler(FlowRunGetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRunsCancel), bearerDoc).
		Method(http.MethodPost, "/runs/{id}/cancel",
			nethttp.NewHandler(RunCancelHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRunsRead), bearerDoc).
		Method(http.MethodGet, "/runs/active",
			nethttp.NewHandler(RunsActiveHandler()))
}
