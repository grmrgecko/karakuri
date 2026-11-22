package mcpserver

import (
	"context"
	"fmt"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

func addDashboardValidationTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "validate_dashboard", Description: "Check draft or deployed dashboard geometry, overlaps, widget package schemas, and binding dependencies. Reports errors and warnings without editing, rendering, or operating devices. Packages without schemas are explicitly reported."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID   string `json:"id" jsonschema:"Dashboard UUID or slug."`
			View string `json:"view,omitempty" jsonschema:"draft (default) or deployed."`
		}) (*mcp.CallToolResult, any, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if in.View == "" {
				in.View = "draft"
			}
			if in.View != "draft" && in.View != "deployed" {
				return nil, nil, fmt.Errorf("view must be draft or deployed")
			}
			d, err := editableDashboard(ctx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			snap, err := dashboardSnapshot(d, in.View)
			if err != nil {
				return nil, nil, err
			}
			if snap == nil {
				return nil, nil, fmt.Errorf("dashboard has no deployment")
			}
			rows, err := snap.WidgetRows(d.ID)
			if err != nil {
				return nil, nil, err
			}
			issues := dashops.ValidateSnapshot(snap, rows)
			c := newDependencyCollector(ctx)
			c.dashboard(rows, snap.Display)
			for _, dep := range c.report.Dependencies {
				if dep.Status == "available" {
					continue
				}
				severity := "warning"
				if dep.Status == "missing" || dep.Status == "invalid" {
					severity = "error"
				}
				issues = append(issues, dashops.ValidationIssue{Severity: severity, Code: "dependency_" + dep.Status, Path: dep.Path, Message: dep.Kind + " " + dep.Ref + ": " + dep.Detail})
			}
			valid := true
			for _, issue := range issues {
				if issue.Severity == "error" {
					valid = false
				}
			}
			return nil, map[string]any{"valid": valid, "issues": issues, "dependencies": c.report.Dependencies, "truncated": c.report.Truncated}, nil
		})
}
