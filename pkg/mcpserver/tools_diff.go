package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type diffInput struct {
	ID   string `json:"id" jsonschema:"Document UUID; dashboards also accept a slug."`
	From string `json:"from,omitempty" jsonschema:"Source: deployed (default), draft, or a positive version number as a string."`
	To   string `json:"to,omitempty" jsonschema:"Destination: draft (default), deployed, or a positive version number as a string."`
}

type documentChange struct {
	Op     string `json:"op"`
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

func addDocumentTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "patch_flow_graph", InputSchema: graphPatchSchema, Description: "Atomically add, merge-update, or remove nodes and edges in a shared draft. Requires expected_revision from get_flow. Removing a node removes its incident edges. Validates the complete batch and never deploys. Use item IDs, not JSON Pointer paths. Example operation: {\"op\":\"update_node\",\"id\":\"node-id\",\"value\":{\"config\":{\"_label\":\"Kitchen\"}}}."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID               string                   `json:"id"`
			ExpectedRevision string                   `json:"expected_revision"`
			Operations       []flowops.GraphOperation `json:"operations"`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
				return nil, nil, err
			}
			f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
			if err != nil {
				return nil, nil, err
			}
			if err := flowops.PatchGraph(ctx, f, callerID(ctx), in.ExpectedRevision, in.Operations); err != nil {
				return nil, nil, opErr(err)
			}
			detail, err := flowDetail(ctx, f)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, draftValidationDetail(f, detail), nil
		})
	for _, kind := range []string{"flow", "dashboard"} {
		mcp.AddTool(s, &mcp.Tool{Name: "diff_" + kind, Description: "Compare deployable document content. Defaults to current deployment versus draft; an undeployed source is empty. Includes frozen settings, keyed item changes, and meaningful edge ordering. Does not modify the document."},
			func(ctx context.Context, req *mcp.CallToolRequest, in diffInput) (*mcp.CallToolResult, any, error) {
				if in.From == "" {
					in.From = "deployed"
				}
				if in.To == "" {
					in.To = "draft"
				}
				var before, after any
				var err error
				if kind == "flow" {
					if err = requirePermission(ctx, perm.PermFlowsRead); err != nil {
						return nil, nil, err
					}
					f, e := loadFlow(ctx, in.ID, models.FlowAccessView)
					if e != nil {
						return nil, nil, e
					}
					before, err = flowDocument(f, in.From)
					if err == nil {
						after, err = flowDocument(f, in.To)
					}
				} else {
					d, e := editableDashboard(ctx, in.ID)
					if e != nil {
						return nil, nil, e
					}
					before, err = dashboardDocument(d, in.From)
					if err == nil {
						after, err = dashboardDocument(d, in.To)
					}
				}
				if err != nil {
					return nil, nil, opErr(err)
				}
				changes := []documentChange{}
				compareDocument("", before, true, after, true, &changes)
				return nil, map[string]any{"from": in.From, "to": in.To, "changed": len(changes) > 0, "changes": changes}, nil
			})
	}
}

func versionNumber(ref string) (int, error) {
	n, err := strconv.Atoi(ref)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("version must be draft, deployed, or a positive version number")
	}
	return n, nil
}

func flowDocument(f *models.Flow, ref string) (any, error) {
	var v *models.FlowVersion
	var err error
	switch ref {
	case "draft":
		v, err = flowops.LoadDraft(f)
	case "deployed":
		if f.DeployedVersionID == nil {
			return nil, nil
		}
		v, err = models.FindFlowVersionByID(*f.DeployedVersionID)
	default:
		n, e := versionNumber(ref)
		if e != nil {
			return nil, e
		}
		v, err = models.FindFlowVersion(f.ID, n)
	}
	if err != nil {
		return nil, err
	}
	settings := *f
	if ref != "draft" {
		v.ApplySettings(&settings)
	}
	var graph map[string]any
	if err := json.Unmarshal([]byte(v.Graph), &graph); err != nil {
		return nil, err
	}
	normalizeGraphDocument(graph)
	return map[string]any{"name": settings.Name, "description": settings.Description, "error_flow_id": settings.ErrorFlowID, "graph": graph}, nil
}

func dashboardSnapshot(d *models.Dashboard, ref string) (*models.DashboardSnapshot, error) {
	if ref == "draft" {
		placements, err := models.ListDashboardWidgets(d.ID)
		if err != nil {
			return nil, err
		}
		sections, err := models.ListDashboardSections(d.ID)
		if err != nil {
			return nil, err
		}
		pages, err := models.ListDashboardPages(d.ID)
		if err != nil {
			return nil, err
		}
		return models.SnapshotDashboard(d, placements, sections, pages), nil
	}
	n := d.DeployedVersion
	var err error
	if ref != "deployed" {
		n, err = versionNumber(ref)
		if err != nil {
			return nil, err
		}
	}
	if n == 0 {
		return nil, nil
	}
	v, err := models.FindDashboardVersion(d.ID, n)
	if err != nil {
		return nil, err
	}
	return models.ParseDashboardSnapshot(v.Snapshot)
}

func dashboardDocument(d *models.Dashboard, ref string) (any, error) {
	snap, err := dashboardSnapshot(d, ref)
	if err != nil || snap == nil {
		return nil, err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if list, ok := result["widgets"].([]any); ok {
		for _, item := range list {
			w := item.(map[string]any)
			if config, ok := w["config"].(string); ok {
				var decoded any
				if json.Unmarshal([]byte(config), &decoded) == nil {
					w["config"] = decoded
				}
			}
		}
	}
	keyDocumentItems(result, "widgets", false)
	keyDocumentItems(result, "sections", false)
	return result, nil
}

func normalizeGraphDocument(g map[string]any) {
	keyDocumentItems(g, "nodes", false)
	keyDocumentItems(g, "edges", true)
	if subs, ok := g["subgraphs"].(map[string]any); ok {
		for _, v := range subs {
			if sub, ok := v.(map[string]any); ok {
				normalizeGraphDocument(sub)
			}
		}
	}
}

func keyDocumentItems(doc map[string]any, key string, ordered bool) {
	keyed := map[string]any{}
	order := []any{}
	if items, ok := doc[key].([]any); ok {
		for _, item := range items {
			if row, ok := item.(map[string]any); ok {
				if id, ok := row["id"].(string); ok {
					keyed[id] = row
					order = append(order, id)
				}
			}
		}
	}
	doc[key] = keyed
	if ordered {
		doc[key+"_order"] = order
	}
}

func compareDocument(path string, before any, hasBefore bool, after any, hasAfter bool, changes *[]documentChange) {
	if hasBefore && hasAfter && reflect.DeepEqual(before, after) {
		return
	}
	a, aok := before.(map[string]any)
	b, bok := after.(map[string]any)
	if hasBefore && hasAfter && aok && bok {
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			av, ah := a[k]
			bv, bh := b[k]
			escaped := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			compareDocument(path+"/"+escaped, av, ah, bv, bh, changes)
		}
		return
	}
	op := "replace"
	if !hasBefore {
		op = "add"
	} else if !hasAfter {
		op = "remove"
	}
	*changes = append(*changes, documentChange{Op: op, Path: path, Before: before, After: after})
}

// graphPatchSchema lists operation names explicitly so models do not infer JSON Patch syntax.
var graphPatchSchema = map[string]any{
	"type": "object", "additionalProperties": false,
	"required": []string{"id", "expected_revision", "operations"},
	"properties": map[string]any{
		"id":                map[string]any{"type": "string", "description": "Selected flow UUID."},
		"expected_revision": map[string]any{"type": "string", "description": "Current draft_revision from get_flow."},
		"operations": map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"op", "id"},
			"properties": map[string]any{
				"op":            map[string]any{"type": "string", "enum": []string{"add_node", "update_node", "remove_node", "add_edge", "update_edge", "remove_edge"}},
				"id":            map[string]any{"type": "string", "description": "Node or edge ID, not a path or array index."},
				"subgraph_path": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Local subgraph keys from root to target; omit for root."},
				"value":         graphPatchValueSchema,
			},
		}},
	},
}

// graphPatchValueSchema names common fields without restricting merge patches
// from clearing fields or editing nested node-specific configuration.
var graphPatchValueSchema = map[string]any{
	"type": "object", "additionalProperties": true,
	"description": "Required for add/update. Node fields include type, position, config, params, retry, bypass, execute_once, collapsed, width, height, subflow_id, subgraph_id. Edge fields are source, source_handle, target, target_handle, kind. Null removes a field; arrays replace. Do not put JSON syntax inside string values.",
	"properties": map[string]any{
		"type":          map[string]any{"type": []string{"string", "null"}, "description": "Exact registered node type from get_node_type, e.g. ha-call-service."},
		"position":      map[string]any{"type": []string{"object", "null"}, "properties": map[string]any{"x": map[string]any{"type": []string{"number", "null"}}, "y": map[string]any{"type": []string{"number", "null"}}}},
		"config":        map[string]any{"type": []string{"object", "null"}, "additionalProperties": true, "description": "Config keys and structured values from get_node_type.config_spec. Fields marked value_schema describe nested JSON."},
		"source":        map[string]any{"type": []string{"string", "null"}, "description": "Source node ID."},
		"source_handle": map[string]any{"type": []string{"string", "null"}, "description": "Exact resolved output port ID, e.g. out or a switch rule's id. Not its display label."},
		"target":        map[string]any{"type": []string{"string", "null"}, "description": "Target node ID."},
		"target_handle": map[string]any{"type": []string{"string", "null"}, "description": "Exact resolved input port ID, usually in. Value edges use param:<field>."},
		"kind":          map[string]any{"type": []string{"string", "null"}, "description": "Omit for message edges; value binds a pure output to a config parameter."},
	},
}
