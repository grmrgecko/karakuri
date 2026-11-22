package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/mcpserver"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bearerTransport injects a bearer token into every request the MCP client
// makes, standing in for the header an agent's MCP configuration carries.
type bearerTransport struct {
	token string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(req)
}

// setupMCPServer boots the real /mcp endpoint over a real SQLite database with
// the production middleware chain: bearer auth, the api:mcp:use gate, and the
// stateless streamable handler. It returns the test server, an admin token,
// and a token for a user who holds the gate but no other permission, which is
// what separates the transport check from the per-tool checks.
func setupMCPServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()

	require.NoError(t, config.Load(), "the middleware needs a config for its JWT settings")
	require.NoError(t, db.Init(db.Options{
		Type:         "sqlite",
		DSN:          memdb.TestDB(t),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}), "the tests need a database")
	require.NoError(t, migrate.AutoMigrate(), "the schema comes from the real migrations")
	require.NoError(t, migrate.SeedDefaults(), "the default roles and groups are what the permission gates check")

	permissions.RegisterDefaults()
	core.RegisterAll(engine.Default())

	// An admin with a real session token: MCP requests authenticate exactly
	// like the browser's.
	hash, err := auth.HashPassword("password")
	require.NoError(t, err, "the fixture users need a password hash")
	admin := &models.User{ID: uuid.New(), Username: "mcp-admin", Email: "mcp-admin@example.com", PasswordHash: hash}
	require.NoError(t, db.DB.Create(admin).Error, "the admin fixture must be stored")
	var adminGroup models.UserGroup
	require.NoError(t, db.DB.Where("name = ?", "Admin").First(&adminGroup).Error,
		"seeding must have created the admin group")
	require.NoError(t, db.DB.Create(&models.UserGroupMember{GroupID: adminGroup.ID, UserID: admin.ID}).Error,
		"permissions are group-first, so the admin needs the membership")
	adminTokens, err := auth.CreateSession(admin, "127.0.0.1", "test")
	require.NoError(t, err, "the admin requests authenticate with a real session token")

	// A user granted only the endpoint gate: they get past the transport and
	// then every tool must refuse them, which is what proves each tool checks
	// its own permission on the request's context.
	gated := &models.User{ID: uuid.New(), Username: "mcp-gated", Email: "mcp-gated@example.com", PasswordHash: hash}
	require.NoError(t, db.DB.Create(gated).Error, "the gated fixture must be stored")
	role := &models.Role{Name: "mcp-gate-only", Description: "test role holding only the MCP gate"}
	require.NoError(t, db.DB.Create(role).Error, "the gate-only role must be stored")
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: permissions.PermMCPUse}).Error,
		"the role carries exactly the endpoint gate")
	require.NoError(t, db.DB.Create(&models.RoleAssignment{RoleID: role.ID, UserID: &gated.ID}).Error,
		"the role must reach the user")
	gatedTokens, err := auth.CreateSession(gated, "127.0.0.1", "test")
	require.NoError(t, err, "the gated user's requests authenticate with a real session token")

	// The production mount: Auth, the gate, then the stateless handler.
	mcpserver.Init()
	r := chi.NewRouter()
	r.Group(func(mr chi.Router) {
		mr.Use(middleware.Auth())
		mr.Use(middleware.RequirePermission(permissions.PermMCPUse))
		mr.Handle("/mcp", mcpserver.Handler())
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return srv, adminTokens.AccessToken, gatedTokens.AccessToken
}

// connectMCP opens an MCP client session against the test server with the
// given bearer token.
func connectMCP(t *testing.T, srv *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "0.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp",
		HTTPClient: &http.Client{
			Transport: bearerTransport{token: token},
		},
		// Stateless servers answer GET with 405, so the standalone
		// notification stream has nothing to offer.
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err, "the MCP handshake must complete over the authenticated transport")
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool invokes one tool and decodes its structured JSON content.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoErrorf(t, err, "calling %s must not fail at the protocol level", name)
	decoded := map[string]any{}
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(text.Text), &decoded)
		}
	}
	return res, decoded
}

// validGraph is the manual -> delay -> debug chain the API tests deploy, in
// the wire shape an agent would send.
func validGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "n1", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "n2", "type": "delay", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{"duration": "5s"}},
			{"id": "n3", "type": "debug", "position": map[string]any{"x": 400, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "n1", "source_handle": "out", "target": "n2", "target_handle": "in"},
			{"id": "e2", "source": "n2", "source_handle": "out", "target": "n3", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// TestMCPFlowLifecycle drives the flow loop an agent actually uses over the
// real endpoint: list the tools, create a flow, save a graph, validate it
// strictly, deploy, enable, and read it back. It runs over the production
// middleware chain and the stateless streamable transport, so it also proves
// the request context (and with it the authenticated user) reaches the tool
// handlers.
func TestMCPFlowLifecycle(t *testing.T) {
	srv, adminToken, _ := setupMCPServer(t)
	session := connectMCP(t, srv, adminToken)

	// The catalog names every registered tool; spot-check the groups.
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err, "tools/list is the first thing every client calls")
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"create_flow", "save_flow_graph", "validate_flow_graph", "deploy_flow",
		"run_flow_test", "get_flow_run", "list_node_types", "get_node_type",
		"list_controls", "set_control", "list_connections", "save_connection",
		"list_mqtt_devices", "list_dashboards", "add_dashboard_widget",
		"list_plugins", "test_script", "test_node", "render_dashboard",
	} {
		assert.Truef(t, names[want], "the %s tool must be registered", want)
	}

	assert.NotContains(t, names, "inspect_dashboard_render", "render diagnostics belong to the single render_dashboard tool")

	// Create, save, validate, deploy, enable.
	res, created := callTool(t, session, "create_flow", map[string]any{"name": "MCP Lifecycle"})
	require.False(t, res.IsError, "creating a flow must succeed for an admin")
	flowID, _ := created["id"].(string)
	require.NotEmpty(t, flowID, "the created flow's id addresses every call below")

	res, _ = callTool(t, session, "save_flow_graph", map[string]any{"id": flowID, "graph": validGraph()})
	require.False(t, res.IsError, "saving a valid draft graph must succeed")

	res, validated := callTool(t, session, "validate_flow_graph", map[string]any{
		"graph": validGraph(), "flow_id": flowID, "strict": true,
	})
	require.False(t, res.IsError, "validation of a deployable graph must not error")
	assert.Equal(t, true, validated["valid"], "the graph deploys, so strict validation must pass")

	res, deployed := callTool(t, session, "deploy_flow", map[string]any{"id": flowID})
	require.False(t, res.IsError, "deploying the validated draft must succeed")
	assert.Equal(t, float64(1), deployed["version"], "the first deploy freezes version 1")

	res, _ = callTool(t, session, "set_flow_enabled", map[string]any{"id": flowID, "enabled": true})
	require.False(t, res.IsError, "a deployed flow must be enableable")

	// The stored rows agree with what the tools reported.
	f, err := models.FindFlowByID(uuid.MustParse(flowID))
	require.NoError(t, err, "the flow the tools built must exist in the database")
	assert.True(t, f.Enabled, "enabling over MCP must persist")
	assert.NotNil(t, f.DeployedVersionID, "deploying over MCP must freeze a version")

	// An invalid graph is a result, not a protocol error.
	broken := validGraph()
	broken["edges"] = []map[string]any{
		{"id": "e1", "source": "n1", "source_handle": "out", "target": "missing", "target_handle": "in"},
	}
	res, validated = callTool(t, session, "validate_flow_graph", map[string]any{"graph": broken, "strict": true})
	require.False(t, res.IsError, "an invalid graph is what validate exists to report, not an error")
	assert.Equal(t, false, validated["valid"], "an edge into a missing node must fail validation")
}

// TestMCPResources covers the guide resources: an agent is told to read the
// flow-authoring guide before writing a graph, so it has to actually resolve
// to the embedded markdown.
func TestMCPResources(t *testing.T) {
	srv, adminToken, _ := setupMCPServer(t)
	session := connectMCP(t, srv, adminToken)

	list, err := session.ListResources(context.Background(), nil)
	require.NoError(t, err, "resources/list must answer")
	uris := map[string]bool{}
	for _, r := range list.Resources {
		uris[r.URI] = true
	}
	require.True(t, uris["karakuri://guides/flow-authoring"], "the flow-authoring guide must be listed")

	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "karakuri://guides/flow-authoring",
	})
	require.NoError(t, err, "the guide must be readable")
	require.Len(t, read.Contents, 1, "one document comes back")
	assert.Equal(t, "text/markdown", read.Contents[0].MIMEType, "agents read markdown source, not rendered HTML")
	assert.Contains(t, read.Contents[0].Text, "## The graph document",
		"the content is the embedded guide, not a placeholder")
}

// TestMCPAuthorization pins the two-layer model: the api:mcp:use gate decides
// who reaches the endpoint at all, and each tool checks its own permission on
// the request's user. A missing token is refused before any JSON-RPC, a user
// holding only the gate connects but may call nothing, and the refusal names
// the permission so an agent can report what to ask the administrator for.
func TestMCPAuthorization(t *testing.T) {
	srv, _, gatedToken := setupMCPServer(t)

	// No token: refused at the transport.
	resp, err := http.Post(srv.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	require.NoError(t, err, "the request itself must reach the server")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"an unauthenticated POST must be refused before any JSON-RPC is read")

	// The gate alone admits the handshake and the tool listing.
	session := connectMCP(t, srv, gatedToken)
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err, "listing tools needs no per-tool permission")
	require.NotEmpty(t, tools.Tools, "the catalog is served to anyone past the gate")

	// Every tool then checks its own permission against the calling user.
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_flows", Arguments: map[string]any{},
	})
	require.NoError(t, err, "a permission refusal is a tool error, not a protocol failure")
	require.True(t, res.IsError, "a user without api:flows:read must not list flows")
	text := ""
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	assert.Contains(t, text, "api:flows:read",
		"the refusal names the missing permission so the agent can say what to grant")
}

// TestMCPDeleteControlReportsAnUnknownControl locks delete_control to the
// answer the REST route gives. The tool reported deleted for any well-formed
// UUID, so an agent that mistyped an ID was told the control was gone while
// it stayed in place.
func TestMCPDeleteControlReportsAnUnknownControl(t *testing.T) {
	srv, adminToken, _ := setupMCPServer(t)
	require.NoError(t, controls.S.Load(), "the control store must be loaded from the test database")
	session := connectMCP(t, srv, adminToken)

	res, created := callTool(t, session, "save_control", map[string]any{"name": "Porch", "kind": "boolean"})
	require.Falsef(t, res.IsError, "creating a control must succeed: %v", res.Content)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id, "the created control must report its ID")

	res, _ = callTool(t, session, "delete_control", map[string]any{"id": uuid.NewString()})
	assert.True(t, res.IsError, "a control that does not exist cannot be reported deleted")
	_, ok := controls.S.Get(uuid.MustParse(id))
	assert.True(t, ok, "the existing control is untouched by a delete that named another ID")

	res, deleted := callTool(t, session, "delete_control", map[string]any{"id": id})
	require.Falsef(t, res.IsError, "deleting an existing control must succeed: %v", res.Content)
	assert.Equal(t, true, deleted["deleted"], "the tool confirms the delete")
	_, ok = controls.S.Get(uuid.MustParse(id))
	assert.False(t, ok, "the control is gone from the store")
}

// TestMCPDeleteMQTTDeviceReportsAnUnknownDevice locks delete_mqtt_device to
// the answer delete_control gives. The tool reported deleted for any
// well-formed UUID, so an agent that mistyped an ID was told the device was
// gone while it stayed in place.
func TestMCPDeleteMQTTDeviceReportsAnUnknownDevice(t *testing.T) {
	srv, adminToken, _ := setupMCPServer(t)
	session := connectMCP(t, srv, adminToken)
	device := &models.MQTTDevice{ID: uuid.New(), Name: "Porch light", ConnectionID: uuid.New()}
	require.NoError(t, db.DB.Create(device).Error)

	res, out := callTool(t, session, "delete_mqtt_device", map[string]any{"id": uuid.NewString()})
	assert.Truef(t, res.IsError, "a device that does not exist cannot be reported deleted: %v", out)
	_, err := models.FindMQTTDeviceByID(device.ID)
	assert.NoError(t, err, "the existing device is untouched by a delete that named another ID")

	res, out = callTool(t, session, "delete_mqtt_device", map[string]any{"id": device.ID.String()})
	require.Falsef(t, res.IsError, "deleting an existing device must succeed: %v", res.Content)
	assert.Equal(t, true, out["deleted"], "the tool confirms the delete")
	_, err = models.FindMQTTDeviceByID(device.ID)
	assert.Error(t, err, "the device is gone from the database")
}

// TestMCPDashboardReadsRedactTheOwner locks get_dashboard, list_dashboards,
// update_dashboard, and restore_dashboard_version to what the REST routes
// show of a dashboard's owner. A caller who is shared a dashboard but holds no
// api:dashboards:read was handed the owner's email address and UI preferences
// over MCP, which the REST routes strip. The owner and a holder of the read
// permission still see the account.
func TestMCPDashboardReadsRedactTheOwner(t *testing.T) {
	srv, adminToken, gatedToken := setupMCPServer(t)
	admin := connectMCP(t, srv, adminToken)
	gated := connectMCP(t, srv, gatedToken)

	res, created := callTool(t, admin, "create_dashboard", map[string]any{"name": "Lights"})
	require.Falsef(t, res.IsError, "creating a dashboard must succeed: %v", res.Content)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id, "the created dashboard must report its ID")
	var viewer models.User
	require.NoError(t, db.DB.Where("username = ?", "mcp-gated").First(&viewer).Error, "the gated fixture must exist")
	require.NoError(t, db.DB.Create(&models.DashboardAccess{
		DashboardID: uuid.MustParse(id), UserID: &viewer.ID, Permission: models.DashboardAccessView,
	}).Error, "the dashboard must be shared with the gated user")

	ownerOf := func(dashboard map[string]any) map[string]any {
		owner, _ := dashboard["owner"].(map[string]any)
		require.NotNil(t, owner, "a dashboard names its owner")
		return owner
	}

	res, got := callTool(t, gated, "get_dashboard", map[string]any{"id": id})
	require.Falsef(t, res.IsError, "a shared dashboard must be readable: %v", res.Content)
	owner := ownerOf(got)
	assert.Equal(t, "mcp-admin", owner["username"], "the owner is still named")
	assert.Empty(t, owner["email"], "a viewer without dashboard read is not shown the owner's email address")

	res, listed := callTool(t, gated, "list_dashboards", map[string]any{})
	require.Falsef(t, res.IsError, "the accessible list must be readable: %v", res.Content)
	rows, _ := listed["dashboards"].([]any)
	require.Len(t, rows, 1, "the shared dashboard is the one the gated user may access")
	assert.Empty(t, ownerOf(rows[0].(map[string]any))["email"], "the list redacts the owner the same way")

	res, got = callTool(t, admin, "get_dashboard", map[string]any{"id": id})
	require.Falsef(t, res.IsError, "the owner must be able to read the dashboard: %v", res.Content)
	assert.Equal(t, "mcp-admin@example.com", ownerOf(got)["email"], "the owner sees their own account")

	// update_dashboard answers with the dashboard too, so an edit share must
	// get the same redaction from a write as from a read.
	require.NoError(t, db.DB.Model(&models.DashboardAccess{}).Where("dashboard_id = ? AND user_id = ?", id, viewer.ID).
		Update("permission", models.DashboardAccessEdit).Error, "the share must be raised to edit")
	res, got = callTool(t, gated, "update_dashboard", map[string]any{"id": id, "description": "hall"})
	require.Falsef(t, res.IsError, "an edit share must be able to update: %v", res.Content)
	assert.Empty(t, ownerOf(got)["email"], "an update's response redacts the owner as a read does")
	res, _ = callTool(t, admin, "deploy_dashboard", map[string]any{"id": id})
	require.Falsef(t, res.IsError, "the dashboard must deploy so there is a version to restore: %v", res.Content)
	res, got = callTool(t, gated, "restore_dashboard_version", map[string]any{"id": id})
	require.Falsef(t, res.IsError, "an edit share must be able to restore the draft: %v", res.Content)
	assert.Empty(t, ownerOf(got)["email"], "a restore's response redacts the owner as a read does")
}

// TestUpdateFlowKeepsAName locks that update_flow refuses an empty name, as
// create_flow and the REST update do. The tool stored it, and the flow list
// then showed a row with no label.
func TestUpdateFlowKeepsAName(t *testing.T) {
	srv, admin, _ := setupMCPServer(t)
	session := connectMCP(t, srv, admin)

	res, created := callTool(t, session, "create_flow", map[string]any{"name": "Porch light"})
	require.False(t, res.IsError, "the flow must be created")
	id := created["id"].(string)

	res, _ = callTool(t, session, "update_flow", map[string]any{"id": id, "name": ""})
	assert.True(t, res.IsError, "an empty name is refused")
	res, renamed := callTool(t, session, "update_flow", map[string]any{"id": id, "name": "Porch"})
	require.False(t, res.IsError, "a real name is still a rename")
	assert.Equal(t, "Porch", renamed["name"], "the flow carries the new name")
}
