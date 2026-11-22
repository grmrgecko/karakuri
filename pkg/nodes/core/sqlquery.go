package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/sqlstore"
)

// SQLQueryNode queries private flow or shared global SQLite storage.
type SQLQueryNode struct {
	store *sqlstore.Store
}

func (SQLQueryNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "sql-query", Label: "SQL query", Folder: flow.FolderStorage, Icon: "las la-database",
		Description: "Reads or writes an isolated SQLite database for this flow or all flows",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}, flow.ErrorPort()},
		ConfigSpec: []engine.FieldSpec{
			{ID: "scope", Label: "Database scope", Type: flow.FieldSelect, Default: "flow", Options: []engine.Option{{Value: "flow", Label: "This flow"}, {Value: "global", Label: "Global"}}, Help: "All SQL query nodes in this flow share one database. Global shares a separate database with every flow."},
			{ID: "query", Label: "SQL", Type: flow.FieldText, Required: true, Placeholder: "SELECT value FROM readings WHERE room = ?", Help: "One SQLite statement. Use placeholders for incoming values; SQL is not interpolated. SELECT, writes, and ordinary schema changes are allowed."},
			{ID: "parameters", Label: "Parameters", Type: flow.FieldExpression, Default: "[]", Placeholder: "[payload.room]", Help: "Expression returning an array or a named object, such as {\":room\": payload.room}. Values are bound separately from SQL."},
			{ID: "timeout", Label: "Timeout (seconds)", Type: flow.FieldNumber, Default: 5, Help: "Whole seconds from 1 to 60, including time waiting for the database."},
			{ID: "max_rows", Label: "Maximum rows", Type: flow.FieldNumber, Default: 1000, Help: "From 1 to 10000. Exceeding this or 1 MiB of result data fails the statement without saving writes."},
		},
	}
}

// Execute binds message values without rendering SQL and prohibits retries.
func (n SQLQueryNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (outputs map[string][]*engine.Msg, err error) {
	defer func() {
		if err != nil {
			err = &nodeapi.ExecutionError{Message: err.Error(), Cause: err, Retryable: false}
		}
	}()
	var cfg struct {
		Scope      string `json:"scope"`
		Query      string `json:"query"`
		Parameters string `json:"parameters"`
		Timeout    *int   `json:"timeout"`
		MaxRows    *int   `json:"max_rows"`
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	seconds, maxRows := 5, 1000
	if cfg.Timeout != nil {
		seconds = *cfg.Timeout
	}
	if cfg.MaxRows != nil {
		maxRows = *cfg.MaxRows
	}
	if seconds < 1 || seconds > 60 {
		return nil, fmt.Errorf("sql timeout must be between 1 and 60 whole seconds")
	}
	if maxRows < 1 || maxRows > 10000 {
		return nil, fmt.Errorf("sql row limit must be between 1 and 10000")
	}
	if cfg.Scope != "" && cfg.Scope != "flow" && cfg.Scope != "global" {
		return nil, fmt.Errorf("sql scope must be flow or global")
	}
	identity, ok := ec.(nodeapi.FlowIdentity)
	if !ok {
		return nil, fmt.Errorf("sql storage requires a server flow execution")
	}
	id, err := uuid.Parse(identity.FlowID())
	if err != nil {
		return nil, fmt.Errorf("invalid executing flow identity: %w", err)
	}
	msg := inbound(ec, inputs)
	var params any = []any{}
	if strings.TrimSpace(cfg.Parameters) != "" {
		params, err = ec.Eval(cfg.Parameters, msg)
		if err != nil {
			return nil, fmt.Errorf("evaluating sql parameters: %w", err)
		}
	}
	store := n.store
	if store == nil {
		store = &sqlstore.Store{DataDir: resources.GetDataPath()}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	result, err := store.Query(ctx, id, cfg.Scope, cfg.Query, params, maxRows)
	if err != nil {
		return nil, err
	}
	out := passThrough(msg)
	out.Payload = result
	return engine.One("out", out), nil
}
