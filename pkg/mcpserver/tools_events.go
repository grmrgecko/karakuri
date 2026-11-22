package mcpserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/auth"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errSubject = errors.New("a subject must be kind:id")

type listEventsInput struct {
	Categories []string `json:"categories,omitempty" jsonschema:"device, connection, flow, agent, security, system, or custom; security needs the audit permission."`
	Levels     []string `json:"levels,omitempty" jsonschema:"info, warning, or error."`
	Subjects   []string `json:"subjects,omitempty" jsonschema:"Subjects as kind:id, such as control:<uuid>, device:<uuid>, connection:<uuid>, flow:<uuid>, or ha_entity:<connection>/<entity>."`
	Actor      string   `json:"actor,omitempty" jsonschema:"Actor as kind or kind:id: user, apikey, flow, agent, system, or anonymous."`
	Query      string   `json:"query,omitempty" jsonschema:"Text in the message, subject, or actor name."`
	From       string   `json:"from,omitempty" jsonschema:"Earliest time: RFC 3339 or now-24h."`
	To         string   `json:"to,omitempty" jsonschema:"Latest time."`
	Before     uint     `json:"before,omitempty" jsonschema:"Entries older than this ID, for the next page."`
	Limit      int      `json:"limit,omitempty" jsonschema:"Entries to return, 1 to 500; default 100."`
}

// addEventTools registers the event log tools. Flows write the log with the
// Log Event node; there is no tool that writes it.
func addEventTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_events",
		Description: "List event log entries newest first: control and device changes with who made them, connection failures, flow deploys and failures, agents, backups, and, with the audit permission, sign-ins and administrative changes. Repeats within a minute are folded with a count."},
		func(ctx context.Context, req *mcp.CallToolRequest, in listEventsInput) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermEventsRead); err != nil {
				return nil, nil, err
			}
			uid, _ := userID(ctx)
			f := events.Filter{Categories: in.Categories, Levels: in.Levels, Text: in.Query, Before: in.Before, Limit: in.Limit,
				Audit: auth.Can(ctx, uid, perm.PermEventsAudit)}
			for _, raw := range in.Subjects {
				sub, ok := events.ParseSubject(raw)
				if !ok {
					return nil, nil, errSubject
				}
				f.Subjects = append(f.Subjects, sub)
			}
			if in.Actor != "" {
				f.ActorKind, f.ActorID, _ = strings.Cut(in.Actor, ":")
			}
			if err := flowops.LimitEventFlows(ctx, uid, &f); err != nil {
				return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
			}
			now := time.Now()
			var err error
			if in.From != "" {
				if f.From, err = tsdb.ParseTime(in.From, now, false); err != nil {
					return nil, nil, err
				}
			}
			if in.To != "" {
				if f.To, err = tsdb.ParseTime(in.To, now, true); err != nil {
					return nil, nil, err
				}
			}
			page, err := events.List(f)
			if err != nil {
				return nil, nil, err
			}
			return nil, page, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_event_settings",
		Description: "Read the event log's retention per category, entry limit, excluded controls, and logged Home Assistant entities."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermEventsRead); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"settings": events.Current()}, nil
		})
}
