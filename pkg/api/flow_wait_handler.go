package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// FlowWaitsInput identifies the flow whose pending waits are listed.
type FlowWaitsInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FlowWaitsOutput is a flow's pending waits.
type FlowWaitsOutput struct {
	Waits []flowops.PendingWait `json:"waits" description:"Pending waits, ordered by deadline."`
}

// FlowWaitsHandler returns the interactor that lists a flow's pending waits.
func FlowWaitsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowWaitsInput, out *apiutil.Success[FlowWaitsOutput]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
		if err != nil {
			return err
		}
		waits, err := flowops.ListWaits(f)
		if err != nil {
			return err
		}
		out.Data = FlowWaitsOutput{Waits: waits}
		return nil
	})
	u.SetTitle("List Pending Waits")
	u.SetDescription("Returns a flow's pending timers, held messages, and partial groups, which survive restarts and redeploys.")
	u.SetTags("Runs")
	return u
}

// FlowWaitCancelInput identifies one pending wait.
type FlowWaitCancelInput struct {
	ID     string `path:"id" format:"uuid" description:"Flow UUID."`
	NodeID string `path:"node_id" description:"Node holding the wait."`
	Key    string `query:"key" required:"true" description:"The wait's key, as the list reports it."`
}

// FlowWaitCancelHandler returns the interactor that cancels one pending wait.
func FlowWaitCancelHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowWaitCancelInput, out *apiutil.Success[any]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}
		username := "an operator"
		if uc := middleware.GetUser(ctx); uc != nil {
			username = uc.Username
		}
		if err := flowops.CancelWait(f, in.NodeID, in.Key, username); err != nil {
			return err
		}
		out.Message = "Wait cancelled"
		return nil
	})
	u.SetTitle("Cancel Pending Wait")
	u.SetDescription("Ends one pending wait without delivering it, and records the cancellation in run history.")
	u.SetTags("Runs")
	return u
}

// RegisterFlowWaitRoutes registers the pending-wait endpoints. They are
// gated like the run endpoints: reading waits is reading runs, and
// cancelling one is cancelling a run's future delivery.
func RegisterFlowWaitRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRunsRead), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/waits",
			nethttp.NewHandler(FlowWaitsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermRunsCancel), bearerDoc).
		Method(http.MethodDelete, "/flows/{id}/waits/{node_id}",
			nethttp.NewHandler(FlowWaitCancelHandler()))
}
