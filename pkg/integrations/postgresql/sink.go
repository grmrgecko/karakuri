package postgresql

import (
	"context"
	"crypto/sha1" //nolint:gosec // a uniqueness key, not a security boundary
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// createStatements returns the DDL that creates the history table and its
// lookup index. The unique constraint includes time, which hypertables
// require of every unique index.
func createStatements(table string) []string {
	q := quoteTable(table)
	base := table
	if _, t, ok := strings.Cut(table, "."); ok {
		base = t
	}
	index := quoteTable(base + "_metric_time_idx")
	return []string{
		`CREATE TABLE IF NOT EXISTS ` + q + ` (
			time timestamptz NOT NULL,
			metric text NOT NULL,
			labels jsonb NOT NULL DEFAULT '{}',
			labels_hash text NOT NULL,
			value double precision,
			state text,
			UNIQUE (time, metric, labels_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS ` + index + ` ON ` + q + ` (metric, time DESC)`,
	}
}

// ensureTable creates the history table, as a hypertable when TimescaleDB is
// installed. The caller holds writeMu.
func (d *Driver) ensureTable(ctx context.Context) error {
	table := d.cfg.table()
	for _, stmt := range createStatements(table) {
		if _, err := d.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	var timescale bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&timescale)
	if err != nil {
		return err
	}
	if timescale {
		_, err := d.pool.Exec(ctx,
			`SELECT create_hypertable($1::regclass, 'time', if_not_exists => TRUE, migrate_data => TRUE)`,
			quoteTable(table))
		if err != nil {
			return err
		}
	}
	d.ensured = true
	d.deps.Logger.Info("postgres history table ready", zap.Stringer("connection", d.id),
		zap.String("table", table), zap.Bool("hypertable", timescale))
	return nil
}

// insertSQL writes a batch as parallel arrays in one statement. Labels travel
// as text and are cast, since pgx would send a Go string bound to jsonb as
// raw JSON in some versions and as a JSON string in others.
const insertSQL = ` (time, metric, labels, labels_hash, value, state)
SELECT t, m, l::jsonb, h, v, s
FROM unnest($1::timestamptz[], $2::text[], $3::text[], $4::text[], $5::float8[], $6::text[]) AS u(t, m, l, h, v, s)
ON CONFLICT DO NOTHING`

// Write inserts points into the history table, creating it first when
// needed. A point already stored for the same time, metric, and labels is
// skipped, so a spool replay cannot duplicate rows.
func (d *Driver) Write(ctx context.Context, points []tsdb.Point) error {
	var (
		times  []time.Time
		names  []string
		labels []string
		hashes []string
		values []*float64
		states []*string
	)
	for _, p := range points {
		if p.Value == nil && p.State == nil {
			continue
		}
		lj, hash := labelsKey(p.Labels)
		times = append(times, time.UnixMilli(p.Time).UTC())
		names = append(names, p.Metric)
		labels = append(labels, lj)
		hashes = append(hashes, hash)
		values = append(values, p.Value)
		states = append(states, p.State)
	}
	if len(times) == 0 {
		return nil
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	insert := func() error {
		if !d.ensured {
			if err := d.ensureTable(ctx); err != nil {
				return err
			}
		}
		_, err := d.pool.Exec(ctx, `INSERT INTO `+quoteTable(d.cfg.table())+insertSQL,
			times, names, labels, hashes, values, states)
		return err
	}
	err := insert()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		// The table was dropped after it was created; create it again.
		d.ensured = false
		err = insert()
	}
	e := classify(err)
	// For a write, losing the INSERT grant (42501) or landing on a read-only
	// standby after a failover (25006) is the server's state, which an
	// operator fixes; the batch is good and must stay buffered rather than
	// be discarded as malformed.
	if errors.As(err, &pgErr) && (pgErr.Code == "42501" || pgErr.Code == "25006") {
		e = &tsdb.Error{Class: tsdb.ClassUnavailable, Message: e.Message, Err: err}
	}
	d.observe(ctx, e)
	if e != nil {
		return e
	}
	return nil
}

// labelsKey returns the labels as JSON with sorted keys and the hex SHA-1 of
// that JSON, which the unique constraint uses in place of jsonb equality.
func labelsKey(labels map[string]string) (string, string) {
	if labels == nil {
		labels = map[string]string{}
	}
	b, _ := json.Marshal(labels) // encoding/json sorts map keys.
	sum := sha1.Sum(b)           //nolint:gosec // a uniqueness key, not a security boundary
	return string(b), hex.EncodeToString(sum[:])
}
