package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// faceplateGraph is a deployable flow with one faceplate whose "max" control
// is a number from 0 to 100, plus a node named by extra so a test can tell
// draft edits apart.
func faceplateGraph(extra string) map[string]any {
	g := manualGraph()
	nodes := g["nodes"].([]map[string]any)
	nodes = append(nodes, map[string]any{
		"id": "fp", "type": "faceplate", "position": map[string]any{"x": 0, "y": 200},
		"config": map[string]any{"name": "Simple", "controls": []any{
			map[string]any{"id": "max", "label": "Max", "type": "number", "default": 80, "min": 0, "max": 100},
		}},
	})
	if extra != "" {
		nodes = append(nodes, map[string]any{"id": extra, "type": "debug", "position": map[string]any{"x": 400, "y": 0}})
	}
	g["nodes"] = nodes
	return g
}

// deployedGraph reads the graph a flow's deployed version froze.
func deployedFaceplateGraph(t *testing.T, flowID string) (string, map[string]any) {
	t.Helper()
	var f models.Flow
	require.NoError(t, dbFind(&f, flowID), "the flow must load")
	var v models.FlowVersion
	require.NoError(t, dbFind(&v, *f.DeployedVersionID), "the deployed version must load")
	var g map[string]any
	require.NoError(t, json.Unmarshal([]byte(v.Graph), &g), "the deployed graph must decode")
	return v.Graph, g
}

// TestFaceplateApply pins feature 042's two Apply paths. An editor's Apply
// saves the draft with the values and deploys all of it. A faceplate-level
// share sees only the faceplate tabs, and its Apply deploys the current
// version with just the new values, leaving the editor's unfinished draft
// work undeployed while the draft takes the values.
func TestFaceplateApply(t *testing.T) {
	ts, _ := setupTestServer(t)
	alice := sessionWith(t, "alice", flowUserPerms...)
	kid := sessionWith(t, "kid")
	viewer := sessionWith(t, "viewer", flowUserPerms...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	status, resp := call(alice, http.MethodPost, "/api/flows", map[string]any{"name": "TV art mode"})
	require.Equalf(t, http.StatusCreated, status, "alice must create the flow: %v", resp)
	flowID := resp["data"].(map[string]any)["id"].(string)
	base := "/api/flows/" + flowID
	status, resp = call(alice, http.MethodPut, base+"/graph", map[string]any{"graph": faceplateGraph("")})
	require.Equalf(t, http.StatusOK, status, "the graph must save: %v", resp)
	status, resp = call(alice, http.MethodPost, base+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the flow must deploy: %v", resp)
	for user, level := range map[string]string{"kid": "faceplate", "viewer": "view"} {
		status, resp = call(alice, http.MethodPost, base+"/shares", map[string]any{"username": user, "permission": level})
		require.Equalf(t, http.StatusCreated, status, "the %s share must be stored: %v", level, resp)
	}

	t.Run("a faceplate share sees only the tabs", func(t *testing.T) {
		status, resp := call(kid, http.MethodGet, "/api/flows?limit=100", nil)
		require.Equalf(t, http.StatusOK, status, "the list must load without any flow permission: %v", resp)
		assert.Len(t, resp["data"], 1, "a deployed flow with faceplates is listed to a faceplate share")
		status, resp = call(kid, http.MethodGet, base+"/faceplates", nil)
		require.Equalf(t, http.StatusOK, status, "the tabs must load: %v", resp)
		tabs := resp["data"].(map[string]any)["tabs"].([]any)
		require.Len(t, tabs, 1, "the flow has one faceplate")
		assert.Equal(t, "Simple", tabs[0].(map[string]any)["name"], "the tab carries its name")
		status, _ = call(kid, http.MethodGet, base, nil)
		assert.Equal(t, http.StatusForbidden, status, "the graph stays closed to a faceplate share")
	})

	t.Run("faceplate apply deploys values without draft work", func(t *testing.T) {
		status, resp := call(alice, http.MethodPut, base+"/graph", map[string]any{"graph": faceplateGraph("unfinished")})
		require.Equalf(t, http.StatusOK, status, "alice's draft edit must save: %v", resp)

		status, _ = call(kid, http.MethodPost, base+"/faceplates/apply", map[string]any{"values": map[string]any{"fp": map[string]any{"max": 150}}})
		assert.Equal(t, http.StatusBadRequest, status, "a value above the control's maximum is refused")
		status, resp = call(kid, http.MethodPost, base+"/faceplates/apply", map[string]any{"values": map[string]any{"fp": map[string]any{"max": 60}}})
		require.Equalf(t, http.StatusOK, status, "a faceplate share applies a value: %v", resp)

		raw, _ := deployedFaceplateGraph(t, flowID)
		assert.Contains(t, raw, `"max":60`, "the deployed version carries the applied value")
		assert.NotContains(t, raw, "unfinished", "alice's draft work must not be deployed by a faceplate share")
		status, resp = call(alice, http.MethodGet, base, nil)
		require.Equal(t, http.StatusOK, status, "alice must read her draft")
		draft, _ := json.Marshal(resp["data"].(map[string]any)["graph"])
		assert.Contains(t, string(draft), "unfinished", "the draft keeps alice's edit")
		assert.Contains(t, string(draft), `"max":60`, "the draft takes the applied value")
	})

	t.Run("an editor's apply deploys the draft", func(t *testing.T) {
		status, resp := call(alice, http.MethodPost, base+"/faceplates/apply", map[string]any{"values": map[string]any{"fp": map[string]any{"max": 40}}})
		require.Equalf(t, http.StatusOK, status, "alice applies: %v", resp)
		raw, _ := deployedFaceplateGraph(t, flowID)
		assert.Contains(t, raw, "unfinished", "an editor's Apply deploys the draft edits too")
		assert.Contains(t, raw, `"max":40`, "and the new value")
	})

	t.Run("a view share cannot apply", func(t *testing.T) {
		status, _ := call(viewer, http.MethodPost, base+"/faceplates/apply", map[string]any{"values": map[string]any{"fp": map[string]any{"max": 10}}})
		assert.Equal(t, http.StatusForbidden, status, "view reads faceplates but does not change them")
	})
}

// dbFind loads one row by primary key.
func dbFind(dst any, id any) error {
	switch v := id.(type) {
	case string:
		return dbFirst(dst, uuid.MustParse(v))
	default:
		return dbFirst(dst, v)
	}
}

func dbFirst(dst any, id any) error {
	return db.DB.First(dst, "id = ?", id).Error
}

// TestPromotedSubflowWithFaceplatesIsListedToFaceplateShares pins that
// promotion records whether the version it deploys has faceplates. A
// faceplate-level share is listed only on that flag, and promotion wrote
// version 1 without setting it, so a promoted subflow holding a Faceplate node
// stayed hidden from the people it was shared with until someone deployed it
// again.
func TestPromotedSubflowWithFaceplatesIsListedToFaceplateShares(t *testing.T) {
	ts, admin := setupTestServer(t)
	kid := sessionWith(t, "kid")
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}
	get := func(token, path string) (int, map[string]any) { return call(token, http.MethodGet, path, nil) }

	parent := subflowParentGraph()
	sg := parent["subgraphs"].(map[string]any)["sg1"].(map[string]any)
	sg["nodes"] = append(sg["nodes"].([]map[string]any), map[string]any{
		"id": "fp", "type": "faceplate", "position": map[string]any{"x": 0, "y": 200},
		"config": map[string]any{"name": "Simple", "controls": []any{
			map[string]any{"id": "max", "label": "Max", "type": "number", "default": 80, "min": 0, "max": 100},
		}},
	})
	status, resp := call(admin, http.MethodPost, "/api/flows", map[string]any{"name": "Faceplate parent"})
	require.Equalf(t, http.StatusCreated, status, "the parent must be created: %v", resp)
	parentID := data(t, resp)["id"].(string)
	status, resp = call(admin, http.MethodPut, "/api/flows/"+parentID+"/graph", map[string]any{"graph": parent})
	require.Equalf(t, http.StatusOK, status, "the parent draft must save: %v", resp)

	status, resp = call(admin, http.MethodPost, "/api/flows/"+parentID+"/promote_subgraph",
		map[string]any{"subgraph_id": "sg1", "name": "Faceplate helper"})
	require.Equalf(t, http.StatusCreated, status, "the subgraph must promote: %v", resp)
	subflowID := data(t, resp)["id"].(string)

	status, resp = call(admin, http.MethodPost, "/api/flows/"+subflowID+"/shares",
		map[string]any{"username": "kid", "permission": "faceplate"})
	require.Equalf(t, http.StatusCreated, status, "the faceplate share must be stored: %v", resp)

	assert.Equal(t, "faceplate", listedFlowIDs(t, kid, get)[subflowID],
		"the promoted subflow is deployed with a faceplate, so its faceplate share must list it")
}
