package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// loadBatch is how many rows one insert carries.
const loadBatch = 500

// loadDatabase creates the current schema on gdb and inserts every table
// dumped under dir/db. Tables load parents first so the foreign keys GORM
// creates on PostgreSQL and MySQL hold at every insert. Columns the current
// models lack are ignored and new columns keep their zero value, which is how
// an older archive meets a newer build; the start-up data migrations then
// run as on any upgrade.
func loadDatabase(ctx context.Context, gdb *gorm.DB, dir string) (map[string]int64, error) {
	if err := gdb.WithContext(ctx).AutoMigrate(models.All()...); err != nil {
		return nil, fmt.Errorf("creating the schema: %w", err)
	}
	order, err := loadOrder(gdb)
	if err != nil {
		return nil, err
	}
	loaded := map[string]int64{}
	for _, m := range order {
		path := filepath.Join(dir, "db", m.schema.Table+".jsonl")
		n, err := loadTable(ctx, gdb, m.schema, m.model, path)
		if err != nil {
			return nil, fmt.Errorf("loading table %s: %w", m.schema.Table, err)
		}
		loaded[m.schema.Table] = n
	}
	if gdb.Name() == "postgres" {
		if err := resetSequences(ctx, gdb, order); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

type orderedModel struct {
	model  any
	schema *schema.Schema
}

// loadOrder sorts models.All() so every table comes after the tables its
// foreign keys point at, keeping registry order where nothing constrains it.
// GORM declares a key from the child (belongs to) or from the parent (has one,
// has many), so both directions become an edge from parent to child.
func loadOrder(gdb *gorm.DB) ([]orderedModel, error) {
	var all []orderedModel
	index := map[string]int{}
	for _, m := range models.All() {
		s, err := parseModel(gdb, m)
		if err != nil {
			return nil, fmt.Errorf("reading the %T model: %w", m, err)
		}
		index[s.Table] = len(all)
		all = append(all, orderedModel{model: m, schema: s})
	}
	parents := make([]map[int]bool, len(all))
	for i := range parents {
		parents[i] = map[int]bool{}
	}
	edge := func(parent, child string) {
		p, okP := index[parent]
		c, okC := index[child]
		if okP && okC && p != c {
			parents[c][p] = true
		}
	}
	for _, m := range all {
		for _, rel := range m.schema.Relationships.BelongsTo {
			edge(rel.FieldSchema.Table, m.schema.Table)
		}
		for _, rel := range m.schema.Relationships.HasOne {
			edge(m.schema.Table, rel.FieldSchema.Table)
		}
		for _, rel := range m.schema.Relationships.HasMany {
			edge(m.schema.Table, rel.FieldSchema.Table)
		}
	}

	placed := make([]bool, len(all))
	var out []orderedModel
	for len(out) < len(all) {
		progressed := false
		for i := range all {
			if placed[i] {
				continue
			}
			ready := true
			for p := range parents[i] {
				if !placed[p] {
					ready = false
					break
				}
			}
			if ready {
				placed[i] = true
				out = append(out, all[i])
				progressed = true
				break
			}
		}
		if !progressed {
			return nil, errors.New("the models' foreign keys form a cycle; no load order exists")
		}
	}
	return out, nil
}

// loadTable inserts one table's rows from path. A missing file is an empty
// table: the archive came from a build that did not have it.
func loadTable(ctx context.Context, gdb *gorm.DB, s *schema.Schema, model any, path string) (int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Rows insert as column maps. A struct insert replaces a zero value with
	// the column's default, so a disabled connection (default:true) would
	// come back enabled; a map writes exactly the columns it names, and a
	// column the archive lacks gets what a new row would. The insert names the
	// table rather than the model: through the model GORM tries to read
	// generated IDs back into the maps and fails.
	insert := gdb.WithContext(ctx).Session(&gorm.Session{SkipHooks: true}).Table(s.Table)
	var batch []map[string]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := insert.Create(batch).Error; err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	r := bufio.NewReaderSize(f, 256<<10)
	var n int64
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			row, derr := decodeRow(ctx, s, model, line)
			if derr != nil {
				return n, fmt.Errorf("row %d: %w", n+1, derr)
			}
			batch = append(batch, row)
			n++
			if len(batch) == loadBatch {
				if err := flush(); err != nil {
					return n, err
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, err
		}
	}
	return n, flush()
}

// decodeRow turns one column-keyed JSON object into column values ready to
// insert. Each column decodes into its field's own Go type, so times, UUIDs,
// and byte columns come back exact on every engine, and a serializer field is
// encoded the way GORM stores it.
func decodeRow(ctx context.Context, s *schema.Schema, model any, line []byte) (map[string]any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	holder := reflect.New(reflect.TypeOf(model).Elem()).Elem()
	row := make(map[string]any, len(raw))
	for col, value := range raw {
		f := s.LookUpField(col)
		if f == nil || f.DBName != col {
			continue
		}
		dst := reflect.New(f.FieldType)
		if err := json.Unmarshal(value, dst.Interface()); err != nil {
			return nil, fmt.Errorf("column %s: %w", col, err)
		}
		v := dst.Elem().Interface()
		if f.Serializer != nil {
			encoded, err := f.Serializer.Value(ctx, f, holder, v)
			if err != nil {
				return nil, fmt.Errorf("column %s: %w", col, err)
			}
			v = encoded
		}
		// SQLite keeps an empty string in a json column; PostgreSQL and
		// MySQL refuse it as invalid JSON. It holds no value, so it loads as
		// NULL, which reads back as the same empty string.
		if s, ok := v.(string); ok && s == "" && strings.EqualFold(f.TagSettings["TYPE"], "json") {
			v = nil
		}
		row[col] = v
	}

	// Columns the archive lacks, from a build older than this one, get the
	// value GORM gives a new row: the declared default or the current time.
	now := time.Now()
	for _, f := range columns(s) {
		if _, ok := row[f.DBName]; ok || (f.PrimaryKey && f.AutoIncrement) {
			continue
		}
		switch {
		case f.AutoCreateTime > 0 || f.AutoUpdateTime > 0:
			row[f.DBName] = now
		case f.DefaultValueInterface != nil:
			row[f.DBName] = f.DefaultValueInterface
		}
	}
	return row, nil
}

// resetSequences moves each integer primary key's PostgreSQL sequence past
// the largest restored ID. Explicit IDs do not advance a sequence there, so
// the next ordinary insert would collide with a restored row.
func resetSequences(ctx context.Context, gdb *gorm.DB, order []orderedModel) error {
	for _, m := range order {
		pk := m.schema.PrioritizedPrimaryField
		if pk == nil || !pk.AutoIncrement {
			continue
		}
		q := fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%q) FROM %q), 0) + 1, false)`,
			m.schema.Table, pk.DBName, pk.DBName, m.schema.Table)
		if err := gdb.WithContext(ctx).Exec(q).Error; err != nil {
			return fmt.Errorf("moving the %s ID sequence: %w", m.schema.Table, err)
		}
	}
	return nil
}
