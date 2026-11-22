package energyops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// chartPackage is the chart widget the preset places.
const chartPackage = "chart"

// presetQuery is one energy query of a preset chart.
func presetQuery(ref string, targets []string, measure, period string) map[string]any {
	return map[string]any{"refId": ref, "connectionId": "energy", "language": "energy", "text": "",
		"energy": map[string]any{"targets": targets, "measure": measure, "period": period}, "format": "time_series"}
}

// presetChart is one chart placement.
type presetChart struct {
	x, y, w, h int
	config     map[string]any
}

// stat is a summary panel: usage and total cost over a range.
func stat(title, timeRange, currency string) map[string]any {
	return map[string]any{
		"viz": "stat", "timeRange": timeRange, "refresh": "1m",
		"queries": []any{presetQuery("A", []string{"house"}, "usage", "day"), presetQuery("B", []string{"house"}, "total_cost", "day")},
		"fieldConfig": map[string]any{"defaults": map[string]any{"decimals": 2}, "overrides": []any{
			override("A", title+" kWh", "kwatth"),
			override("B", title+" cost", "currency"+currency),
		}},
		"transformations": []any{},
		"options":         map[string]any{"reduceOptions": map[string]any{"calcs": []string{"sum"}}, "graphMode": "none", "textMode": "value_and_name"},
	}
}

func override(ref, name, unit string) map[string]any {
	props := []any{map[string]any{"id": "displayName", "value": name}}
	if unit != "" && unit != "currency" {
		props = append(props, map[string]any{"id": "unit", "value": unit})
	}
	return map[string]any{"matcher": map[string]any{"id": "byFrameRefID", "options": ref}, "properties": props}
}

// CreatePreset creates the energy dashboard for a user: today, this week,
// and this month, 30 days of history, a breakdown by group and source with
// untracked usage, and solar against the grid when solar sources exist.
func CreatePreset(ctx context.Context, userID uuid.UUID) (*models.Dashboard, error) {
	var widget models.Widget
	if err := db.DB.Where("package_name = ?", chartPackage).First(&widget).Error; err != nil {
		return nil, apperrors.ErrValidation.WithMessage("the chart widget is not installed")
	}
	sources, err := models.ListEnergySources()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	groups, err := models.ListEnergyGroups()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	tariffs, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	currency := ""
	if len(tariffs) > 0 {
		currency = tariffs[len(tariffs)-1].Currency
	}
	solar := false
	var consumers []string
	for _, s := range sources {
		if s.Role == models.EnergyRoleProduction {
			solar = true
		}
		if s.Role == models.EnergyRoleConsumer {
			consumers = append(consumers, "source:"+s.ID.String())
		}
	}
	breakdown := []string{}
	for _, g := range groups {
		breakdown = append(breakdown, "group:"+g.ID.String())
	}
	if len(breakdown) == 0 {
		breakdown = append(breakdown, consumers...)
	}
	breakdown = append(breakdown, "untracked")

	charts := []presetChart{
		{0, 0, 4, 3, stat("Today", "now/d,now", currency)},
		{4, 0, 4, 3, stat("This week", "now/w,now", currency)},
		{8, 0, 4, 3, stat("This month", "now/M,now", currency)},
		{0, 3, 8, 5, map[string]any{
			"viz": "barchart", "timeRange": "now-29d/d,now", "refresh": "5m",
			"queries":         []any{presetQuery("A", []string{"house"}, "usage", "day")},
			"fieldConfig":     map[string]any{"defaults": map[string]any{"unit": "kwatth", "decimals": 1, "displayName": "Daily use"}, "overrides": []any{}},
			"transformations": []any{}, "options": map[string]any{"legend": map[string]any{"showLegend": false}, "showValue": "never"},
		}},
		{8, 3, 4, 5, map[string]any{
			"viz": "piechart", "timeRange": "now/M,now", "refresh": "5m",
			"queries":         []any{presetQuery("A", breakdown, "usage", "day")},
			"fieldConfig":     map[string]any{"defaults": map[string]any{"unit": "kwatth", "decimals": 1}, "overrides": []any{}},
			"transformations": []any{},
			"options": map[string]any{"reduceOptions": map[string]any{"calcs": []string{"sum"}}, "pieType": "donut",
				"legend": map[string]any{"showLegend": true, "values": []string{"value", "percent"}}},
		}},
	}
	if len(consumers) > 0 {
		q := presetQuery("A", append(consumers, "untracked"), "usage", "month")
		q["format"] = "table"
		charts = append(charts, presetChart{0, 8, 6, 5, map[string]any{
			"viz": "table", "timeRange": "now/M,now", "refresh": "5m", "queries": []any{q},
			"fieldConfig":     map[string]any{"defaults": map[string]any{"unit": "kwatth", "decimals": 2}, "overrides": []any{}},
			"transformations": []any{}, "options": map[string]any{},
		}})
	}
	if solar {
		charts = append(charts, presetChart{6, 8, 6, 5, map[string]any{
			"viz": "timeseries", "timeRange": "now-7d,now", "refresh": "5m",
			"queries": []any{
				presetQuery("A", []string{"house"}, "production", "hour"),
				presetQuery("B", []string{"house"}, "import", "hour"),
				presetQuery("C", []string{"house"}, "export", "hour"),
				presetQuery("D", []string{"house"}, "self_consumption", "hour"),
			},
			"fieldConfig": map[string]any{"defaults": map[string]any{"unit": "kwatth", "decimals": 2}, "overrides": []any{
				override("A", "Solar production", ""), override("B", "Grid import", ""),
				override("C", "Grid export", ""), override("D", "Solar used at home", ""),
			}},
			"transformations": []any{}, "options": map[string]any{},
		}})
	}

	// The first of energy, energy-2, energy-3, and so on that the user does
	// not already see in use.
	slug := "energy"
	for i := 2; dashops.SlugVisible(ctx, userID, slug); i++ {
		slug = fmt.Sprintf("energy-%d", i)
	}
	dashboard, err := dashops.Create(ctx, dashops.CreateSpec{Name: "Energy", Slug: slug, Description: "Electricity use, cost, and solar.",
		LayoutMode: "scroll", GridRows: 13, GridCols: 12}, userID)
	if err != nil {
		return nil, err
	}
	for _, c := range charts {
		raw, err := json.Marshal(c.config)
		if err != nil {
			return nil, apperrors.ErrInternal.WithError(err)
		}
		if _, err := dashops.AddWidget(ctx, dashboard, dashops.WidgetSpec{WidgetID: widget.ID, X: c.x, Y: c.y, Width: c.w, Height: c.h,
			Config: string(raw), UserID: userID}); err != nil {
			return nil, fmt.Errorf("placing a preset chart: %w", err)
		}
	}
	return dashboard, nil
}
