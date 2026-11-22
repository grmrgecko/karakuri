package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// WidgetTriggerItem is one named widget trigger a dashboard can press, listed
// with the flow it belongs to so a picker can group by flow.
type WidgetTriggerItem struct {
	FlowID      string `json:"flow_id" description:"Flow the trigger belongs to."`
	FlowName    string `json:"flow_name" description:"Display name of that flow."`
	NodeID      string `json:"node_id" description:"Graph node a press starts the run from."`
	Name        string `json:"name" description:"Name a widget presses the trigger by."`
	AllowPublic bool   `json:"allow_public" description:"Whether anonymous viewers of a public dashboard may press it."`
}

// deployedGraph returns a flow's deployed graph, or nil when it has none or
// the stored blob will not parse.
func deployedGraph(f *models.Flow) *flow.Graph {
	if f.DeployedVersionID == nil {
		return nil
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return nil
	}
	var g flow.Graph
	if err := json.Unmarshal([]byte(version.Graph), &g); err != nil {
		return nil
	}
	return &g
}

// WidgetTriggerListHandler returns the interactor that lists every widget
// trigger a dashboard can press. Only deployed, enabled flows are listed:
// a trigger in a draft has nothing behind it, so offering it would build a
// button that silently does nothing.
func WidgetTriggerListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]WidgetTriggerItem]) error {
		flows, err := models.ListDeployedEnabledFlows()
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Collect each flow's declared triggers from its deployed graph. A flow
		// whose stored graph will not parse is skipped rather than failing the
		// whole list; it cannot be armed either.
		// Only flows the caller may run are offered: a dashboard binding one
		// they cannot would be refused when the dashboard is deployed.
		uid := callerID(ctx)
		items := make([]WidgetTriggerItem, 0)
		for _, f := range flows {
			if !flowops.Holds(ctx, uid, f, models.FlowAccessEdit) {
				continue
			}
			g := deployedGraph(f)
			if g == nil {
				continue
			}
			for _, t := range flow.WidgetTriggers(g) {
				items = append(items, WidgetTriggerItem{
					FlowID:      f.ID.String(),
					FlowName:    f.Name,
					NodeID:      t.NodeID,
					Name:        t.Name,
					AllowPublic: t.AllowPublic,
				})
			}
		}

		// Sort by flow then trigger name so the picker's order is stable.
		sort.Slice(items, func(i, j int) bool {
			if items[i].FlowName != items[j].FlowName {
				return items[i].FlowName < items[j].FlowName
			}
			return items[i].Name < items[j].Name
		})

		out.Data = items
		return nil
	})
	u.SetTitle("List Widget Triggers")
	u.SetDescription("Returns the named widget triggers of the deployed, enabled flows the caller may run, for a dashboard widget's trigger picker.")
	u.SetTags("Flows")
	return u
}

// WidgetTriggerFireInput names the trigger to press and the parameters to
// press it with.
type WidgetTriggerFireInput struct {
	FlowID  string          `path:"flow_id" format:"uuid" description:"Flow UUID."`
	Name    string          `json:"name" required:"true" description:"Widget trigger name declared by the flow."`
	Payload json.RawMessage `json:"payload,omitempty" description:"JSON object of parameters delivered as the trigger's payload."`
}

// WidgetTriggerFireHandler returns the interactor that presses a named widget
// trigger: the run starts on that trigger node against the flow's deployed
// graph, which is the same path an armed trigger takes.
//
// Authorization is per trigger. A trigger marked public may be pressed by
// anyone who can see the dashboard, including anonymous viewers; every other
// trigger needs a signed-in user holding api:flows:trigger.
func WidgetTriggerFireHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in WidgetTriggerFireInput, out *apiutil.Success[RunStarted]) error {
		if err := flowops.RequireEngine(); err != nil {
			return err
		}
		if in.Name == "" {
			return apperrors.ErrValidation.WithMessage("name is required")
		}

		// Decide up front who may hear why a press failed: a caller holding
		// the trigger permission outright, or one whose dashboard grants the
		// named trigger. Everyone else gets the same refusal whether the flow
		// is missing, undeployed, or renamed — otherwise an anonymous caller
		// walking UUIDs can enumerate flows and their trigger names.
		// The trigger permission only reaches flows the caller may run, so it
		// also needs edit on the flow; a dashboard's grant was checked against
		// its deployer when the dashboard was deployed.
		uc := middleware.GetUser(ctx)
		byScope := dashscope.Allows(ctx, dashscope.Capability{
			Kind:  dashscope.CapTrigger,
			Owner: strings.ToLower(in.FlowID),
			Ref:   in.Name,
			Write: true,
		})

		// Resolve the flow and the trigger the press names. A flow that is not
		// deployed and enabled has nothing to press, the same rule that decides
		// whether a webhook is armed. A subflow never presses: it runs only
		// inside a caller, and the list endpoint never offers its triggers.
		f, err := flowops.Load(in.FlowID)
		byPermission := uc != nil && auth.Can(ctx, uc.UserID, perm.PermFlowsTrigger) &&
			err == nil && flowops.Holds(ctx, uc.UserID, f, models.FlowAccessEdit)
		mayDiagnose := byPermission || byScope
		if err != nil {
			if !mayDiagnose {
				return unauthorizedOrForbidden(ctx)
			}
			return err
		}
		if f.IsSubflow {
			if !mayDiagnose {
				return unauthorizedOrForbidden(ctx)
			}
			return apperrors.ErrValidation.WithMessage("a subflow's triggers cannot be fired directly")
		}
		if !f.Enabled || f.DeployedVersionID == nil {
			if !mayDiagnose {
				return unauthorizedOrForbidden(ctx)
			}
			return apperrors.ErrValidation.WithMessage("flow is not deployed and enabled")
		}
		version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		g, err := flowops.ParseGraph(version.Graph)
		if err != nil {
			return err
		}
		var trigger *flow.WidgetTrigger
		for _, t := range flow.WidgetTriggers(g) {
			if t.Name == in.Name {
				found := t
				trigger = &found
				break
			}
		}
		if trigger == nil {
			if !mayDiagnose {
				return unauthorizedOrForbidden(ctx)
			}
			return apperrors.ErrNotFound.WithMessage("flow has no widget trigger named " + in.Name)
		}

		// Check the press is allowed before anything runs: the trigger is
		// marked public, or the caller could already hear diagnostics — which
		// is exactly the permission or dashboard grant that permits pressing.
		if !trigger.AllowPublic && !mayDiagnose {
			return unauthorizedOrForbidden(ctx)
		}

		// Build the seed message. The trigger's output port promises an object,
		// so anything else is refused here rather than delivered as the wrong
		// shape.
		params := map[string]any{}
		if len(in.Payload) > 0 {
			if err := json.Unmarshal(in.Payload, &params); err != nil {
				return apperrors.ErrValidation.WithMessage("payload must be a JSON object")
			}
		}
		// A JSON null decodes into a nil map, which would seed the run with
		// null on a port that promises an object.
		if params == nil {
			params = map[string]any{}
		}
		meta := map[string]any{
			"source":  "widget",
			"trigger": trigger.Name,
		}
		if uc != nil {
			meta["user"] = uc.Username
		}
		// The dashboard header is a claim, but by this point the press was
		// authorized, so it is worth handing to the flow: a trigger pressed
		// from two dashboards can tell them apart.
		if ref := middleware.GetDashboardContext(ctx); ref != "" {
			meta["dashboard"] = ref
		}

		// Start the run on the named trigger node.
		handle, err := flowops.Engine().RunFlow(engine.RunOptions{
			FlowID:        f.ID,
			FlowName:      f.Name,
			FlowSlug:      f.Slug,
			FlowVersionID: version.ID,
			RunAs:         engine.RunAsFor(f, version),
			TriggerNodeID: trigger.NodeID,
			Graph:         g,
			InitialMsg: &engine.Msg{
				ID:      uuid.New().String(),
				Payload: params,
				Meta:    meta,
			},
			ErrorFlowID:  f.ErrorFlowID,
			DurableWaits: true,
		})
		if err == engine.ErrRunLimit {
			return apperrors.ErrRateLimit.WithMessage("flow has too many concurrent runs")
		}
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		out.Data = RunStarted{RunID: handle.ID.String()}
		out.Message = "Trigger fired"
		return nil
	})
	u.SetTitle("Fire Widget Trigger")
	u.SetDescription("Starts a run of the flow's deployed graph on the named widget trigger.")
	u.SetTags("Flows")
	return u
}

// RegisterWidgetTriggerRoutes registers the dashboard-facing trigger endpoints.
// Firing is optionally authenticated so a public dashboard can press a trigger
// that allows it; the handler enforces the rest.
func RegisterWidgetTriggerRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/widget_triggers",
			nethttp.NewHandler(WidgetTriggerListHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodPost, "/widget_triggers/{flow_id}/fire",
			nethttp.NewHandler(WidgetTriggerFireHandler()))
}
