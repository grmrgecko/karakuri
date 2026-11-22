package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addReferenceTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "find_references", Description: "Find accessible flows and editable dashboards referencing a connection, control, MQTT device, shared flow, HA entity, or agent. Includes deployed shared subflows. Pages scan source documents, not matches; continue while has_more is true."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Kind  string `json:"kind" jsonschema:"connection, control, mqtt_device, flow, ha_entity, or agent."`
			Ref   string `json:"ref" jsonschema:"Referenced UUID; controls also accept a name. HA entities use an entity ID."`
			Owner string `json:"owner,omitempty" jsonschema:"Required connection UUID for an HA entity."`
			View  string `json:"view,omitempty" jsonschema:"draft (default) or deployed source documents."`
			Page  int    `json:"page,omitempty" jsonschema:"Source document page, starting at 1; scans up to 50 flows and 50 dashboards."`
		}) (*mcp.CallToolResult, any, error) {
			uid, err := userID(ctx)
			if err != nil {
				return nil, nil, err
			}
			permission := map[string]string{"connection": perm.PermConnectionsRead, "control": perm.PermControlsRead, "mqtt_device": perm.PermConnectionsRead, "flow": perm.PermFlowsRead, "ha_entity": perm.PermConnectionsRead, "agent": perm.PermAgentsRead}[in.Kind]
			if permission == "" || in.Ref == "" {
				return nil, nil, fmt.Errorf("kind must name a supported reference and ref must not be empty")
			}
			if err := requirePermission(ctx, permission); err != nil {
				return nil, nil, err
			}
			if in.Kind == "ha_entity" && in.Owner == "" {
				return nil, nil, fmt.Errorf("owner is required for an HA entity")
			}
			// Where a connection, device, entity, or agent is used is itself
			// about that resource, so the caller must be able to use it.
			if err := referenceAccess(ctx, in.Kind, in.Ref, in.Owner); err != nil {
				return nil, nil, err
			}
			if in.View == "" {
				in.View = "draft"
			}
			if in.View != "draft" && in.View != "deployed" {
				return nil, nil, fmt.Errorf("view must be draft or deployed")
			}
			if in.Page == 0 {
				in.Page = 1
			}
			if in.Page < 1 || in.Page > 1000000 {
				return nil, nil, fmt.Errorf("page must be between 1 and 1000000")
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			aliases := map[string]bool{in.Ref: true}
			if in.Kind == "control" {
				var control models.Control
				if err := db.DB.WithContext(ctx).Where("id = ? OR name = ?", in.Ref, in.Ref).First(&control).Error; err == nil {
					aliases[control.ID.String()] = true
					aliases[control.Name] = true
				}
			}
			matches := []map[string]any{}
			issues := []map[string]any{}
			incomplete := false
			hasMore := false
			collect := func(kind, id, name string, c *dependencyCollector, err error) {
				if err != nil {
					incomplete = true
					issues = append(issues, map[string]any{"kind": kind, "id": id, "error": "could not inspect document"})
					return
				}
				if c.report.Truncated {
					incomplete = true
				}
				for _, dep := range c.report.Dependencies {
					if dep.Status == "dynamic" || dep.Status == "unchecked" || dep.Kind == "node_type" || dep.Kind == "widget_package" || dep.Kind == "binding" {
						incomplete = true
					}
					if dep.Kind == in.Kind && aliases[dep.Ref] && (in.Owner == "" || dep.Owner == in.Owner) {
						if len(matches) >= 2000 {
							incomplete = true
							return
						}
						matches = append(matches, map[string]any{"document_kind": kind, "document_id": id, "document_name": name, "path": dep.Path, "ref": dep.Ref, "owner": dep.Owner})
					}
				}
			}
			flowAccess := requirePermission(ctx, perm.PermFlowsRead) == nil
			if flowAccess {
				var flows []models.Flow
				q := db.DB.WithContext(ctx).Order("id").Offset((in.Page - 1) * 50).Limit(51)
				readable, err := flowops.ReadableFlowIDs(ctx, uid)
				if err != nil {
					return nil, nil, err
				}
				if readable != nil {
					ids := make([]uuid.UUID, 0, len(readable))
					for id := range readable {
						ids = append(ids, id)
					}
					q = q.Where("id IN ?", ids)
				}
				if in.View == "deployed" {
					q = q.Where("deployed_version_id IS NOT NULL")
				}
				if err := q.Find(&flows).Error; err != nil {
					return nil, nil, err
				}
				if len(flows) > 50 {
					hasMore = true
					flows = flows[:50]
				}
				for i := range flows {
					f := &flows[i]
					c := newDependencyCollector(ctx)
					c.referencesOnly = true
					err := c.flowDocument(f, in.View, "flow/"+f.ID.String(), 0)
					collect("flow", f.ID.String(), f.Name, c, err)
				}
			}
			groups, err := models.GetUserGroupIDs(uid)
			if err != nil {
				return nil, nil, err
			}
			var dashboards []*models.Dashboard
			var total int64
			if requirePermission(ctx, perm.PermDashboardsManage) == nil {
				dashboards, total, err = models.ListDashboards(in.Page, 50)
			} else {
				dashboards, total, err = models.ListAccessibleDashboards(uid, groups, in.Page, 50)
			}
			if err != nil {
				return nil, nil, err
			}
			hasMore = hasMore || int64(in.Page*50) < total
			for _, d := range dashboards {
				if !dashops.CanEdit(ctx, uid, d) || in.View == "deployed" && d.DeployedVersion == 0 {
					continue
				}
				c := newDependencyCollector(ctx)
				c.referencesOnly = true
				snap, err := dashboardSnapshot(d, in.View)
				if err == nil && snap != nil {
					var rows []models.DashboardWidget
					rows, err = snap.WidgetRows(d.ID)
					if err == nil {
						c.dashboard(rows, snap.Display)
					}
				}
				collect("dashboard", d.ID.String(), d.Name, c, err)
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"references": matches, "issues": issues, "incomplete": incomplete, "has_more": hasMore, "next_page": in.Page + 1, "searched_flows": flowAccess, "dashboard_scope": "editable"}, nil
		})
}
