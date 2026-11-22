package backup

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// summaryModels are the kinds a manifest counts for people, by the name the
// spec uses. Soft-deleted rows are not counted here; the per-table counts
// include them.
var summaryModels = []struct {
	name  string
	model any
}{
	{"flows", &models.Flow{}},
	{"dashboards", &models.Dashboard{}},
	{"connections", &models.Connection{}},
	{"devices", &models.MQTTDevice{}},
	{"controls", &models.Control{}},
	{"users", &models.User{}},
	{"agents", &models.Agent{}},
}

var schemaCache sync.Map

// parseModel returns the GORM schema of a model under the database's naming.
func parseModel(gdb *gorm.DB, model any) (*schema.Schema, error) {
	return schema.Parse(model, &schemaCache, gdb.NamingStrategy)
}

// columns returns a schema's database columns, leaving out association
// fields, which have no column of their own.
func columns(s *schema.Schema) []*schema.Field {
	var out []*schema.Field
	for _, f := range s.Fields {
		if f.DBName != "" {
			out = append(out, f)
		}
	}
	return out
}

// beginSnapshot opens a read-only transaction that sees one moment of the
// database. On SQLite a read-only transaction begins deferred, whatever the
// connection's _txlock, so under WAL it holds a snapshot without blocking
// writers; the driver refuses an explicit isolation level, so none is given.
func beginSnapshot(ctx context.Context, gdb *gorm.DB) *gorm.DB {
	opts := &sql.TxOptions{ReadOnly: true}
	if gdb.Name() != "sqlite" {
		opts.Isolation = sql.LevelRepeatableRead
	}
	return gdb.WithContext(ctx).Begin(opts)
}

// dumpDatabase writes every table of models.All() to work/db/<table>.jsonl
// from one snapshot and calls add for each file in registry order. Each row
// is one JSON object keyed by column, so fields the API hides (password
// hashes, sealed secrets) are kept. It returns the rows per table and the
// summary counts.
//
// Only the tables and columns the database has are copied. A backup taken
// before a newer release migrates the database meets models that name tables
// and columns it does not have yet; leaving them out makes an archive like an
// older release's, which restore already accepts.
func dumpDatabase(ctx context.Context, gdb *gorm.DB, work string, add func(name string, f *os.File) error) (map[string]int64, map[string]int64, error) {
	tx := beginSnapshot(ctx, gdb)
	if tx.Error != nil {
		return nil, nil, fmt.Errorf("opening a database snapshot: %w", tx.Error)
	}
	defer tx.Rollback()

	// SQLite pins its snapshot at the first read, not at BEGIN, so read once
	// before anything else.
	var probe int64
	if err := tx.Model(&models.Setting{}).Count(&probe).Error; err != nil {
		return nil, nil, fmt.Errorf("opening a database snapshot: %w", err)
	}

	dir := filepath.Join(work, "db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	tables := map[string]int64{}
	migrator := tx.Migrator()
	for _, model := range models.All() {
		s, err := parseModel(gdb, model)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the %T model: %w", model, err)
		}
		if !migrator.HasTable(s.Table) {
			continue
		}
		var present []*schema.Field
		for _, f := range columns(s) {
			if migrator.HasColumn(model, f.DBName) {
				present = append(present, f)
			}
		}
		path := filepath.Join(dir, s.Table+".jsonl")
		n, err := dumpTable(ctx, tx, model, present, path)
		if err != nil {
			return nil, nil, fmt.Errorf("copying table %s: %w", s.Table, err)
		}
		tables[s.Table] = n
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		err = add("db/"+s.Table+".jsonl", f)
		f.Close()
		os.Remove(path)
		if err != nil {
			return nil, nil, err
		}
	}

	counts := map[string]int64{}
	for _, m := range summaryModels {
		var n int64
		if !migrator.HasTable(m.model) {
			counts[m.name] = 0
			continue
		}
		if err := tx.Model(m.model).Count(&n).Error; err != nil {
			return nil, nil, fmt.Errorf("counting %s: %w", m.name, err)
		}
		counts[m.name] = n
	}
	return tables, counts, nil
}

// dumpTable streams one table's rows, soft-deleted ones included, to path,
// reading only the columns in cols.
func dumpTable(ctx context.Context, tx *gorm.DB, model any, cols []*schema.Field, path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 256<<10)

	names := make([]string, len(cols))
	for i, f := range cols {
		names[i] = f.DBName
	}
	rows, err := tx.Unscoped().Model(model).Select(names).Rows()
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	typ := reflect.TypeOf(model).Elem()
	var n int64
	for rows.Next() {
		row := reflect.New(typ)
		if err := tx.ScanRows(rows, row.Interface()); err != nil {
			return n, err
		}
		if err := writeRow(ctx, w, cols, row.Elem()); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	if err := w.Flush(); err != nil {
		return n, err
	}
	return n, f.Sync()
}

// writeRow writes one row as a JSON object in column order, ending the line.
func writeRow(ctx context.Context, w *bufio.Writer, cols []*schema.Field, row reflect.Value) error {
	w.WriteByte('{')
	for i, f := range cols {
		if i > 0 {
			w.WriteByte(',')
		}
		key, _ := json.Marshal(f.DBName)
		w.Write(key)
		w.WriteByte(':')
		value, err := json.Marshal(f.ReflectValueOf(ctx, row).Interface())
		if err != nil {
			return fmt.Errorf("encoding column %s: %w", f.DBName, err)
		}
		w.Write(value)
	}
	w.WriteByte('}')
	return w.WriteByte('\n')
}
