package sqlstore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/vfs"
)

// Result contains bounded JSON-compatible rows and SQLite write counters.
type Result struct {
	Columns      []string         `json:"columns"`
	Rows         []map[string]any `json:"rows"`
	Changes      int64            `json:"changes"`
	LastInsertID int64            `json:"last_insert_id"`
}

// execute owns the disposable connection, including driver allocation panics.
func execute(ctx context.Context, data []byte, sql string, params any, maxRows int) (result *Result, snapshot []byte, changed bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			result, snapshot, changed = nil, nil, false
			err = fmt.Errorf("sql execution stopped: %v", p)
		}
	}()
	name := "query-" + rand.Text()
	budget := &memoryBudget{allocated: int64(cap(data))}
	file := &memoryFile{data: data, budget: budget, retain: true}
	fs := &memoryVFS{name: name, main: file}
	vfs.Register(name, fs)
	defer vfs.Unregister(name)
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, 64<<20), "file:"+name+"?vfs="+name)
	if err != nil {
		return nil, nil, false, err
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
	conn.HardHeapLimit(48 << 20)
	for _, setting := range []struct {
		op    sqlite3.DBConfig
		value bool
	}{
		{sqlite3.DBCONFIG_DEFENSIVE, true},
		{sqlite3.DBCONFIG_TRUSTED_SCHEMA, false},
	} {
		if _, err := conn.Config(setting.op, setting.value); err != nil {
			return nil, nil, false, err
		}
	}
	for _, limit := range []struct {
		op    sqlite3.LimitCategory
		value int
	}{
		{sqlite3.LIMIT_LENGTH, maxResultBytes}, {sqlite3.LIMIT_SQL_LENGTH, maxSQLBytes},
		{sqlite3.LIMIT_COLUMN, 100}, {sqlite3.LIMIT_EXPR_DEPTH, 50},
		{sqlite3.LIMIT_COMPOUND_SELECT, 20}, {sqlite3.LIMIT_VDBE_OP, 25000},
		{sqlite3.LIMIT_FUNCTION_ARG, 32}, {sqlite3.LIMIT_ATTACHED, 0},
		{sqlite3.LIMIT_LIKE_PATTERN_LENGTH, 1000}, {sqlite3.LIMIT_VARIABLE_NUMBER, 1000},
		{sqlite3.LIMIT_TRIGGER_DEPTH, 10},
	} {
		conn.Limit(limit.op, limit.value)
	}
	if err := conn.Exec("PRAGMA journal_mode=MEMORY; PRAGMA temp_store=MEMORY; PRAGMA cache_size=-4096; PRAGMA cell_size_check=ON; PRAGMA mmap_size=0;"); err != nil {
		return nil, nil, false, err
	}
	denied := ""
	altering := false
	if err := conn.SetAuthorizer(func(action sqlite3.AuthorizerActionCode, a, b, schema, inner string) sqlite3.AuthorizerReturnCode {
		if action == sqlite3.AUTH_ALTER_TABLE && a == "main" {
			altering = true
		}
		// ALTER checks and rewrites the empty temporary catalog for dependent
		// objects. Ordinary SQL has no access to that schema.
		if altering && schema == "temp" && (a == "sqlite_temp_master" || a == "sqlite_temp_schema") &&
			(action == sqlite3.AUTH_READ || action == sqlite3.AUTH_UPDATE && b == "sql") {
			return sqlite3.AUTH_OK
		}
		decision := authorize(action, a, b, schema, inner)
		if decision == sqlite3.AUTH_DENY {
			denied = fmt.Sprintf("%v %q %q in schema %q", action, a, b, schema)
		}
		return decision
	}); err != nil {
		return nil, nil, false, err
	}
	stmt, tail, err := conn.PrepareFlags(sql, sqlite3.PREPARE_NO_VTAB)
	if err != nil {
		if denied != "" {
			return nil, nil, false, fmt.Errorf("sql sandbox refused %s: %w", denied, err)
		}
		return nil, nil, false, fmt.Errorf("preparing sql: %w", err)
	}
	if stmt == nil {
		return nil, nil, false, fmt.Errorf("sql must contain one statement")
	}
	defer stmt.Close()
	// SQLite parses comments and empty statements itself; no text filter needs
	// to guess where a quoted semicolon ends a statement.
	for tail != "" {
		extra, rest, err := conn.PrepareFlags(tail, sqlite3.PREPARE_NO_VTAB)
		if extra != nil {
			extra.Close()
		}
		if err != nil || extra != nil {
			return nil, nil, false, fmt.Errorf("sql must contain exactly one statement")
		}
		tail = rest
	}
	if err := bind(stmt, params); err != nil {
		return nil, nil, false, err
	}
	changed = !stmt.ReadOnly()
	result = &Result{Columns: []string{}, Rows: []map[string]any{}}
	seen := make(map[string]bool)
	for i := 0; i < stmt.ColumnCount(); i++ {
		name := stmt.ColumnName(i)
		if seen[name] {
			return nil, nil, false, fmt.Errorf("duplicate sql column %q; use an alias", name)
		}
		seen[name] = true
		result.Columns = append(result.Columns, name)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, nil, false, err
	}
	bytes := len(encoded)
	for stmt.Step() {
		if len(result.Rows) >= maxRows {
			return nil, nil, false, fmt.Errorf("sql result exceeds %d rows", maxRows)
		}
		row := make(map[string]any, len(result.Columns))
		for i, name := range result.Columns {
			switch stmt.ColumnType(i) {
			case sqlite3.INTEGER:
				row[name] = stmt.ColumnInt64(i)
			case sqlite3.FLOAT:
				row[name] = stmt.ColumnFloat(i)
			case sqlite3.TEXT:
				row[name] = stmt.ColumnText(i)
			case sqlite3.BLOB:
				row[name] = base64.StdEncoding.EncodeToString(stmt.ColumnRawBlob(i))
			case sqlite3.NULL:
				row[name] = nil
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, nil, false, fmt.Errorf("encoding sql row: %w", err)
		}
		bytes += len(encoded) + 1
		if bytes > maxResultBytes {
			return nil, nil, false, fmt.Errorf("sql result exceeds 1 MiB")
		}
		result.Rows = append(result.Rows, row)
	}
	if err := stmt.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, nil, false, ctx.Err()
		}
		return nil, nil, false, fmt.Errorf("executing sql: %w", err)
	}
	result.Changes, result.LastInsertID = conn.Changes(), conn.LastInsertRowID()
	encoded, err = json.Marshal(result)
	if err != nil {
		return nil, nil, false, err
	}
	if len(encoded) > maxResultBytes {
		return nil, nil, false, fmt.Errorf("sql result exceeds 1 MiB")
	}
	if err := stmt.Close(); err != nil {
		return nil, nil, false, err
	}
	if err := conn.Close(); err != nil {
		return nil, nil, false, err
	}
	return result, file.data, changed, nil
}

// authorize permits main-schema SQL and reviewed built-ins, denying new actions.
func authorize(action sqlite3.AuthorizerActionCode, a, b, schema, inner string) sqlite3.AuthorizerReturnCode {
	if schema != "" && schema != "main" {
		return sqlite3.AUTH_DENY
	}
	switch action {
	case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE,
		sqlite3.AUTH_DELETE, sqlite3.AUTH_CREATE_TABLE, sqlite3.AUTH_CREATE_INDEX,
		sqlite3.AUTH_CREATE_VIEW, sqlite3.AUTH_CREATE_TRIGGER, sqlite3.AUTH_DROP_TABLE,
		sqlite3.AUTH_DROP_INDEX, sqlite3.AUTH_DROP_VIEW, sqlite3.AUTH_DROP_TRIGGER,
		sqlite3.AUTH_REINDEX, sqlite3.AUTH_ANALYZE, sqlite3.AUTH_RECURSIVE:
		return sqlite3.AUTH_OK
	case sqlite3.AUTH_ALTER_TABLE:
		if a == "main" {
			return sqlite3.AUTH_OK
		}
	case sqlite3.AUTH_FUNCTION:
		if strings.Contains(" "+safeFunctions+" ", " "+strings.ToLower(b)+" ") {
			return sqlite3.AUTH_OK
		}
	}
	return sqlite3.AUTH_DENY
}

// safeFunctions excludes extension, file, evaluator, and virtual-table helpers.
const safeFunctions = "abs avg changes char coalesce concat concat_ws count format glob group_concat hex if ifnull iif instr " +
	"last_insert_rowid length like likelihood likely lower ltrim max min nullif octet_length printf quote random randomblob " +
	"replace round rtrim sign soundex sqlite_source_id sqlite_version substr substring sum total total_changes trim typeof " +
	"unhex unicode unlikely upper zeroblob current_date current_time current_timestamp date time datetime julianday unixepoch strftime timediff " +
	"acos acosh asin asinh atan atan2 atanh ceil ceiling cos cosh degrees exp floor ln log log10 log2 mod pi pow power radians sin sinh sqrt tan tanh trunc " +
	"json json_array json_array_length json_error_position json_extract json_insert json_object json_patch json_remove json_replace " +
	"json_set json_type json_valid json_quote json_group_array json_group_object jsonb jsonb_array jsonb_extract jsonb_insert " +
	"jsonb_object jsonb_patch jsonb_remove jsonb_replace jsonb_set jsonb_group_array jsonb_group_object -> ->> " +
	"row_number rank dense_rank percent_rank cume_dist ntile lag lead first_value last_value nth_value " +
	"sqlite_rename_table sqlite_rename_column sqlite_rename_test sqlite_drop_column sqlite_rename_quotefix"

// bind requires every placeholder and rejects values SQLite cannot represent.
func bind(stmt *sqlite3.Stmt, params any) error {
	if params == nil {
		params = []any{}
	}
	if encoded, err := json.Marshal(params); err != nil || len(encoded) > maxResultBytes {
		return fmt.Errorf("sql parameters must encode within 1 MiB")
	}
	switch values := params.(type) {
	case []any:
		if len(values) != stmt.BindCount() {
			return fmt.Errorf("sql expects %d parameters, got %d", stmt.BindCount(), len(values))
		}
		for i, value := range values {
			if err := bindValue(stmt, i+1, value); err != nil {
				return err
			}
		}
	case map[string]any:
		if len(values) != stmt.BindCount() {
			return fmt.Errorf("sql expects %d named parameters, got %d", stmt.BindCount(), len(values))
		}
		for name, value := range values {
			index := stmt.BindIndex(name)
			if index == 0 {
				return fmt.Errorf("unknown sql parameter %q; include its :, @, or $ prefix", name)
			}
			if err := bindValue(stmt, index, value); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("sql parameters must be an array or object")
	}
	return nil
}

// bindValue preserves integers and rejects structured and nonfinite values.
func bindValue(stmt *sqlite3.Stmt, index int, value any) error {
	switch v := value.(type) {
	case nil:
		return stmt.BindNull(index)
	case string:
		return stmt.BindText(index, v)
	case []byte:
		return stmt.BindBlob(index, v)
	case bool:
		return stmt.BindBool(index, v)
	case int:
		return stmt.BindInt64(index, int64(v))
	case int64:
		return stmt.BindInt64(index, v)
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return stmt.BindFloat(index, v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return stmt.BindInt64(index, n)
		}
		if n, err := v.Float64(); err == nil {
			return bindValue(stmt, index, n)
		}
	}
	return fmt.Errorf("sql parameter %d must be a scalar, binary value, or null", index)
}
