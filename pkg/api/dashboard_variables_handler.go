package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/dashlive"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// DashboardVariablesInput identifies a dashboard and the view whose variables
// to resolve.
type DashboardVariablesInput struct {
	ID   string `path:"id" description:"Dashboard UUID or slug."`
	View string `query:"view" enum:"draft,deployed" description:"draft returns the editable draft's variables to users who may edit it."`
	// Vars carries the viewer's values, so a query variable that refers to
	// an earlier one resolves for the value this viewer picked.
	Vars string `query:"vars" description:"Viewer variable values as a JSON object, for queries that refer to earlier variables."`
}

// DashboardVariablesData is a view's variables with every option resolved.
type DashboardVariablesData struct {
	Variables []models.DashboardVariable `json:"variables" description:"Variables with options set for every type."`
	// Followed holds the value each followed select entity's state maps to.
	Followed map[string]string `json:"followed" description:"Dashboard-wide values of variables that follow a select entity, by name."`
	// FollowMap maps each followed entity's possible states to option values.
	FollowMap map[string]map[string]string `json:"follow_map" description:"For each followed variable, the option value each entity state maps to."`
}

// DashboardVariablesHandler returns the interactor that resolves a dashboard's
// variables. A query's options are what scope grants from, so viewers are
// served the same list the server decides with.
func DashboardVariablesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardVariablesInput, out *apiutil.Success[DashboardVariablesData]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil || !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		rc := middleware.GetRenderContext(ctx)
		draftRender := rc != nil && rc.DashboardID == dashboard.ID && rc.Draft
		draft := in.View == "draft" && (canEditDashboard(ctx, dashboard) || draftRender)
		served, err := dashops.ServedVariables(dashboard, draft)
		if err != nil {
			return err
		}
		var resolved []models.DashboardVariable
		var followed map[string]string
		var mappings map[string]map[string]string
		if in.Vars != "" {
			var values map[string]string
			if err := json.Unmarshal([]byte(in.Vars), &values); err != nil {
				return apperrors.ErrValidation.WithMessage("vars must be a JSON object of variable values")
			}
			resolved, followed, mappings = dashvars.ResolveFollowingFor(served, values)
		} else {
			resolved, followed, mappings = dashvars.ResolveFollowing(served)
		}
		if resolved == nil {
			resolved = []models.DashboardVariable{}
		}
		if followed == nil {
			followed = map[string]string{}
		}
		if mappings == nil {
			mappings = map[string]map[string]string{}
		}
		out.Data = DashboardVariablesData{Variables: resolved, Followed: followed, FollowMap: mappings}
		return nil
	})
	u.SetTitle("Resolve Dashboard Variables")
	u.SetDescription("Returns a dashboard's variables with the options of constants and Home Assistant queries resolved, and the values of variables that follow a select entity.")
	u.SetTags("Dashboards")
	return u
}

// DashboardVariablePreviewInput carries a variable being edited.
type DashboardVariablePreviewInput struct {
	ID       string                   `path:"id" description:"Dashboard UUID or slug."`
	Variable models.DashboardVariable `json:"variable" required:"true" description:"Variable definition to resolve, unsaved."`
}

// DashboardVariablePreviewData is what a variable would resolve to.
type DashboardVariablePreviewData struct {
	Options   []models.VariableOption `json:"options" description:"Options the variable takes."`
	Truncated bool                    `json:"truncated" description:"Whether a query returned more options than are kept."`
	Followed  string                  `json:"followed,omitempty" description:"Value a followed select entity's state maps to."`
	Error     string                  `json:"error,omitempty" description:"Why a query returned nothing."`
}

// DashboardVariablePreviewHandler returns the interactor that resolves an
// unsaved variable for the editor's query preview. It takes edit
// access because it reads Home Assistant beyond what the layout grants, and
// connection read for a time-series query, whose text runs on the database.
func DashboardVariablePreviewHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardVariablePreviewInput, out *apiutil.Success[DashboardVariablePreviewData]) error {
		if _, err := editableFor(ctx, in.ID); err != nil {
			return err
		}
		if err := dashops.ValidateVariables([]models.DashboardVariable{in.Variable}); err != nil {
			return err
		}
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}
		next := &models.DashboardDisplay{Variables: []models.DashboardVariable{in.Variable}}
		if err := dashops.CheckVariableQueries(ctx, uc.UserID, nil, next); err != nil {
			return err
		}
		data := DashboardVariablePreviewData{Options: []models.VariableOption{}}
		if in.Variable.Kind() == models.VariableTypeQuery {
			r := dashvars.Query(ctx, in.Variable.Query)
			if r.Err != nil {
				data.Error = r.Err.Error()
			} else {
				data.Options = r.Options
				data.Truncated = r.Truncated
				data.Followed = r.Followed
			}
		} else {
			resolved, _ := dashvars.Resolve([]models.DashboardVariable{in.Variable})
			data.Options = resolved[0].Options
		}
		out.Data = data
		return nil
	})
	u.SetTitle("Preview Dashboard Variable")
	u.SetDescription("Resolves an unsaved variable definition and returns its options.")
	u.SetTags("Dashboards")
	return u
}

// DashboardVariableSetInput names a variable and the value to give it on every
// screen.
type DashboardVariableSetInput struct {
	ID    string `path:"id" description:"Dashboard UUID or slug."`
	Name  string `path:"name" description:"Variable name."`
	Value string `json:"value" description:"Option value or label; empty returns to the default."`
}

// DashboardVariableSetHandler returns the interactor that sets a variable's
// dashboard-wide value from a viewer, such as a Variable buttons widget set to
// every screen. An editor may always; anyone else needs a placement granting
// that variable and the dashboard's control policy, the same rule as pressing
// a switch, because the change reaches every screen.
func DashboardVariableSetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in DashboardVariableSetInput, out *apiutil.Success[*dashlive.Result]) error {
		dashboard, err := resolveDashboard(ctx, in.ID)
		if err != nil || !canReadDashboard(ctx, dashboard) {
			return apperrors.ErrNotFound.WithMessage("dashboard not found")
		}
		if !canEditDashboard(ctx, dashboard) {
			scope := dashscope.FromContext(ctx)
			if scope == nil || scope.DashboardID != dashboard.ID.String() ||
				!scope.Allows(dashscope.Capability{Kind: dashscope.CapVariable, Ref: in.Name, Write: true}) {
				return apperrors.ErrForbidden.WithMessage("this dashboard does not let you set that variable for every screen")
			}
		}
		raw, err := json.Marshal(in.Value)
		if err != nil {
			return err
		}
		res, err := dashlive.S.Apply(dashboard.ID, dashlive.Command{Action: dashlive.ActionVariable, Variable: in.Name, Value: raw})
		if err != nil {
			return err
		}
		out.Data = res
		return nil
	})
	u.SetTitle("Set Dashboard Variable")
	u.SetDescription("Sets a variable's dashboard-wide value. Editors may always; other viewers need a placement that grants the variable and the dashboard's control policy.")
	u.SetTags("Dashboards")
	return u
}

// registerDashboardVariableRoutes registers variable resolution, preview, and
// setting.
func registerDashboardVariableRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/dashboards/{id}/variables",
			nethttp.NewHandler(DashboardVariablesHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodPost, "/dashboards/{id}/variables/{name}/value",
			nethttp.NewHandler(DashboardVariableSetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermDashboardsWrite), bearerDoc).
		Method(http.MethodPost, "/dashboards/{id}/variables/preview",
			nethttp.NewHandler(DashboardVariablePreviewHandler()))
}
