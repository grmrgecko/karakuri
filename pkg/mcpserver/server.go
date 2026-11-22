// Package mcpserver serves karakuri over the Model Context Protocol at /mcp,
// so an AI agent can drive the instance: author and deploy flows, inspect
// runs, manage controls, connections, MQTT devices, dashboards, and plugins,
// and read the embedded guides. The endpoint reuses the REST API's bearer
// authentication and sits behind the api:mcp:use gate; every tool then checks
// the same api:* permission its REST counterpart does, so what an agent may do
// is exactly what its credential's roles grant.
package mcpserver

import (
	"net/http"
	"time"

	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// instructions is handed to every connecting client. Agents write graphs, so
// the one thing worth saying up front is where the graph format is documented.
const instructions = `Karakuri is a reliability-focused home-automation flow engine and
dashboard. Before authoring or editing a flow graph, read the
karakuri://guides/flow-authoring resource: it documents the graph JSON format,
port typing, and the draft/validate/deploy/enable lifecycle. Use
validate_flow_graph to check a graph before saving, and get_flow_run to see
per-node results after a test.`

// server is the process-wide MCP server, built once by Init.
var server *mcp.Server

// Init builds the MCP server and registers every tool and resource. Call it
// after the engine, trigger runner, connection manager, and plugin host are
// wired; it is safe without them, in which case the tools that need one
// report the subsystem as unavailable.
func Init() {
	server = mcp.NewServer(&mcp.Implementation{
		Name:    "karakuri",
		Title:   "Karakuri",
		Version: version.Version,
	}, &mcp.ServerOptions{Instructions: instructions})
	server.AddReceivingMiddleware(connectionArgumentAccess)

	addFlowTools(server)
	addFaceplateTools(server)
	addDocumentTools(server)
	addDependencyTools(server)
	addDiscoveryTools(server)
	addReferenceTools(server)
	addStateTools(server)
	addDashboardValidationTools(server)
	addAgentTools(server)
	addRunTools(server)
	addHomeTools(server)
	addNodeTools(server)
	addCatalogTools(server)
	addControlTools(server)
	addConnectionTools(server)
	addOpenHABTools(server)
	addTimeSeriesTools(server)
	addChartTools(server)
	addNotificationTools(server)
	addDeviceTools(server)
	addNVRTools(server)
	addEnergyTools(server)
	addBackupTools(server)
	addEventTools(server)
	addDashboardTools(server)
	addDashboardLayoutTools(server)
	addRenderTools(server)
	addPluginTools(server)
	addScriptTools(server)
	addResources(server)
}

// Handler returns the HTTP handler for the /mcp endpoint. Stateless mode
// serves each POST on its own request context, which is what lets tool
// handlers read the authenticated user middleware.Auth put there. The write
// deadline is cleared per request because the server's global WriteTimeout is
// sized for REST calls, and a tool call driving a connection test or a
// sandboxed script can outlive it.
func Handler() http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true},
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		streamable.ServeHTTP(w, r)
	})
}
