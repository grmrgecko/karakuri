package dashops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/widgets"
)

// ValidationIssue identifies a stored layout or configuration problem.
type ValidationIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// issueMessage returns an error's message without its API error code, which
// the issue's own code already carries.
func issueMessage(err error) string {
	var appErr *apperrors.AppError
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}

// ValidateSnapshot checks a layout without mutating it or contacting devices.
func ValidateSnapshot(s *models.DashboardSnapshot, rows []models.DashboardWidget) []ValidationIssue {
	issues := []ValidationIssue{}
	add := func(severity, code, path, message string) {
		issues = append(issues, ValidationIssue{severity, code, path, message})
	}
	if !models.ValidDashboardLayout(s.LayoutMode) || s.GridCols < 1 || s.GridRows < 1 || s.RowHeight < 1 || s.Padding < 0 || s.Gap < 0 || s.WidgetPadding < 0 {
		add("error", "geometry", "layout", "grid dimensions and row height must be positive; spacing cannot be negative")
	}
	if err := ValidateDisplay(s.Display); err != nil {
		add("error", "invalid_display", "display", issueMessage(err))
	}
	sections := map[string]bool{}
	for _, section := range s.Sections {
		sections[section.ID.String()] = true
	}
	pages := map[string]bool{}
	for _, page := range s.Pages {
		pages[page.ID.String()] = true
	}
	regions := s.LayoutMode == models.DashboardLayoutRegions
	var variables []models.DashboardVariable
	defined := map[string]bool{}
	if s.Display != nil {
		variables = s.Display.Variables
		for _, f := range variables {
			defined[f.Name] = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, v := range variables {
		if v.Kind() != models.VariableTypeQuery {
			continue
		}
		path := fmt.Sprintf("display/variables/%d", i)
		r := dashvars.Query(ctx, v.Query)
		switch {
		case r.Err != nil:
			add("warning", "variable_query_failed", path, fmt.Sprintf("query for %q: %s; it has no options until it resolves", v.Name, r.Err))
		case len(r.Options) == 0:
			add("warning", "variable_query_empty", path, fmt.Sprintf("query for %q returns no options", v.Name))
		case r.Truncated:
			add("warning", "variable_query_truncated", path, fmt.Sprintf("query for %q returns more than %d options; the rest are dropped", v.Name, dashvars.MaxQueryOptions))
		}
	}
	variables, _ = dashvars.Resolve(variables)
	// A templated config is checked as viewers first receive it, with each
	// variable at its default, so "hours": "${range}" validates as a number.
	defaults := dashscope.DefaultValues(variables)
	for i, w := range rows {
		path := "widgets/" + w.ID.String()
		refs := dashscope.PlacementReferences(w)
		names := make([]string, 0, len(refs))
		for name := range refs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !defined[name] {
				add("warning", "variable_undefined", path, fmt.Sprintf("refers to variable %q, which the dashboard does not define; the reference stays as written", name))
			}
		}
		if _, ok := dashscope.VariableCombinations(variables, refs); !ok {
			add("error", "variable_combinations", path, fmt.Sprintf("its variables combine into more than %d values, so it grants none of its bindings", dashscope.MaxVariableCombinations))
		}
		if w.PageID != nil && !pages[w.PageID.String()] {
			add("error", "page_missing", path, "widget page does not exist on this dashboard")
		}
		if err := ValidateWidgetDisplay(w.Display, true); err != nil {
			add("error", "invalid_display", path, issueMessage(err))
		}
		if w.Region != "" && !models.ValidDashboardRegion(w.Region) {
			add("error", "unknown_region", path, "widget region is not a region the layout draws")
		}
		if regions {
			// The region layout stacks placements and ignores columns and
			// sections, so grid bounds and overlaps do not apply.
			if w.Width < 1 || w.Height < 1 || w.Width > s.GridCols {
				add("error", "out_of_bounds", path, "widget width must be 1 to grid_cols and height positive")
			}
		}
		if w.SectionID != nil && !sections[w.SectionID.String()] {
			add("error", "section_missing", path, "widget section does not exist")
		}
		if s.LayoutMode == models.DashboardLayoutFit && w.SectionID != nil {
			add("error", "section_in_fit", path, "fit layouts cannot contain section widgets")
		}
		if !regions && (w.X < 0 || w.Y < 0 || w.Width < 1 || w.Height < 1 || w.X > s.GridCols-w.Width || (s.LayoutMode == models.DashboardLayoutFit && w.Y > s.GridRows-w.Height)) {
			add("error", "out_of_bounds", path, "widget position or size is outside the grid")
		}
		for _, other := range rows[:i] {
			if regions {
				break
			}
			samePage := w.PageID == nil || other.PageID == nil || *w.PageID == *other.PageID
			if !samePage {
				continue
			}
			sameSection := (w.SectionID == nil && other.SectionID == nil) || (w.SectionID != nil && other.SectionID != nil && *w.SectionID == *other.SectionID)
			if sameSection && w.X < other.X+other.Width && other.X < w.X+w.Width && w.Y < other.Y+other.Height && other.Y < w.Y+w.Height {
				add("warning", "overlap", path, "overlaps widget "+other.ID.String())
			}
		}
		if _, ok := widgets.Available(w.Widget.PackageName); !ok {
			add("error", "package_unavailable", path, "widget package is not loaded")
		}
		var config map[string]any
		raw := dashscope.SubstituteConfig(w.Config, defaults)
		if raw == "" {
			raw = "{}"
		}
		if err := json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
			add("error", "invalid_config", path, "widget config must be a JSON object")
			continue
		}
		schemaJSON := w.Widget.ConfigSchema
		if schemaJSON == "" || schemaJSON == "{}" || schemaJSON == "null" {
			add("warning", "schema_unavailable", path, "widget has no config schema; inspect its documentation and rendered output")
			continue
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
			add("warning", "invalid_schema", path, "widget package config schema could not be parsed")
			continue
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			add("warning", "invalid_schema", path, "widget package config schema could not be resolved locally")
			continue
		}
		effective := map[string]any{}
		if w.Widget.DefaultConfig != "" {
			if err := json.Unmarshal([]byte(w.Widget.DefaultConfig), &effective); err != nil {
				add("warning", "invalid_defaults", path, "widget package defaults could not be parsed")
			}
		}
		if effective == nil {
			effective = map[string]any{}
		}
		for key, value := range config {
			effective[key] = value
		}
		if err := resolved.Validate(effective); err != nil {
			add("error", "invalid_config", path, fmt.Sprintf("widget config: %s", err))
		}
	}
	return issues
}
