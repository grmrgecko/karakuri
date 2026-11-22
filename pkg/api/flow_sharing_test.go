package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// flowUserPerms are the flow permissions the default User role is seeded
// with, so the users below match an ordinary household account.
var flowUserPerms = []string{
	permissions.PermFlowsRead, permissions.PermFlowsWrite, permissions.PermFlowsDeploy,
	permissions.PermRunsRead, permissions.PermRunsCancel,
}

// manualGraph is a deployable draft: one manual trigger into a debug node.
func manualGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "dbg", "type": "debug", "position": map[string]any{"x": 200, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "dbg", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// listedFlowIDs returns the IDs the flow list shows a user.
func listedFlowIDs(t *testing.T, token string, get func(token, path string) (int, map[string]any)) map[string]string {
	t.Helper()
	status, resp := get(token, "/api/flows?limit=100")
	require.Equalf(t, http.StatusOK, status, "the flow list must load: %v", resp)
	out := map[string]string{}
	for _, item := range resp["data"].([]any) {
		f := item.(map[string]any)
		level, _ := f["access_level"].(string)
		out[f["id"].(string)] = level
	}
	return out
}

// TestFlowSharing pins feature 043 against the real API. Before it, the
// default User role could read, change, deploy, and delete every flow on the
// server, because a flow's owner was recorded and never checked. Each step
// depends on the share state the previous one left, so they run in order.
func TestFlowSharing(t *testing.T) {
	ts, _ := setupTestServer(t)
	alice := sessionWith(t, "alice", flowUserPerms...)
	bob := sessionWith(t, "bob", flowUserPerms...)
	boss := sessionWith(t, "boss", permissions.PermFlowsRead, permissions.PermFlowsManage)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}
	get := func(token, path string) (int, map[string]any) { return call(token, http.MethodGet, path, nil) }

	status, resp := call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Porch light"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her flow: %v", resp)
	flowID := resp["data"].(map[string]any)["id"].(string)
	base := "/api/flows/" + flowID
	status, resp = call(alice, http.MethodPut, base+"/graph", map[string]any{"graph": manualGraph()})
	require.Equalf(t, http.StatusOK, status, "alice must save her graph: %v", resp)

	t.Run("an unshared flow is invisible to another user", func(t *testing.T) {
		assert.NotContains(t, listedFlowIDs(t, bob, get), flowID, "bob must not see alice's flow listed")
		for _, path := range []string{base, base + "/versions", base + "/run", base + "/export"} {
			status, _ := get(bob, path)
			assert.Equalf(t, http.StatusNotFound, status, "%s must answer not found to bob", path)
		}
		status, _ := call(bob, http.MethodDelete, base, nil)
		assert.Equal(t, http.StatusNotFound, status, "bob must not delete alice's flow")
		assert.Equal(t, "admin", listedFlowIDs(t, alice, get)[flowID], "the owner holds admin")
		assert.Equal(t, "admin", listedFlowIDs(t, boss, get)[flowID], "the manage permission reaches every flow")
	})

	shares := base + "/shares"
	var shareID float64
	t.Run("a view share reads but changes nothing", func(t *testing.T) {
		status, _ := call(alice, http.MethodPost, shares, map[string]any{"username": "bob", "permission": "owner"})
		assert.Equal(t, http.StatusBadRequest, status, "an unknown level must be refused")
		status, resp := call(alice, http.MethodPost, shares, map[string]any{"username": "bob", "permission": "view"})
		require.Equalf(t, http.StatusCreated, status, "the view share must be stored: %v", resp)
		shareID = resp["data"].(map[string]any)["id"].(float64)

		assert.Equal(t, "view", listedFlowIDs(t, bob, get)[flowID], "bob must now see the flow at view")
		status, _ = get(bob, base)
		assert.Equal(t, http.StatusOK, status, "a view share opens the graph")
		status, _ = call(bob, http.MethodPut, base+"/graph", map[string]any{"graph": manualGraph()})
		assert.Equal(t, http.StatusForbidden, status, "a view share must not save the graph")
		status, _ = call(bob, http.MethodPost, base+"/deploy", map[string]any{})
		assert.Equal(t, http.StatusForbidden, status, "a view share must not deploy")
		status, _ = call(bob, http.MethodPost, base+"/test", map[string]any{})
		assert.Equal(t, http.StatusForbidden, status, "a view share must not run the flow")
		status, _ = get(bob, shares)
		assert.Equal(t, http.StatusForbidden, status, "a view share must not list shares")
	})

	t.Run("an edit share deploys but does not share or delete", func(t *testing.T) {
		status, _ := call(alice, http.MethodDelete, shares+"/"+formatID(shareID), nil)
		require.Equal(t, http.StatusOK, status, "alice must remove the view share")
		status, resp := call(alice, http.MethodPost, shares, map[string]any{"username": "bob", "permission": "edit"})
		require.Equalf(t, http.StatusCreated, status, "the edit share must be stored: %v", resp)

		status, resp = call(bob, http.MethodPost, base+"/deploy", map[string]any{"comment": "bob's change"})
		assert.Equalf(t, http.StatusOK, status, "an edit share must deploy: %v", resp)
		status, _ = call(bob, http.MethodPost, shares, map[string]any{"username": "boss", "permission": "view"})
		assert.Equal(t, http.StatusForbidden, status, "an edit share must not share the flow onward")
		status, _ = call(bob, http.MethodDelete, base, nil)
		assert.Equal(t, http.StatusForbidden, status, "an edit share must not delete the flow")
	})

	t.Run("a faceplate share reaches no graph route", func(t *testing.T) {
		carol := sessionWith(t, "carol", flowUserPerms...)
		status, resp := call(alice, http.MethodPost, shares, map[string]any{"username": "carol", "permission": "faceplate"})
		require.Equalf(t, http.StatusCreated, status, "the faceplate share must be stored: %v", resp)
		assert.NotContains(t, listedFlowIDs(t, carol, get), flowID, "a faceplate share is not listed until the flow has faceplates")
		for _, path := range []string{base, base + "/versions", base + "/export", base + "/run"} {
			status, _ := get(carol, path)
			assert.Equalf(t, http.StatusForbidden, status, "%s must refuse a faceplate share", path)
		}
	})

	t.Run("a new reference needs view on the subflow", func(t *testing.T) {
		lib := sessionWith(t, "lib", flowUserPerms...)
		status, resp := call(lib, http.MethodPost, "/api/flows", map[string]any{"name": "Lamp library", "is_subflow": true})
		require.Equalf(t, http.StatusCreated, status, "the subflow must be created: %v", resp)
		subID := resp["data"].(map[string]any)["id"].(string)
		subGraph := map[string]any{
			"nodes": []map[string]any{
				{"id": "pi", "type": "subflow-input", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{"port_id": "in"}},
				{"id": "po", "type": "subflow-output", "position": map[string]any{"x": 200, "y": 0}, "config": map[string]any{"port_id": "out"}},
			},
			"edges":    []map[string]any{{"id": "se", "source": "pi", "source_handle": "out", "target": "po", "target_handle": "in"}},
			"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
		}
		status, resp = call(lib, http.MethodPut, "/api/flows/"+subID+"/graph", map[string]any{"graph": subGraph})
		require.Equalf(t, http.StatusOK, status, "the subflow graph must save: %v", resp)
		status, resp = call(lib, http.MethodPost, "/api/flows/"+subID+"/deploy", map[string]any{})
		require.Equalf(t, http.StatusOK, status, "the subflow must deploy: %v", resp)

		parent := manualGraph()
		parent["nodes"] = append(parent["nodes"].([]map[string]any),
			map[string]any{"id": "s1", "type": "subflow", "position": map[string]any{"x": 100, "y": 100}, "subflow_id": subID})
		status, _ = call(alice, http.MethodPut, base+"/graph", map[string]any{"graph": parent})
		assert.Equal(t, http.StatusBadRequest, status, "referencing a subflow alice was never shared must be refused")

		status, resp = call(lib, http.MethodPost, "/api/flows/"+subID+"/shares", map[string]any{"username": "alice", "permission": "view"})
		require.Equalf(t, http.StatusCreated, status, "lib must share the subflow with alice: %v", resp)
		share := formatID(resp["data"].(map[string]any)["id"].(float64))
		status, resp = call(alice, http.MethodPut, base+"/graph", map[string]any{"graph": parent})
		require.Equalf(t, http.StatusOK, status, "a view share on the subflow admits the reference: %v", resp)

		status, _ = call(lib, http.MethodDelete, "/api/flows/"+subID+"/shares/"+share, nil)
		require.Equal(t, http.StatusOK, status, "lib must withdraw the share")
		status, resp = call(alice, http.MethodPut, base+"/graph", map[string]any{"graph": parent})
		assert.Equalf(t, http.StatusOK, status, "a reference the draft already holds must keep saving after access is withdrawn: %v", resp)
	})
}

// formatID renders a JSON number ID for a path.
func formatID(id float64) string {
	return strconv.FormatInt(int64(id), 10)
}

// userIDOf looks up a test user's ID by name.
func userIDOf(t *testing.T, username string) string {
	t.Helper()
	var u models.User
	require.NoError(t, db.DB.Where("username = ?", username).First(&u).Error, "the test user must exist")
	return u.ID.String()
}

// TestFlowSharesReachRunsEventsAndTriggers pins the paths outside the flow
// routes that also expose a flow: the live run topics, the event log, and a
// dashboard binding one of its widget triggers. Before feature 043 each was
// gated by a global permission alone, so any holder could watch any flow's
// debug data, read its log, or publish a button that pressed it.
func TestFlowSharesReachRunsEventsAndTriggers(t *testing.T) {
	ts, _ := setupTestServer(t)
	read := recordEvents(t)
	alice := sessionWith(t, "alice", flowUserPerms...)
	bob := sessionWith(t, "bob", append([]string{permissions.PermEventsRead, permissions.PermDashboardsRead,
		permissions.PermDashboardsWrite, permissions.PermDashboardsDeploy}, flowUserPerms...)...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	status, resp := call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Garage door"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her flow: %v", resp)
	flowID := resp["data"].(map[string]any)["id"].(string)
	events.Record(events.Entry{Category: models.EventFlow, Subject: events.Subject{Kind: "flow", ID: flowID, Name: "Garage door"}, Message: "Garage door failed"})
	events.Record(events.Entry{Category: models.EventDevice, Subject: events.Subject{Kind: "control", ID: "c1", Name: "Porch"}, Message: "Porch set to on"})
	read()

	bobID := userIDOf(t, "bob")
	flowMessages := func() []string {
		t.Helper()
		status, resp := call(bob, http.MethodGet, "/api/events", nil)
		require.Equalf(t, http.StatusOK, status, "bob must read the log: %v", resp)
		var out []string
		for _, e := range resp["data"].(map[string]any)["events"].([]any) {
			out = append(out, e.(map[string]any)["message"].(string))
		}
		return out
	}

	assert.NotContains(t, flowMessages(), "Garage door failed", "bob must not read the log of a flow he was not shared")
	assert.Contains(t, flowMessages(), "Porch set to on", "entries about other subjects stay readable")
	assert.False(t, flowops.CanWatchFlow(bobID, flowID, permissions.PermRunsRead), "bob must not watch the flow's live runs")
	assert.False(t, flowops.EventVisible(context.Background(), uuid.MustParse(bobID), "flow", flowID), "the live log must drop the flow's entries for bob")

	// A dashboard button pressing alice's flow cannot be published by bob.
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Entity Control", PackageName: "entity-control", TagName: "entity-control-widget",
		Version: "0.1.0", Scope: widgets.WidgetScope{Bindings: "."},
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })
	status, resp = call(bob, http.MethodPost, "/api/dashboards", map[string]any{"name": "Garage panel"})
	require.Equalf(t, http.StatusCreated, status, "bob must create his dashboard: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	status, resp = call(bob, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]
	status, resp = call(bob, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": widgetID, "x": 0, "y": 0, "width": 2, "height": 2,
		"config": `{"source":"flow-trigger","flowId":"` + flowID + `","trigger":"open"}`,
	})
	require.Equalf(t, http.StatusCreated, status, "a draft may hold any binding: %v", resp)
	status, _ = call(bob, http.MethodPost, "/api/dashboards/"+dashID+"/deploy", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, status, "deploying a button for a flow bob cannot see must be refused without confirming the flow exists")

	status, resp = call(alice, http.MethodPost, "/api/flows/"+flowID+"/shares", map[string]any{"username": "bob", "permission": "view"})
	require.Equalf(t, http.StatusCreated, status, "alice must share at view: %v", resp)
	status, _ = call(bob, http.MethodPost, "/api/dashboards/"+dashID+"/deploy", map[string]any{})
	assert.Equal(t, http.StatusForbidden, status, "a view share does not let bob publish a button that runs the flow")
	assert.True(t, flowops.CanWatchFlow(bobID, flowID, permissions.PermRunsRead), "a view share watches the flow's live runs")
	assert.Contains(t, flowMessages(), "Garage door failed", "a view share reads the flow's log")

	status, resp = call(alice, http.MethodPost, "/api/flows/"+flowID+"/shares", map[string]any{"username": "bob", "permission": "edit"})
	require.Equalf(t, http.StatusCreated, status, "alice must share at edit: %v", resp)
	status, resp = call(bob, http.MethodPost, "/api/dashboards/"+dashID+"/deploy", map[string]any{})
	assert.Equalf(t, http.StatusOK, status, "an edit share lets bob publish the button: %v", resp)
}

// TestFolderSharing pins folder ownership and shares. A folder's owner and
// shares reach its subfolders and every flow inside, the way a dashboard
// group's reach its dashboards, so placing a flow or folder anywhere is a
// sharing change. Before this, folder owners were recorded and never checked,
// and every flow reader saw every folder's name.
func TestFolderSharing(t *testing.T) {
	ts, _ := setupTestServer(t)
	alice := sessionWith(t, "alice", flowUserPerms...)
	bob := sessionWith(t, "bob", flowUserPerms...)
	dave := sessionWith(t, "dave", flowUserPerms...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}
	get := func(token, path string) (int, map[string]any) { return call(token, http.MethodGet, path, nil) }
	folderLevels := func(token string) map[float64]string {
		t.Helper()
		status, resp := get(token, "/api/folders")
		require.Equalf(t, http.StatusOK, status, "the folder list must load: %v", resp)
		out := map[float64]string{}
		for _, item := range resp["data"].([]any) {
			f := item.(map[string]any)
			level, _ := f["access_level"].(string)
			out[f["id"].(float64)] = level
		}
		return out
	}

	status, resp := call(alice, http.MethodPost, "/api/folders", map[string]any{"name": "Garage"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her folder: %v", resp)
	folderID := resp["data"].(map[string]any)["id"].(float64)
	folder := "/api/folders/" + formatID(folderID)
	status, resp = call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Garage door", "folder_id": folderID})
	require.Equalf(t, http.StatusCreated, status, "alice must create a flow in her folder: %v", resp)
	flowID := resp["data"].(map[string]any)["id"].(string)

	assert.NotContains(t, folderLevels(bob), folderID, "bob must not see alice's folder")
	assert.NotContains(t, listedFlowIDs(t, bob, get), flowID, "bob must not see the flow inside it")
	status, _ = call(bob, http.MethodPost, "/api/flows", map[string]any{"name": "Planted", "folder_id": folderID})
	assert.Equal(t, http.StatusBadRequest, status, "bob must not create a flow in a folder he cannot see")

	status, resp = call(alice, http.MethodPost, folder+"/shares", map[string]any{"username": "bob", "permission": "view"})
	require.Equalf(t, http.StatusCreated, status, "alice must share the folder at view: %v", resp)
	assert.Equal(t, "view", folderLevels(bob)[folderID], "a folder share shows the folder")
	assert.Equal(t, "view", listedFlowIDs(t, bob, get)[flowID], "a folder share reaches the flows inside")
	status, _ = call(bob, http.MethodPut, "/api/flows/"+flowID+"/graph", map[string]any{"graph": manualGraph()})
	assert.Equal(t, http.StatusForbidden, status, "a view share on the folder does not change its flows")
	status, _ = call(bob, http.MethodPut, folder, map[string]any{"name": "Bob's now"})
	assert.Equal(t, http.StatusForbidden, status, "a view share does not rename the folder")

	status, resp = call(alice, http.MethodPost, folder+"/shares", map[string]any{"username": "bob", "permission": "edit"})
	require.Equalf(t, http.StatusCreated, status, "alice must share the folder at edit: %v", resp)
	status, resp = call(bob, http.MethodPost, "/api/flows", map[string]any{"name": "Garage light", "folder_id": folderID})
	assert.Equalf(t, http.StatusCreated, status, "an edit share adds flows to the folder: %v", resp)
	status, _ = call(bob, http.MethodPut, folder, map[string]any{"name": "Garage and shed"})
	assert.Equal(t, http.StatusOK, status, "an edit share renames the folder in place")
	status, resp = call(bob, http.MethodPost, "/api/folders", map[string]any{"name": "Bob's"})
	require.Equalf(t, http.StatusCreated, status, "bob must create his own folder: %v", resp)
	bobFolder := resp["data"].(map[string]any)["id"].(float64)
	status, _ = call(bob, http.MethodPut, folder, map[string]any{"name": "Garage and shed", "parent_id": bobFolder})
	assert.Equal(t, http.StatusForbidden, status, "moving the folder into one bob owns would make him its admin, so it needs admin")
	status, _ = call(bob, http.MethodDelete, folder, nil)
	assert.Equal(t, http.StatusForbidden, status, "an edit share does not delete the folder")
	status, _ = call(bob, http.MethodPut, "/api/flows/"+flowID+"/folder", map[string]any{"folder_id": bobFolder})
	assert.Equal(t, http.StatusForbidden, status, "moving alice's flow needs admin on the flow")

	status, resp = call(alice, http.MethodPost, "/api/flows/"+flowID+"/shares", map[string]any{"username": "dave", "permission": "view"})
	require.Equalf(t, http.StatusCreated, status, "alice must share the flow directly with dave: %v", resp)
	status, resp = get(dave, "/api/flows/"+flowID)
	require.Equalf(t, http.StatusOK, status, "dave must open the shared flow: %v", resp)
	assert.Nil(t, resp["data"].(map[string]any)["folder_id"], "a flow shared out of a hidden folder must not name the folder")
	assert.NotContains(t, folderLevels(dave), folderID, "a flow share does not reveal its folder")

	status, _ = call(dave, http.MethodPost, "/api/flows", map[string]any{"name": "Garage door"})
	assert.Equal(t, http.StatusConflict, status, "a name dave can see in use is refused")
	status, _ = call(dave, http.MethodPost, "/api/flows", map[string]any{"name": "Garage light"})
	assert.Equal(t, http.StatusCreated, status, "a name used only by bob's flow, which dave cannot see, is free to dave")
	status, resp = call(dave, http.MethodPost, "/api/flows", map[string]any{"name": "Secret thing"})
	require.Equalf(t, http.StatusCreated, status, "a fresh name works: %v", resp)
	status, resp = call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Secret thing"})
	assert.Equalf(t, http.StatusCreated, status, "a name used only by a flow alice cannot see must not be refused: %v", resp)
}

// TestFolderUpdateDoesNotNameAHiddenParent locks the folder update response
// to what the folder list shows (specs/043-flow-sharing FR-017). A folder
// shared out of a private parent is listed at the caller's root, but renaming
// it answered with the stored parent_id, naming a folder the caller cannot
// see. The owner, who can see the parent, still gets it back.
func TestFolderUpdateDoesNotNameAHiddenParent(t *testing.T) {
	ts, _ := setupTestServer(t)
	alice := sessionWith(t, "alice", flowUserPerms...)
	bob := sessionWith(t, "bob", flowUserPerms...)

	status, resp := doJSON(t, ts, alice, http.MethodPost, "/api/folders", map[string]any{"name": "Private"})
	require.Equalf(t, http.StatusCreated, status, "alice must create the parent: %v", resp)
	parentID := resp["data"].(map[string]any)["id"].(float64)
	status, resp = doJSON(t, ts, alice, http.MethodPost, "/api/folders", map[string]any{"name": "Garage", "parent_id": parentID})
	require.Equalf(t, http.StatusCreated, status, "alice must create the child: %v", resp)
	child := "/api/folders/" + formatID(resp["data"].(map[string]any)["id"].(float64))
	status, resp = doJSON(t, ts, alice, http.MethodPost, child+"/shares", map[string]any{"username": "bob", "permission": "edit"})
	require.Equalf(t, http.StatusCreated, status, "alice must share the child at edit: %v", resp)

	status, resp = doJSON(t, ts, bob, http.MethodPut, child, map[string]any{"name": "Garage flows"})
	require.Equalf(t, http.StatusOK, status, "an edit share renames the folder: %v", resp)
	assert.NotContains(t, resp["data"], "parent_id", "the response must not name a parent bob cannot see")

	status, resp = doJSON(t, ts, alice, http.MethodPut, child, map[string]any{"name": "Garage", "parent_id": parentID})
	require.Equalf(t, http.StatusOK, status, "alice renames her own folder: %v", resp)
	assert.Equal(t, parentID, resp["data"].(map[string]any)["parent_id"], "bob's rename left the folder under its parent, and alice is shown it")
}

// TestSlugsResolveAmongVisibleItems pins slugs as readable, non-unique
// addresses. Two owners may give a dashboard or flow the same slug. A slug
// resolves to the one item the caller can see, several visible items make it
// ambiguous with the choices listed by ID, and an item the caller cannot see
// never takes part. Before this, the first owner to pick a slug blocked it
// for everyone and the refusal confirmed the private item existed.
func TestSlugsResolveAmongVisibleItems(t *testing.T) {
	ts, _ := setupTestServer(t)
	perms := append([]string{permissions.PermDashboardsRead, permissions.PermDashboardsWrite}, flowUserPerms...)
	alice := sessionWith(t, "alice", perms...)
	bob := sessionWith(t, "bob", perms...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}
	ids := func(resp map[string]any) []string {
		var out []string
		for _, m := range resp["data"].([]any) {
			out = append(out, m.(map[string]any)["id"].(string))
		}
		return out
	}

	status, resp := call(alice, http.MethodPost, "/api/dashboards", map[string]any{"name": "Kitchen"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her dashboard: %v", resp)
	aliceDash := resp["data"].(map[string]any)["id"].(string)
	status, resp = call(bob, http.MethodPost, "/api/dashboards", map[string]any{"name": "Kitchen"})
	require.Equalf(t, http.StatusCreated, status, "bob may use the same slug, since he cannot see alice's: %v", resp)
	bobDash := resp["data"].(map[string]any)["id"].(string)
	status, _ = call(bob, http.MethodPost, "/api/dashboards", map[string]any{"name": "Kitchen"})
	assert.Equal(t, http.StatusConflict, status, "a slug bob can already see in use is refused")

	status, resp = call(bob, http.MethodGet, "/api/dashboards/kitchen", nil)
	require.Equalf(t, http.StatusOK, status, "the slug must resolve to bob's own dashboard: %v", resp)
	assert.Equal(t, bobDash, resp["data"].(map[string]any)["id"], "alice's private dashboard must not take part")

	status, resp = call(alice, http.MethodPost, "/api/dashboards/"+aliceDash+"/shares", map[string]any{"username": "bob", "permission": "read"})
	require.Equalf(t, http.StatusCreated, status, "alice must share her dashboard: %v", resp)
	status, resp = call(bob, http.MethodGet, "/api/dashboards/kitchen", nil)
	assert.Equal(t, http.StatusConflict, status, "two visible dashboards make the slug ambiguous")
	assert.Equal(t, "AMBIGUOUS", resp["error"], "the error names the ambiguity so a client can offer a choice")
	status, resp = call(bob, http.MethodGet, "/api/dashboards/by_slug/kitchen", nil)
	require.Equal(t, http.StatusOK, status, "the chooser list must load")
	assert.ElementsMatch(t, []string{aliceDash, bobDash}, ids(resp), "the chooser lists both dashboards by ID")
	status, resp = call(bob, http.MethodGet, "/api/dashboards/"+aliceDash, nil)
	assert.Equalf(t, http.StatusOK, status, "the ID still opens either one: %v", resp)

	status, resp = call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Porch light"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her flow: %v", resp)
	status, resp = call(bob, http.MethodPost, "/api/flows", map[string]any{"name": "Porch light"})
	require.Equalf(t, http.StatusCreated, status, "bob may reuse the flow name and slug: %v", resp)
	bobFlow := resp["data"].(map[string]any)["id"].(string)
	status, resp = call(bob, http.MethodGet, "/api/flows/by_slug/porch-light", nil)
	require.Equal(t, http.StatusOK, status, "the flow chooser list must load")
	assert.Equal(t, []string{bobFlow}, ids(resp), "only the flow bob may read is offered")
}

// passThroughSubflow is a deployable subflow definition: one input wired
// straight to one output.
func passThroughSubflow() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "pi", "type": "subflow-input", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{"port_id": "in"}},
			{"id": "po", "type": "subflow-output", "position": map[string]any{"x": 200, "y": 0}, "config": map[string]any{"port_id": "out"}},
		},
		"edges":    []map[string]any{{"id": "se", "source": "pi", "source_handle": "out", "target": "po", "target_handle": "in"}},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// TestFlowImportChecksSubflowReferences pins that importing a document is a
// save like any other: a reference to a shared subflow needs view on it. A
// draft save refused such a reference, but an imported document carrying the
// same subflow_id was stored, and since later saves keep references the draft
// already holds, the importer could deploy and run a subflow they were never
// shared.
func TestFlowImportChecksSubflowReferences(t *testing.T) {
	ts, _ := setupTestServer(t)
	lib := sessionWith(t, "lib", flowUserPerms...)
	alice := sessionWith(t, "alice", flowUserPerms...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	status, resp := call(lib, http.MethodPost, "/api/flows", map[string]any{"name": "Lamp library", "is_subflow": true})
	require.Equalf(t, http.StatusCreated, status, "the subflow must be created: %v", resp)
	subID := resp["data"].(map[string]any)["id"].(string)
	status, resp = call(lib, http.MethodPut, "/api/flows/"+subID+"/graph", map[string]any{"graph": passThroughSubflow()})
	require.Equalf(t, http.StatusOK, status, "the subflow graph must save: %v", resp)
	status, resp = call(lib, http.MethodPost, "/api/flows/"+subID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the subflow must deploy: %v", resp)

	graph := manualGraph()
	graph["nodes"] = append(graph["nodes"].([]map[string]any),
		map[string]any{"id": "s1", "type": "subflow", "position": map[string]any{"x": 100, "y": 100}, "subflow_id": subID})
	doc := map[string]any{"document": map[string]any{"karakuri_flow": 1, "name": "Borrowed", "graph": graph}}

	status, resp = call(alice, http.MethodPost, "/api/flows/import", doc)
	assert.Equalf(t, http.StatusBadRequest, status, "importing a reference to a subflow alice was never shared must be refused: %v", resp)

	status, resp = call(lib, http.MethodPost, "/api/flows/"+subID+"/shares", map[string]any{"username": "alice", "permission": "view"})
	require.Equalf(t, http.StatusCreated, status, "lib must share the subflow with alice: %v", resp)
	status, resp = call(alice, http.MethodPost, "/api/flows/import", doc)
	assert.Equalf(t, http.StatusCreated, status, "a view share on the subflow admits the imported reference: %v", resp)
}

// TestShareListingsOmitAccountFields pins that listing who a flow, folder, or
// dashboard is shared with names each grantee and nothing more. The routes
// need access to the item, not permission to read users. The flow and folder
// listings returned every grantee's email address and UI preferences, and the
// dashboard listing their preferences.
func TestShareListingsOmitAccountFields(t *testing.T) {
	ts, _ := setupTestServer(t)
	alice := sessionWith(t, "alice", append([]string{permissions.PermDashboardsRead, permissions.PermDashboardsWrite}, flowUserPerms...)...)
	sessionWith(t, "bob", flowUserPerms...)
	require.NoError(t, db.DB.Model(&models.User{}).Where("username = ?", "bob").
		Update("preferences", models.Preferences{"theme": "dark"}).Error, "bob must have preferences to leak")
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	status, resp := call(alice, http.MethodPost, "/api/folders", map[string]any{"name": "Garage"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her folder: %v", resp)
	folder := "/api/folders/" + formatID(resp["data"].(map[string]any)["id"].(float64))
	status, resp = call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Garage door"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her flow: %v", resp)
	flowPath := "/api/flows/" + resp["data"].(map[string]any)["id"].(string)

	status, resp = call(alice, http.MethodPost, "/api/dashboards", map[string]any{"name": "Garage panel"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her dashboard: %v", resp)
	dashboard := "/api/dashboards/" + resp["data"].(map[string]any)["id"].(string)

	for _, base := range []string{flowPath, folder, dashboard} {
		status, resp = call(alice, http.MethodPost, base+"/shares", map[string]any{"username": "bob", "permission": "view"})
		require.Equalf(t, http.StatusCreated, status, "%s must be shared with bob: %v", base, resp)
		status, resp = call(alice, http.MethodGet, base+"/shares", nil)
		require.Equalf(t, http.StatusOK, status, "%s shares must list: %v", base, resp)
		entries := resp["data"].([]any)
		require.Lenf(t, entries, 1, "%s holds the one share", base)
		user := entries[0].(map[string]any)["user"].(map[string]any)
		assert.Equalf(t, "bob", user["username"], "%s: the listing names the grantee", base)
		assert.Emptyf(t, user["email"], "%s: a grantee's email address is no part of a share", base)
		assert.Emptyf(t, user["preferences"], "%s: a grantee's preferences are no part of a share", base)
	}
}

// TestPromotedSubflowKeepsItsDependencies pins that a promoted subflow records
// what its graph references. Promotion stored the new flow with no dependency
// rows while rebuilding the parent's, so a shared subflow called only from
// inside the promoted subgraph lost its last recorded dependent and could be
// deleted out from under the flow that calls it.
func TestPromotedSubflowKeepsItsDependencies(t *testing.T) {
	ts, token := setupTestServer(t)
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	status, resp := call(http.MethodPost, "/api/flows", map[string]any{"name": "Inner helper", "is_subflow": true})
	require.Equalf(t, http.StatusCreated, status, "the inner subflow must be created: %v", resp)
	innerID := data(t, resp)["id"].(string)
	status, resp = call(http.MethodPut, "/api/flows/"+innerID+"/graph", map[string]any{"graph": passThroughSubflow()})
	require.Equalf(t, http.StatusOK, status, "the inner subflow graph must save: %v", resp)
	status, resp = call(http.MethodPost, "/api/flows/"+innerID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the inner subflow must deploy: %v", resp)

	// The parent's local subgraph calls the inner shared subflow.
	parent := subflowParentGraph()
	parent["subgraphs"] = map[string]any{"sg1": map[string]any{
		"nodes": []map[string]any{
			{"id": "pi", "type": "subflow-input", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{"port_id": "in"}},
			{"id": "call", "type": "subflow", "position": map[string]any{"x": 200, "y": 0}, "subflow_id": innerID},
			{"id": "po", "type": "subflow-output", "position": map[string]any{"x": 400, "y": 0}, "config": map[string]any{"port_id": "out"}},
		},
		"edges": []map[string]any{
			{"id": "se1", "source": "pi", "source_handle": "out", "target": "call", "target_handle": "in"},
			{"id": "se2", "source": "call", "source_handle": "out", "target": "po", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}}
	status, resp = call(http.MethodPost, "/api/flows", map[string]any{"name": "Outer parent"})
	require.Equalf(t, http.StatusCreated, status, "the parent must be created: %v", resp)
	parentID := data(t, resp)["id"].(string)
	status, resp = call(http.MethodPut, "/api/flows/"+parentID+"/graph", map[string]any{"graph": parent})
	require.Equalf(t, http.StatusOK, status, "the parent draft must save: %v", resp)

	status, resp = call(http.MethodPost, "/api/flows/"+parentID+"/promote_subgraph",
		map[string]any{"subgraph_id": "sg1", "name": "Outer helper"})
	require.Equalf(t, http.StatusCreated, status, "the subgraph must promote: %v", resp)

	status, resp = call(http.MethodDelete, "/api/flows/"+innerID, nil)
	assert.Equalf(t, http.StatusConflict, status,
		"the promoted subflow still calls the inner one, so deleting it must be refused: %v", resp)
}

// TestSubflowImportChecksNestedReferences pins that copying a shared subflow
// into a draft is a save like any other. The copy carries the subflow's own
// calls to other shared subflows, and a draft save refuses a reference to one
// the author may not read. Importing stored it unchecked, and since later
// saves keep references the draft already holds, the importer could then call
// the inner subflow directly and deploy it.
func TestSubflowImportChecksNestedReferences(t *testing.T) {
	ts, _ := setupTestServer(t)
	lib := sessionWith(t, "lib", flowUserPerms...)
	alice := sessionWith(t, "alice", flowUserPerms...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}
	subflow := func(name string, graph map[string]any) string {
		t.Helper()
		status, resp := call(lib, http.MethodPost, "/api/flows", map[string]any{"name": name, "is_subflow": true})
		require.Equalf(t, http.StatusCreated, status, "%s must be created: %v", name, resp)
		id := resp["data"].(map[string]any)["id"].(string)
		status, resp = call(lib, http.MethodPut, "/api/flows/"+id+"/graph", map[string]any{"graph": graph})
		require.Equalf(t, http.StatusOK, status, "%s must save: %v", name, resp)
		status, resp = call(lib, http.MethodPost, "/api/flows/"+id+"/deploy", map[string]any{})
		require.Equalf(t, http.StatusOK, status, "%s must deploy: %v", name, resp)
		return id
	}

	innerID := subflow("Private helper", passThroughSubflow())
	outerID := subflow("Shared wrapper", map[string]any{
		"nodes": []map[string]any{
			{"id": "pi", "type": "subflow-input", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{"port_id": "in"}},
			{"id": "call", "type": "subflow", "position": map[string]any{"x": 200, "y": 0}, "subflow_id": innerID},
			{"id": "po", "type": "subflow-output", "position": map[string]any{"x": 400, "y": 0}, "config": map[string]any{"port_id": "out"}},
		},
		"edges": []map[string]any{
			{"id": "se1", "source": "pi", "source_handle": "out", "target": "call", "target_handle": "in"},
			{"id": "se2", "source": "call", "source_handle": "out", "target": "po", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	})
	status, resp := call(lib, http.MethodPost, "/api/flows/"+outerID+"/shares", map[string]any{"username": "alice", "permission": "view"})
	require.Equalf(t, http.StatusCreated, status, "lib must share the wrapper with alice: %v", resp)

	status, resp = call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "Alice flow"})
	require.Equalf(t, http.StatusCreated, status, "alice must create her flow: %v", resp)
	flowPath := "/api/flows/" + resp["data"].(map[string]any)["id"].(string)

	status, resp = call(alice, http.MethodPost, flowPath+"/import_subflow", map[string]any{"subflow_id": outerID})
	assert.Equalf(t, http.StatusBadRequest, status,
		"the copy would call a subflow alice was never shared, so the import must be refused: %v", resp)
	status, resp = call(alice, http.MethodGet, flowPath, nil)
	require.Equalf(t, http.StatusOK, status, "alice must be able to read her flow: %v", resp)
	assert.NotContains(t, fmt.Sprint(resp["data"].(map[string]any)["graph"]), innerID,
		"a refused import must leave no reference to the private subflow in the draft")

	status, resp = call(lib, http.MethodPost, "/api/flows/"+innerID+"/shares", map[string]any{"username": "alice", "permission": "view"})
	require.Equalf(t, http.StatusCreated, status, "lib must share the helper with alice: %v", resp)
	status, resp = call(alice, http.MethodPost, flowPath+"/import_subflow", map[string]any{"subflow_id": outerID})
	assert.Equalf(t, http.StatusOK, status, "a view share on the helper admits the copied reference: %v", resp)
}
