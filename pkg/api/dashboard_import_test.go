package api_test

import (
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDashboardImportKeepsZeroSpacing locks the export and import round trip
// for a dashboard drawn edge to edge. The spacing columns default to 8, 8, and
// 4 pixels, and an insert treats a zero as unset and takes the default, so a
// full-screen dashboard exported with no padding, gap, or widget padding came
// back with all three. A document that names no spacing still gets the
// defaults.
func TestDashboardImportKeepsZeroSpacing(t *testing.T) {
	ts, bearer := setupTestServer(t)

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Full screen"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	id, _ := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+id, map[string]any{"padding": 0, "gap": 0, "widget_padding": 0})
	require.Equalf(t, http.StatusOK, status, "the spacing must be set to zero: %v", resp)
	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/dashboards/"+id+"/export", nil)
	require.Equalf(t, http.StatusOK, status, "the dashboard must export: %v", resp)
	exported := resp["data"].(map[string]any)

	cases := []struct {
		name                        string
		document                    map[string]any
		padding, gap, widgetPadding float64
		why                         string
	}{
		{
			name: "an exported edge-to-edge dashboard", document: exported,
			padding: 0, gap: 0, widgetPadding: 0,
			why: "a zero the document states must be stored, not replaced by the column default",
		},
		{
			name:     "a document that names no spacing",
			document: map[string]any{"karakuri_dashboard": 1, "name": "Bare", "grid": map[string]any{"rows": 12, "cols": 12}, "widgets": []any{}},
			padding:  8, gap: 8, widgetPadding: 4,
			why: "spacing the document leaves out falls back to the defaults a new dashboard has",
		},
	}
	for _, c := range cases {
		status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/import", map[string]any{"document": c.document})
		require.Equalf(t, http.StatusCreated, status, "%s: the document must import: %v", c.name, resp)
		imported := resp["data"].(map[string]any)
		assert.Equalf(t, c.padding, imported["padding"], "%s: outer padding: %s", c.name, c.why)
		assert.Equalf(t, c.gap, imported["gap"], "%s: gap: %s", c.name, c.why)
		assert.Equalf(t, c.widgetPadding, imported["widget_padding"], "%s: widget padding: %s", c.name, c.why)
	}
}

// TestDashboardImportSlugIgnoresPrivateDashboards locks the rule that a slug
// confirms nothing private. Creating a dashboard refuses a slug only when the
// creator can see a dashboard using it, but an import picked its slug by
// counting every dashboard, so the "-imported" suffix told the importer that
// somebody's private dashboard already had that address.
func TestDashboardImportSlugIgnoresPrivateDashboards(t *testing.T) {
	ts, bearer := setupTestServer(t)

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Kitchen"})
	require.Equalf(t, http.StatusCreated, status, "the private dashboard must be created: %v", resp)

	stranger := sessionWith(t, "stranger", permissions.PermDashboardsRead, permissions.PermDashboardsWrite)
	doc := map[string]any{"karakuri_dashboard": 1, "name": "Kitchen", "grid": map[string]any{"rows": 12, "cols": 12}, "widgets": []any{}}
	status, resp = doJSON(t, ts, stranger, http.MethodPost, "/api/dashboards/import", map[string]any{"document": doc})
	require.Equalf(t, http.StatusCreated, status, "the document must import: %v", resp)
	assert.Equal(t, "kitchen", resp["data"].(map[string]any)["slug"],
		"a slug used only by a dashboard the importer cannot see must be given as asked, as creating one does")

	status, resp = doJSON(t, ts, stranger, http.MethodPost, "/api/dashboards/import", map[string]any{"document": doc})
	require.Equalf(t, http.StatusCreated, status, "the second import must succeed: %v", resp)
	assert.Equal(t, "kitchen-imported", resp["data"].(map[string]any)["slug"],
		"a slug the importer can already see in use still takes a suffix")
}
