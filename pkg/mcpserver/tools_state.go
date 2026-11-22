package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type persistedStateEntry struct {
	ID           uint            `json:"id"`
	NodeID       string          `json:"node_id,omitempty"`
	Key          string          `json:"key"`
	Value        json.RawMessage `json:"value,omitempty"`
	ValueOmitted bool            `json:"value_omitted,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func addStateTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "inspect_flow_state", Description: "Read persisted flow variables, durable node state, or pending waits. Variables and node state are read from storage, whose writes are asynchronous, so they may lag running nodes; waits come from the running engine. Read-only; excludes global variables, transient state, and connection credentials."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID      string `json:"id" jsonschema:"Flow UUID."`
			Store   string `json:"store,omitempty" jsonschema:"variables (default), nodes, or waits (pending timers, held messages, and partial groups)."`
			NodeID  string `json:"node_id,omitempty" jsonschema:"Exact durable node namespace; only valid with store=nodes."`
			Key     string `json:"key,omitempty" jsonschema:"Exact state key filter."`
			AfterID uint   `json:"after_id,omitempty" jsonschema:"Continue after the returned next_id."`
			Limit   int    `json:"limit,omitempty" jsonschema:"Page size 1-100; default 50."`
		}) (*mcp.CallToolResult, any, error) {
			for _, p := range []string{perm.PermFlowsRead, perm.PermRunsRead} {
				if err := requirePermission(ctx, p); err != nil {
					return nil, nil, err
				}
			}
			if in.Store == "" {
				in.Store = "variables"
			}
			if in.Store != "variables" && in.Store != "nodes" && in.Store != "waits" {
				return nil, nil, fmt.Errorf("store must be variables, nodes, or waits")
			}
			if in.Store == "waits" {
				f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
				if err != nil {
					return nil, nil, err
				}
				waits, err := flowops.ListWaits(f)
				if err != nil {
					return nil, nil, opErr(err)
				}
				return nil, map[string]any{"waits": waits, "source": "engine", "store": in.Store}, nil
			}
			if in.Store == "variables" && in.NodeID != "" {
				return nil, nil, fmt.Errorf("node_id requires store=nodes")
			}
			if in.Limit == 0 {
				in.Limit = 50
			}
			if in.Limit < 1 || in.Limit > 100 {
				return nil, nil, fmt.Errorf("limit must be between 1 and 100")
			}
			f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
			if err != nil {
				return nil, nil, err
			}
			q := db.DB.WithContext(ctx).Where("flow_id = ? AND id > ?", f.ID, in.AfterID).Order("id").Limit(in.Limit + 1)
			if in.Key != "" {
				q = q.Where(map[string]any{"key": in.Key})
			}
			// The database bounds values before loading them; an omitted value keeps its key and timestamp.
			columns := []string{"id", "key", "updated_at", "CASE WHEN length(value) <= 65536 THEN value ELSE '' END AS value"}
			if in.Store == "variables" {
				q = q.Model(&models.Variable{}).Where("scope = ?", "flow")
			} else {
				q = q.Model(&models.NodeStoreEntry{})
				columns = append(columns, "node_id")
				if in.NodeID != "" {
					q = q.Where("node_id = ?", in.NodeID)
				}
			}
			var rows []struct {
				ID        uint
				NodeID    string
				Key       string
				Value     string
				UpdatedAt time.Time
			}
			if err := q.Select(columns).Scan(&rows).Error; err != nil {
				return nil, nil, err
			}
			more := len(rows) > in.Limit
			if more {
				rows = rows[:in.Limit]
			}
			entries := []persistedStateEntry{}
			size := 0
			next := in.AfterID
			for _, row := range rows {
				entry := persistedStateEntry{ID: row.ID, NodeID: row.NodeID, Key: row.Key, UpdatedAt: row.UpdatedAt}
				if row.Value != "" && json.Valid([]byte(row.Value)) && len(row.Value) <= 65536 && size+len(row.Value) <= 1<<20 {
					entry.Value = json.RawMessage(row.Value)
					size += len(row.Value)
				} else {
					entry.ValueOmitted = true
				}
				entries = append(entries, entry)
				next = row.ID
			}
			return nil, map[string]any{"entries": entries, "source": "persisted", "store": in.Store, "has_more": more, "next_id": next}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "cancel_flow_wait", Description: "End one pending wait (a timer, held message, or partial group) without delivering it. The cancellation is recorded in run history. List waits with inspect_flow_state store=waits."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID     string `json:"id" jsonschema:"Flow UUID."`
			NodeID string `json:"node_id" jsonschema:"Node holding the wait."`
			Key    string `json:"key" jsonschema:"The wait's key, as inspect_flow_state reports it."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermRunsCancel); err != nil {
				return nil, nil, err
			}
			f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
			if err != nil {
				return nil, nil, err
			}
			username := "an operator"
			if uc := middleware.GetUser(ctx); uc != nil {
				username = uc.Username
			}
			if err := flowops.CancelWait(f, in.NodeID, in.Key, username); err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"cancelled": true}, nil
		})
}
