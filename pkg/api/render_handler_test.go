package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/render"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newRenderFixture stores a private dashboard and a render job for it, and
// returns the dashboard and the raw token that job's headless browser would
// carry. Only the digest is stored, exactly as the render engine stores it.
func newRenderFixture(t *testing.T, slug, rawToken string, status string) *models.Dashboard {
	t.Helper()

	var owner models.User
	require.NoError(t, db.DB.Where("username = ?", "admin").First(&owner).Error,
		"the fixture dashboard needs the seeded admin as its owner")

	dashboard := &models.Dashboard{
		ID: uuid.New(), Name: slug, Slug: slug, OwnerID: owner.ID,
		LayoutMode: models.DashboardLayoutFit, GridRows: 12, GridCols: 12,
	}
	require.NoError(t, db.DB.Create(dashboard).Error, "the fixture dashboard must be stored")

	if rawToken != "" {
		job := &models.RenderJob{
			ID:              uuid.New(),
			DashboardID:     dashboard.ID,
			TokenHash:       models.HashRenderToken("result-" + rawToken),
			RenderTokenHash: models.HashRenderToken(rawToken),
			Status:          status,
			Width:           800,
			Height:          600,
			Format:          "png",
			ExpiresAt:       time.Now().Add(time.Hour),
		}
		require.NoError(t, models.CreateRenderJob(job), "the fixture render job must be stored")
	}
	return dashboard
}

// TestRenderTokenViewsOneDashboardWhileItRuns pins the credential the headless
// browser carries. It is not an account: it opens exactly the dashboard its
// job names, for as long as that job is being worked on, and nothing else on
// the instance answers it.
//
// Handing the browser the dashboard owner's session instead, which is how this
// is usually done, would put the owner's whole API behind every render of a
// dashboard they shared. A render is a viewer, so its token grants what a
// viewer of one dashboard has.
func TestRenderTokenViewsOneDashboardWhileItRuns(t *testing.T) {
	srv, _ := setupTestServer(t)

	const token = "1c1cbb0f5a3f4b0f9d1f6bb3f4a3d2c1"
	target := newRenderFixture(t, "render-target", token, models.RenderStatusProcessing)
	other := newRenderFixture(t, "render-bystander", "", "")

	status, _ := doAnon(t, srv, "", http.MethodGet, "/api/dashboards/"+target.Slug, nil)
	assert.Equal(t, http.StatusNotFound, status,
		"a private dashboard stays invisible to a caller with no credential at all")

	status, _ = doAnon(t, srv, "", http.MethodGet, "/api/dashboards/"+target.Slug+"?token="+token, nil)
	assert.Equal(t, http.StatusOK, status,
		"the render token opens the dashboard its own job names")

	status, _ = doAnon(t, srv, "", http.MethodGet, "/api/dashboards/"+other.Slug+"?token="+token, nil)
	assert.Equal(t, http.StatusNotFound, status,
		"one dashboard's render token must not reach another private dashboard")

	status, _ = doAnon(t, srv, "", http.MethodGet, "/api/flows?token="+token, nil)
	assert.Equal(t, http.StatusUnauthorized, status,
		"a render token is not an account, so a permission-gated endpoint refuses it")

	_, err := dashops.Deploy(context.Background(), target, "published", target.OwnerID)
	require.NoError(t, err)
	require.NoError(t, db.DB.Model(target).Update("grid_cols", 4).Error)
	status, response := doAnon(t, srv, "", http.MethodGet, "/api/dashboards/"+target.Slug+"?view=draft&token="+token, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(12), response["data"].(map[string]any)["grid_cols"], "a published render token must not read the draft even when requested")
	require.NoError(t, db.DB.Model(&models.RenderJob{}).Where("dashboard_id = ?", target.ID).Update("draft", true).Error)
	status, response = doAnon(t, srv, "", http.MethodGet, "/api/dashboards/"+target.Slug+"?view=draft&token="+token, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(4), response["data"].(map[string]any)["grid_cols"], "an editor preview must photograph the draft without publishing it")
	status, _ = doAnon(t, srv, "", http.MethodPatch, "/api/dashboards/"+target.Slug+"?token="+token, map[string]any{"grid_cols": 8})
	assert.NotEqual(t, http.StatusOK, status, "draft render credentials must remain read-only")

	// Settle the job the way a finished render does. The grant is the job:
	// once it stops running, the token stops opening anything.
	require.NoError(t, db.DB.Model(&models.RenderJob{}).
		Where("dashboard_id = ?", target.ID).
		Update("status", models.RenderStatusCompleted).Error,
		"the fixture job must settle")

	status, _ = doAnon(t, srv, "", http.MethodGet, "/api/dashboards/"+target.Slug+"?token="+token, nil)
	assert.Equal(t, http.StatusNotFound, status,
		"a finished job's render token grants nothing")
}

// TestRenderURLSpecs pins the public render address, which is a format rather
// than an API call: an <img> tag, a wiki page, or a photo frame is what points
// at it, so "{width}x{height}.{format}" has to keep meaning what it means.
//
// The render engine is not running in this test, so a spec that parses gets as
// far as asking for a render and is told the subsystem is off. That is what
// separates a rejected address from an accepted one here.
func TestRenderURLSpecs(t *testing.T) {
	srv, _ := setupTestServer(t)
	dashboard := newRenderFixture(t, "render-specs", "", "")
	require.NoError(t, db.DB.Model(dashboard).Update("is_public", true).Error,
		"the render URL is checked as a viewer would reach it")

	cases := []struct {
		name string
		spec string
		want int
		why  string
	}{
		{"accepted", "1920x1080.png", http.StatusServiceUnavailable, "a well-formed spec reaches the render engine"},
		{"smallest accepted", "100x100.jpg", http.StatusServiceUnavailable, "100 pixels is the floor, not below it"},
		{"largest accepted", "7680x7680.pdf", http.StatusServiceUnavailable, "7680 is the ceiling, not above it"},
		{"no extension", "1920x1080", http.StatusBadRequest, "the extension is what names the format"},
		{"unknown format", "1920x1080.webp", http.StatusBadRequest, "an unsupported format is refused rather than substituted"},
		{"no dimensions", "wide.png", http.StatusBadRequest, "the size is required"},
		{"width too small", "99x1080.png", http.StatusBadRequest, "below the floor is refused"},
		{"height too large", "1920x7681.png", http.StatusBadRequest, "above the ceiling is refused"},
	}

	for _, tc := range cases {
		resp, err := srv.Client().Get(srv.URL + "/dashboard/" + dashboard.Slug + "/render/" + tc.spec)
		require.NoErrorf(t, err, "the request for %s must reach the server", tc.spec)
		_ = resp.Body.Close()
		assert.Equalf(t, tc.want, resp.StatusCode, "%s: %s", tc.spec, tc.why)
	}
}

// TestRenderScheduleCreatedDisabledStaysDisabled locks the enabled flag a new
// render schedule carries. The column defaults to enabled, and an insert
// treats a false as unset and takes the default, so a schedule created
// disabled was stored enabled and fired on its next cron tick.
func TestRenderScheduleCreatedDisabledStaysDisabled(t *testing.T) {
	srv, token := setupTestServer(t)
	render.InitScheduler(zap.NewNop())
	dashboard := newRenderFixture(t, "scheduled", "", "")

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/dashboards/"+dashboard.ID.String()+"/render_schedules",
		map[string]any{"cron_expr": "0 3 * * *", "enabled": false})
	require.Equalf(t, http.StatusCreated, status, "the schedule must be created: %v", resp)
	data, _ := resp["data"].(map[string]any)
	assert.Equal(t, false, data["enabled"], "the response must report the schedule as disabled, as it was asked for")

	schedules, err := models.ListEnabledRenderSchedules()
	require.NoError(t, err, "the enabled schedules must be readable")
	assert.Empty(t, schedules, "a schedule created disabled must not be among those the scheduler arms")
}
