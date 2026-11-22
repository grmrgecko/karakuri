package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotWithoutDisplaySettingsMatchesEarlierDeployments pins the
// upgrade contract of feature 023: a dashboard that uses no pages, regions,
// or display settings must snapshot byte-for-byte as builds before them did,
// or every deployed dashboard reports draft changes after the upgrade. The
// stored string is the shape those builds wrote. An empty display object
// written by a client counts as none.
func TestSnapshotWithoutDisplaySettingsMatchesEarlierDeployments(t *testing.T) {
	widgetID := uuid.MustParse("6f1c1b8e-0000-4000-8000-000000000001")
	d := &Dashboard{Name: "Hall", LayoutMode: DashboardLayoutFit, GridRows: 12, GridCols: 12, RowHeight: 80, Padding: 8, Gap: 8, WidgetPadding: 4}
	widgets := []DashboardWidget{{ID: widgetID, WidgetID: 3, X: 0, Y: 0, Width: 4, Height: 2, Config: `{"format":"24h"}`, Display: &WidgetDisplay{}}}

	got, err := SnapshotDashboard(d, widgets, nil, nil).JSON()
	require.NoError(t, err)
	stored := `{"presentation":{"name":"Hall","description":"","theme_id":null},"layout_mode":"fit","grid_rows":12,"grid_cols":12,"row_height":80,"padding":8,"gap":8,"widget_padding":4,"widgets":[{"id":"6f1c1b8e-0000-4000-8000-000000000001","widget_id":3,"x":0,"y":0,"width":4,"height":2,"config":"{\"format\":\"24h\"}"}]}`
	assert.Equal(t, stored, got, "a dashboard without the new settings must serialize like an earlier deployment")

	parsed, err := ParseDashboardSnapshot(stored)
	require.NoError(t, err)
	again, err := parsed.JSON()
	require.NoError(t, err)
	assert.Equal(t, stored, again, "an earlier snapshot must re-encode unchanged")
}

// TestSnapshotRoundTripsDisplaySettings checks that pages, regions, and
// display settings survive storage, since a restore rebuilds the draft from
// exactly this encoding.
func TestSnapshotRoundTripsDisplaySettings(t *testing.T) {
	pageID := uuid.New()
	d := &Dashboard{Name: "Mirror", LayoutMode: DashboardLayoutRegions, GridRows: 12, GridCols: 12, RowHeight: 60,
		Display: &DashboardDisplay{PageInterval: 20, NightDim: &DimWindow{From: "23:00", To: "07:00", Level: 80}, PixelShift: 4}}
	widgets := []DashboardWidget{{
		ID: uuid.New(), WidgetID: 1, Width: 12, Height: 2, PageID: &pageID, Region: "top_left",
		Display: &WidgetDisplay{Name: "Garage", Hidden: true, Conditions: []Condition{
			{Kind: ConditionState, Source: "ha", ConnectionID: "c1", EntityID: "binary_sensor.garage", Operator: "equals", Value: "on"},
			{Kind: ConditionTime, From: "22:00", To: "06:00", Days: []int{1, 2}},
		}, AnimateIn: "slide-up", Duration: 600},
	}}
	pages := []DashboardPage{{ID: pageID, Title: "Today", SortOrder: 0}}

	snap := SnapshotDashboard(d, widgets, nil, pages)
	stored, err := snap.JSON()
	require.NoError(t, err)
	parsed, err := ParseDashboardSnapshot(stored)
	require.NoError(t, err)

	assert.Equal(t, snap.Display, parsed.Display, "display settings must survive storage")
	assert.Equal(t, snap.Pages, parsed.Pages, "pages must survive storage")
	assert.Equal(t, snap.Widgets, parsed.Widgets, "placement page, region, and display must survive storage")
}
