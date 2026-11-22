package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/api"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupTestServer boots the real API on a real HTTP listener over a real SQLite
// database: the router, the middleware chain, the permission gates, and the
// session tokens are all the production ones. That is what lets these tests
// assert on status codes and stored rows rather than on handler internals.
func setupTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	require.NoError(t, config.Load(), "the API needs a config for its JWT settings")

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

	// An admin user with a real session token, so the requests below go through
	// the same authentication the browser does.
	hash, err := auth.HashPassword("password")
	require.NoError(t, err, "the fixture user needs a password hash")

	user := &models.User{ID: uuid.New(), Username: "admin", Email: "admin@example.com", PasswordHash: hash}
	require.NoError(t, db.DB.Create(user).Error, "the fixture user must be stored")

	var adminGroup models.UserGroup
	require.NoError(t, db.DB.Where("name = ?", "Admin").First(&adminGroup).Error,
		"seeding must have created the admin group the user joins")
	require.NoError(t, db.DB.Create(&models.UserGroupMember{GroupID: adminGroup.ID, UserID: user.ID}).Error,
		"permissions are group-first, so the user needs the membership to be allowed anything")

	tokens, err := auth.CreateSession(user, "127.0.0.1", "test")
	require.NoError(t, err, "the requests below authenticate with a real session token")

	// Build the API router.
	r := chi.NewRouter()
	api.RegisterRoutes(r, zap.NewNop())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return srv, tokens.AccessToken
}

// doJSON performs a JSON request against the test server and decodes the body.
func doJSON(t *testing.T, srv *httptest.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()

	var reqBody bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&reqBody).Encode(body), "the request body must encode")
	}

	req, err := http.NewRequest(method, srv.URL+path, &reqBody)
	require.NoErrorf(t, err, "the request to %s %s must build", method, path)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := srv.Client().Do(req)
	require.NoErrorf(t, err, "%s %s must reach the server", method, path)
	defer func() { _ = resp.Body.Close() }()

	var decoded map[string]any
	require.NoErrorf(t, json.NewDecoder(resp.Body).Decode(&decoded),
		"every response is JSON, including the errors: %s %s", method, path)
	return resp.StatusCode, decoded
}

// validGraph returns a manual -> delay -> debug chain graph document.
func validGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "n1", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "n2", "type": "delay", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{"duration": "5s"}},
			{"id": "n3", "type": "debug", "position": map[string]any{"x": 400, "y": 0},
				"config": map[string]any{"note": "end of chain"}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "n1", "source_handle": "out", "target": "n2", "target_handle": "in"},
			{"id": "e2", "source": "n2", "source_handle": "out", "target": "n3", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// TestFlowLifecycle walks a flow through the loop an author actually uses,
// against the real router and database: create, save a draft, deploy, enable,
// edit, list, delete. The ordering constraints are the substance — a flow
// cannot be enabled before it is deployed, because arming a trigger on a graph
// nobody froze would run whatever the draft happened to hold — and the invalid
// save proves a rejected write leaves the stored draft untouched rather than
// half-applied.
func TestFlowLifecycle(t *testing.T) {
	srv, token := setupTestServer(t)

	// The node type catalog must list the core nodes.
	// The palette is drawn entirely from this endpoint, so a node type that
	// registered but never reached it is a node nobody can place.
	status, resp := doJSON(t, srv, token, http.MethodGet, "/api/nodetypes", nil)
	require.Equalf(t, http.StatusOK, status, "the node type catalog must be served: %v", resp)

	types, ok := resp["data"].([]any)
	require.Truef(t, ok, "the catalog is a list: %v", resp)

	listed := make([]string, 0, len(types))
	for _, entry := range types {
		meta, ok := entry.(map[string]any)
		require.Truef(t, ok, "a catalog entry is %T, want an object", entry)
		name, _ := meta["type"].(string)
		listed = append(listed, name)
	}
	for _, want := range []string{
		"manual", "webhook", "schedule", "delay", "trigger", "debug", "switch",
		"filter", "change", "template", "script", "split", "join", "batch",
		"aggregate", "http-request", "wol", "ping", "note",
		"file-read", "file-write", "file-watch",
		"subflow", "subflow-input", "subflow-output",
	} {
		assert.Containsf(t, listed, want, "%q registered but never reaches the palette, so nobody can place it", want)
	}

	// Create a flow.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "Test Flow", "description": "Lifecycle test"})
	require.Equalf(t, http.StatusCreated, status, "creating a flow must answer 201: %v", resp)

	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "the created flow comes back under data")
	flowID, _ := data["id"].(string)
	require.NotEmpty(t, flowID, "the id is how everything below addresses the flow")

	assert.Equal(t, "test-flow", data["slug"], "the slug is derived from the name and appears in the metrics labels")
	assert.Equal(t, false, data["deployed"], "a new flow has nothing frozen yet")
	assert.Equal(t, true, data["has_draft_changes"],
		"with no deployed version the draft differs from it by definition, which is what the editor's badge reads")

	graph, ok := data["graph"].(map[string]any)
	require.Truef(t, ok, "a new flow carries an empty graph document, not a null: %v", data["graph"])
	assert.Empty(t, graph["nodes"], "a new flow starts with no nodes")

	// Save a valid graph to the draft.
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": validGraph()})
	require.Equalf(t, http.StatusOK, status, "a valid draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/enable", nil)
	require.Equalf(t, http.StatusBadRequest, status,
		"enabling arms triggers against the deployed graph, and there is none: %v", resp)

	// Deploy the draft.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy",
		map[string]any{"comment": "first deploy"})
	require.Equalf(t, http.StatusOK, status, "deploying a valid draft must succeed: %v", resp)

	data, ok = resp["data"].(map[string]any)
	require.True(t, ok, "the deployed flow comes back under data")
	assert.Equal(t, true, data["deployed"], "a version was frozen, so the flow is deployed")
	assert.Equal(t, false, data["has_draft_changes"],
		"the draft and the frozen version are now identical, which is computed by comparing them rather than stored")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/enable", nil)
	require.Equalf(t, http.StatusOK, status, "a deployed flow must be enableable: %v", resp)
	assert.Equal(t, true, resp["data"].(map[string]any)["enabled"], "enabling is what arms the flow's triggers")

	// The version list must show the deployed version.
	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID+"/versions", nil)
	require.Equalf(t, http.StatusOK, status, "the version history must be served: %v", resp)

	versions, ok := resp["data"].([]any)
	require.True(t, ok, "the history is a list")
	require.Len(t, versions, 1, "one deploy makes one version")

	v, ok := versions[0].(map[string]any)
	require.True(t, ok, "a version entry is an object")
	assert.Equal(t, float64(1), v["version"], "versions are numbered from one")
	assert.Equal(t, "first deploy", v["comment"], "the deploy comment is the only note attached to a frozen version")

	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID+"/versions/1", nil)
	require.Equalf(t, http.StatusOK, status, "a numbered version must be retrievable: %v", resp)

	graph, ok = resp["data"].(map[string]any)["graph"].(map[string]any)
	require.True(t, ok, "a version carries its frozen graph")
	assert.Len(t, graph["nodes"], 3, "the frozen graph is the draft as it stood, all three nodes of it")

	// Saving an invalid graph must return validation details.
	invalid := map[string]any{
		"nodes": []map[string]any{
			{"id": "n1", "type": "does-not-exist", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "n2", "type": "debug", "position": map[string]any{"x": 100, "y": 0}},
			{"id": "n3", "type": "manual", "position": map[string]any{"x": 0, "y": 100}},
			{"id": "n4", "type": "manual", "position": map[string]any{"x": 0, "y": 200}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "n3", "source_handle": "out", "target": "n2", "target_handle": "in"},
			{"id": "e2", "source": "n4", "source_handle": "out", "target": "n2", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": invalid})
	require.Equalf(t, http.StatusBadRequest, status, "an unknown node type must be refused even on a draft save: %v", resp)
	assert.Equal(t, "VALIDATION_ERROR", resp["error"], "the editor renders problems only for this code")

	details, ok := resp["details"].(map[string]any)
	require.Truef(t, ok, "validation problems come back as details: %v", resp)
	assert.NotEmpty(t, details, "the editor highlights nodes from these details, so an empty map explains nothing")

	// The rejected save must not have modified the stored draft.
	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID, nil)
	require.Equalf(t, http.StatusOK, status, "the flow must still be readable: %v", resp)

	graph, ok = resp["data"].(map[string]any)["graph"].(map[string]any)
	require.True(t, ok, "the flow carries its draft graph")
	assert.Len(t, graph["nodes"], 3,
		"a rejected save must leave the stored draft exactly as it was, not half-applied")

	// Update flow metadata.
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID,
		map[string]any{"description": "updated"})
	require.Equalf(t, http.StatusOK, status, "editing a flow's metadata must succeed: %v", resp)
	assert.Equal(t, "updated", resp["data"].(map[string]any)["description"],
		"identity is not versioned, so a description change takes effect without a deploy")

	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows", nil)
	require.Equalf(t, http.StatusOK, status, "the flow list must be served: %v", resp)
	assert.Len(t, resp["data"], 1, "the one flow created above must be listed")

	status, resp = doJSON(t, srv, token, http.MethodDelete, "/api/flows/"+flowID, nil)
	require.Equalf(t, http.StatusOK, status, "deleting a flow must succeed: %v", resp)

	status, _ = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID, nil)
	assert.Equal(t, http.StatusNotFound, status, "a deleted flow must be gone, not merely hidden from the list")

	// Every route above went through the real middleware chain, so the
	// unauthenticated case is worth stating once here.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/flows", nil)
	require.NoError(t, err, "the unauthenticated request must build")
	unauth, err := srv.Client().Do(req)
	require.NoError(t, err, "the request must reach the server")
	defer func() { _ = unauth.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, unauth.StatusCode,
		"the API is behind authentication, and a missing token must be refused rather than treated as anonymous")
}

// TestFlowCreateConflict covers a duplicate flow name. The name becomes the
// slug, which appears in metrics labels and in URLs, so two flows sharing one
// would make a metric ambiguous; the second create is refused with 409 rather
// than silently renamed, so the author picks the name themselves.
func TestFlowCreateConflict(t *testing.T) {
	srv, token := setupTestServer(t)

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "Same Name"})
	require.Equalf(t, http.StatusCreated, status, "the first flow of that name must be created: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "Same Name"})
	assert.Equalf(t, http.StatusConflict, status,
		"the name becomes the slug used in metrics and URLs, so a duplicate must be refused rather than renamed: %v", resp)
}
