package api

import (
	"context"
	"fmt"
	"github.com/grmrgecko/karakuri/pkg/access"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// NodeTypeListHandler returns the interactor that lists all registered node types.
func NodeTypeListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]engine.NodeMeta]) error {
		// Build response from the registry, sorted by type name.
		out.Data = access.VisibleNodeTypes(ctx, callerID(ctx), engine.Default().List())
		return nil
	})
	u.SetTitle("List Node Types")
	u.SetDescription("Returns metadata for the registered node types the caller may use; an agent's node types are listed only to users the agent is shared with.")
	u.SetTags("NodeTypes")
	return u
}

// ScriptNodeFunctionListHandler returns the derived synchronous function
// catalog without executing nodes or resolving integrations.
func ScriptNodeFunctionListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[scriptsdk.Catalog]) error {
		catalog := scriptsdk.NewCatalog(access.VisibleNodeTypes(ctx, callerID(ctx), engine.Default().List()))
		if catalog.Revision == "" {
			return fmt.Errorf("script node function metadata is not JSON serializable")
		}
		out.Data = catalog
		return nil
	})
	u.SetTitle("List Script Node Functions")
	u.SetDescription("Returns the versioned catalog of nodes callable from server flow scripts.")
	u.SetTags("NodeTypes")
	return u
}

// RegisterNodeTypeRoutes registers the node type and script function catalog endpoints.
func RegisterNodeTypeRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/nodetypes",
			nethttp.NewHandler(NodeTypeListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermFlowsRead), bearerDoc).
		Method(http.MethodGet, "/scripts/node_functions",
			nethttp.NewHandler(ScriptNodeFunctionListHandler()))
}
