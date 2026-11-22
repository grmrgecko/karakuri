package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/eventsources"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordEvents runs a recorder and the sources for the test and returns a
// function that flushes them and reads every entry back, oldest first.
func recordEvents(t *testing.T) func() []models.Event {
	t.Helper()
	require.NoError(t, controls.S.Load())
	events.R = events.NewRecorder(nil, nil)
	events.R.Start()
	src := eventsources.Start(nil, nil)
	stopped := false
	stop := func() {
		if !stopped {
			src.Stop()
			events.R.Stop()
			events.R = nil
			stopped = true
		}
	}
	t.Cleanup(stop)
	return func() []models.Event {
		stop()
		var rows []models.Event
		require.NoError(t, db.DB.Order("id").Find(&rows).Error)
		return rows
	}
}

func postWith(t *testing.T, ts *httptest.Server, token, path string, body any, headers map[string]string) int {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// TestControlWritesNameTheirActor covers SC-001 for the HTTP paths: a write
// with a session names the user, one with an API key names the key and its
// owner, and one from a dashboard names the dashboard.
func TestControlWritesNameTheirActor(t *testing.T) {
	ts, admin := setupTestServer(t)
	read := recordEvents(t)
	previous := config.C.Security.EncryptionKey
	config.C.Security.EncryptionKey = testEncryptionKey
	t.Cleanup(func() { config.C.Security.EncryptionKey = previous })

	ctl, err := controls.S.Create("Event test switch", controls.KindBoolean, "", nil, false)
	require.NoError(t, err)

	var owner models.User
	require.NoError(t, db.DB.Where("username = ?", "admin").First(&owner).Error)
	key := &models.APIKey{UserID: owner.ID, Name: "kitchen tablet"}
	rawKey, err := key.Generate()
	require.NoError(t, err)
	require.NoError(t, db.DB.Create(key).Error)

	path := "/api/controls/" + ctl.ID.String() + "/set"
	require.Equal(t, http.StatusOK, postWith(t, ts, admin, path, map[string]any{"value": true}, nil))
	require.Equal(t, http.StatusOK, postWith(t, ts, rawKey, path, map[string]any{"value": false}, nil))
	require.Equal(t, http.StatusOK, postWith(t, ts, admin, path, map[string]any{"value": true},
		map[string]string{middleware.DashboardHeader: "hallway"}))

	var got []models.Event
	for _, e := range read() {
		if e.SubjectID == ctl.ID.String() {
			got = append(got, e)
		}
	}
	require.Len(t, got, 3, "each write is one entry")
	assert.Equal(t, "Event test switch set to on", got[0].Message)
	assert.Equal(t, events.ActorUser, got[0].ActorKind)
	assert.Equal(t, "admin", got[0].ActorName)
	assert.Equal(t, events.ViaAPI, got[0].Via)

	assert.Equal(t, events.ActorAPIKey, got[1].ActorKind)
	assert.Equal(t, "kitchen tablet (admin)", got[1].ActorName)
	assert.Equal(t, "Event test switch set to off", got[1].Message)

	assert.Equal(t, events.ViaDashboard, got[2].Via)
	assert.Equal(t, "hallway", got[2].DashboardID)
	assert.Equal(t, map[string]any{"old": false, "new": true}, got[2].Details)
}

// TestExcludedControlIsNotLogged covers the per-control exclusion.
func TestExcludedControlIsNotLogged(t *testing.T) {
	ts, admin := setupTestServer(t)
	read := recordEvents(t)
	ctl, err := controls.S.Create("Event test mirror", controls.KindBoolean, "", nil, false)
	require.NoError(t, err)
	s := events.Defaults()
	s.ExcludedControls = []string{ctl.ID.String()}
	_, err = events.SaveSettings(s)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = events.SaveSettings(events.Defaults()) })

	require.Equal(t, http.StatusOK, postWith(t, ts, admin, "/api/controls/"+ctl.ID.String()+"/set", map[string]any{"value": true}, nil))
	for _, e := range read() {
		assert.NotEqual(t, ctl.ID.String(), e.SubjectID, "an excluded control is not logged")
	}
}

// TestFlowLifecycleNamesItsActor covers US3 scenario 3: deploying, enabling,
// and disabling a flow are recorded with who did it.
func TestFlowLifecycleNamesItsActor(t *testing.T) {
	ts, admin := setupTestServer(t)
	read := recordEvents(t)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/flows", map[string]any{"name": "Event test flow"})
	require.Equal(t, http.StatusCreated, status)
	id := resp["data"].(map[string]any)["id"].(string)
	status, _ = doJSON(t, ts, admin, http.MethodPut, "/api/flows/"+id+"/graph", map[string]any{"graph": validGraph()})
	require.Equal(t, http.StatusOK, status)
	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/flows/"+id+"/deploy", map[string]any{"comment": "first"})
	require.Equal(t, http.StatusOK, status)
	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/flows/"+id+"/enable", nil)
	require.Equal(t, http.StatusOK, status)
	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/flows/"+id+"/disable", nil)
	require.Equal(t, http.StatusOK, status)

	var got []string
	for _, e := range read() {
		if e.SubjectID == id {
			got = append(got, e.Message)
			assert.Equal(t, "admin", e.ActorName)
			assert.Equal(t, models.EventFlow, e.Category)
		}
	}
	assert.Equal(t, []string{"Deployed Event test flow (version 1)", "Enabled Event test flow", "Disabled Event test flow"}, got)
}
