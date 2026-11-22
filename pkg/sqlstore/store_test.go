package sqlstore

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// query runs a real SQLite request against a disposable installation directory.
func query(t *testing.T, store Store, id uuid.UUID, scope, sql string, params any) *Result {
	t.Helper()
	result, err := store.Query(t.Context(), id, scope, sql, params, 1000)
	require.NoError(t, err)
	return result
}

// TestSQLitePersistenceAndScopes pins SQLite types, bindings, DDL and private/global persistence.
func TestSQLitePersistenceAndScopes(t *testing.T) {
	store := Store{DataDir: t.TempDir()}
	id, other := uuid.New(), uuid.New()
	query(t, store, id, "flow", `CREATE TABLE readings (id INTEGER PRIMARY KEY, label TEXT UNIQUE, value REAL, active INTEGER, bytes BLOB)`, nil)
	label := `x'); DROP TABLE readings; --`
	result := query(t, store, id, "flow", `INSERT INTO readings(label, value, active, bytes) VALUES (:label, :value, :active, :bytes) RETURNING id, label`, map[string]any{":label": label, ":value": 2.5, ":active": true, ":bytes": []byte{0, 1, 2}})
	assert.Equal(t, int64(1), result.Changes)
	assert.Equal(t, int64(1), result.LastInsertID)
	assert.Equal(t, label, result.Rows[0]["label"])
	// Construct a new Store and read the file again rather than trusting a connection cache.
	result = query(t, Store{DataDir: store.DataDir}, id, "flow", `SELECT id, label, value, active, bytes, NULL AS missing FROM readings`, nil)
	assert.Equal(t, []string{"id", "label", "value", "active", "bytes", "missing"}, result.Columns)
	assert.Equal(t, []map[string]any{{"id": int64(1), "label": label, "value": 2.5, "active": int64(1), "bytes": "AAEC", "missing": nil}}, result.Rows)
	_, err := store.Query(t.Context(), other, "flow", `SELECT * FROM readings`, nil, 1000)
	assert.Error(t, err, "another flow must not read this flow's tables")
	query(t, store, id, "flow", `CREATE INDEX reading_label ON readings(label)`, nil)
	query(t, store, id, "flow", `ALTER TABLE readings ADD COLUMN room TEXT`, nil)
	query(t, store, id, "flow", `CREATE TABLE timestamps (created TEXT DEFAULT CURRENT_TIMESTAMP)`, nil)
	query(t, store, id, "flow", `INSERT INTO timestamps DEFAULT VALUES`, nil)
	assert.NotEmpty(t, query(t, store, id, "flow", `SELECT created FROM timestamps`, nil).Rows[0]["created"])
	query(t, store, id, "flow", `ALTER TABLE readings RENAME COLUMN room TO area`, nil)
	query(t, store, id, "flow", `CREATE VIEW reading_values AS SELECT value FROM readings`, nil)
	query(t, store, id, "flow", `CREATE TRIGGER adjust AFTER UPDATE OF value ON readings BEGIN UPDATE readings SET active=0 WHERE id=NEW.id; END`, nil)
	result = query(t, store, id, "flow", `UPDATE readings SET value=? WHERE id=?`, []any{3.5, 1})
	assert.Equal(t, int64(1), result.Changes)
	result = query(t, store, id, "flow", `SELECT value, active, json_extract('{"x":3}', '$.x') AS json_value FROM readings; -- trailing comment`, nil)
	assert.Equal(t, []map[string]any{{"value": 3.5, "active": int64(0), "json_value": int64(3)}}, result.Rows)
	query(t, store, id, "global", `CREATE TABLE shared (value INTEGER)`, nil)
	query(t, store, id, "global", `INSERT INTO shared VALUES (?)`, []any{42})
	assert.Equal(t, int64(42), query(t, store, other, "global", `SELECT value FROM shared`, nil).Rows[0]["value"])
	result = query(t, store, id, "flow", `DELETE FROM readings WHERE id=1 RETURNING label`, nil)
	assert.Equal(t, label, result.Rows[0]["label"])
	assert.Empty(t, query(t, store, id, "flow", `SELECT * FROM readings`, nil).Rows)
}

// TestSQLiteSandbox preserves outside bytes and prior rows across actual SQLite escape attempts.
func TestSQLiteSandbox(t *testing.T) {
	store := Store{DataDir: t.TempDir()}
	id := uuid.New()
	outside := filepath.Join(t.TempDir(), "sentinel.db")
	require.NoError(t, os.WriteFile(outside, []byte("private sentinel"), 0600))
	query(t, store, id, "flow", `CREATE TABLE values_table (value INTEGER)`, nil)
	query(t, store, id, "flow", `INSERT INTO values_table VALUES (1)`, nil)
	for _, tc := range []struct {
		name, sql string
		params    any
	}{
		{"attach", `ATTACH DATABASE ? AS outside`, []any{outside}},
		{"vacuum into", `VACUUM INTO ?`, []any{outside}},
		{"detach", `DETACH main`, nil},
		{"pragma", `PRAGMA writable_schema=ON`, nil},
		{"pragma table function", `SELECT * FROM pragma_table_info('values_table')`, nil},
		{"load extension", `SELECT load_extension(?)`, []any{outside}},
		{"read file", `SELECT readfile(?)`, []any{outside}},
		{"write file", `SELECT writefile(?, 'corrupt')`, []any{outside}},
		{"virtual table", `CREATE VIRTUAL TABLE escape USING fts5(content)`, nil},
		{"temporary table", `CREATE TEMP TABLE escape(value)`, nil},
		{"temporary schema read", `SELECT * FROM temp.sqlite_temp_master`, nil},
		{"temporary schema write", `UPDATE temp.sqlite_temp_master SET sql='corrupt'`, nil},
		{"transaction", `BEGIN`, nil},
		{"savepoint", `SAVEPOINT escape`, nil},
		{"schema corruption", `DELETE FROM sqlite_schema`, nil},
		{"two statements", `INSERT INTO values_table VALUES (2); DELETE FROM values_table`, nil},
		{"NUL termination", "INSERT INTO values_table VALUES(2);\x00DELETE FROM values_table", nil},
		{"quoted semicolon", `SELECT ';' AS safe; INSERT INTO values_table VALUES (2)`, nil},
		{"constraint partial write", `INSERT OR FAIL INTO values_table(rowid,value) VALUES (2,2),(1,3)`, nil},
		{"returning row cap", `UPDATE values_table SET value=9 RETURNING value`, nil},
		{"result bytes", `SELECT hex(zeroblob(600000)) AS oversized`, nil},
		{"missing binding", `INSERT INTO values_table VALUES (?)`, nil},
		{"extra binding", `INSERT INTO values_table VALUES (2)`, []any{1}},
		{"structured binding", `INSERT INTO values_table VALUES (?)`, []any{map[string]any{"x": 1}}},
		{"duplicate columns", `SELECT 1 AS value, 2 AS value`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			maxRows := 1000
			if tc.name == "returning row cap" {
				query(t, store, id, "flow", `INSERT INTO values_table VALUES (2)`, nil)
				maxRows = 1
			}
			before, err := os.ReadFile(filepath.Join(store.DataDir, "sql", "flow-"+id.String()+".db"))
			require.NoError(t, err)
			_, err = store.Query(t.Context(), id, "flow", tc.sql, tc.params, maxRows)
			assert.Error(t, err, "the SQLite policy must refuse %s", tc.name)
			after, err := os.ReadFile(filepath.Join(store.DataDir, "sql", "flow-"+id.String()+".db"))
			require.NoError(t, err)
			assert.Equal(t, before, after, "failed statements must leave the snapshot unchanged")
			bytes, err := os.ReadFile(outside)
			require.NoError(t, err)
			assert.Equal(t, "private sentinel", string(bytes))
			assert.Equal(t, int64(1), query(t, store, id, "flow", `SELECT value FROM values_table WHERE rowid=1`, nil).Rows[0]["value"])
		})
	}
	assert.Equal(t, ";", query(t, store, id, "flow", `SELECT ';' AS safe; -- comment`, nil).Rows[0]["safe"])
	name := filepath.Join(store.DataDir, "sql", "flow-"+uuid.NewString()+".db")
	require.NoError(t, os.Symlink(outside, name))
	symlinkID := uuid.MustParse(filepath.Base(name)[5:41])
	_, err := store.Query(t.Context(), symlinkID, "flow", `SELECT 1`, nil, 1000)
	assert.Error(t, err, "scope snapshots must not follow symlinks")
	otherStore := Store{DataDir: t.TempDir()}
	require.NoError(t, os.Symlink(filepath.Join(store.DataDir, "sql"), filepath.Join(otherStore.DataDir, "sql")))
	_, err = otherStore.Query(t.Context(), id, "flow", `DELETE FROM values_table`, nil, 1000)
	assert.Error(t, err, "a substituted SQL directory must not expose another database")
}

// TestSQLiteConcurrentUpdatesAndCancellation pins serialized snapshots and interrupt recovery.
func TestSQLiteConcurrentUpdatesAndCancellation(t *testing.T) {
	store := Store{DataDir: t.TempDir()}
	id := uuid.New()
	query(t, store, id, "flow", `CREATE TABLE counter(value INTEGER)`, nil)
	query(t, store, id, "flow", `INSERT INTO counter VALUES(0)`, nil)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			_, err := store.Query(t.Context(), id, "flow", `UPDATE counter SET value=value+1`, nil, 1000)
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	assert.Equal(t, int64(12), query(t, store, id, "flow", `SELECT value FROM counter`, nil).Rows[0]["value"])
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := store.Query(ctx, id, "flow", `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n`, nil, 1000)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, int64(12), query(t, store, id, "flow", `SELECT value FROM counter`, nil).Rows[0]["value"])
	release, err := lockDatabase(t.Context(), filepath.Join(store.DataDir, "sql", "flow-"+id.String()+".db"))
	require.NoError(t, err)
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = store.Query(ctx, id, "flow", `UPDATE counter SET value=0`, nil, 1000)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	release()
	assert.Equal(t, int64(12), query(t, store, id, "flow", `SELECT value FROM counter`, nil).Rows[0]["value"])
}

// TestSQLiteDatabaseCapacity rejects growth before saving and keeps the last valid snapshot readable.
func TestSQLiteDatabaseCapacity(t *testing.T) {
	store := Store{DataDir: t.TempDir()}
	id := uuid.New()
	query(t, store, id, "flow", `CREATE TABLE data(bytes BLOB)`, nil)
	_, err := store.Query(t.Context(), id, "flow", `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<20) INSERT INTO data SELECT zeroblob(1000000) FROM n`, nil, 1000)
	assert.Error(t, err, "an oversized database must fail before snapshot replacement")
	assert.Equal(t, int64(0), query(t, store, id, "flow", `SELECT count(*) AS count FROM data`, nil).Rows[0]["count"])
	// printf is subject to SQLite's value limit before host output allocation.
	_, err = store.Query(t.Context(), id, "flow", `SELECT printf('%100000000s','x') AS text`, nil, 1000)
	assert.Error(t, err, "SQLite allocation failures must return normally")
	_, err = store.Query(t.Context(), id, "flow", `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<80), chunks AS MATERIALIZED (SELECT randomblob(1000000) AS bytes FROM n) SELECT sum(length(bytes)) FROM chunks`, nil, 1000)
	assert.Error(t, err, "materialized rows must not escape the SQLite memory cap")
	assert.Equal(t, int64(1), query(t, store, id, "flow", `SELECT 1 AS value`, nil).Rows[0]["value"])
}
