// Package postgresql is the PostgreSQL time-series connection: SQL queries in
// read-only transactions, information_schema metadata, and a history table
// the writer fills, made a hypertable when TimescaleDB is installed.
package postgresql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Kind is the connection kind identifier.
const Kind = "postgres"

// DefaultTable is the history table written when the connection names none.
const DefaultTable = "karakuri_history"

// maxConns bounds the pool; dashboards and the writer share it.
const maxConns = 4

// retryInterval is how often an unreachable server is probed again.
const retryInterval = 30 * time.Second

var (
	sslModes   = []string{"disable", "prefer", "require", "verify-ca", "verify-full"}
	identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)
)

// Config is the stored connection configuration.
type Config struct {
	Host         string   `json:"host"`
	Port         float64  `json:"port"`
	Database     string   `json:"database"`
	Username     string   `json:"username"`
	Password     string   `json:"password"`
	SSLMode      string   `json:"sslmode"`
	TimeoutS     float64  `json:"timeout_s"`
	WriteEnabled bool     `json:"write_enabled"`
	Table        string   `json:"table"`
	HeartbeatS   *float64 `json:"heartbeat_s"`
	BufferMiB    float64  `json:"buffer_mib"`
}

// Validate checks the settings a connection cannot work without.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required")
	}
	if strings.TrimSpace(c.Database) == "" {
		return errors.New("database is required")
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535 || c.Port != float64(int(c.Port))) {
		return errors.New("port must be between 1 and 65535")
	}
	if c.SSLMode != "" && !contains(sslModes, c.SSLMode) {
		return fmt.Errorf("sslmode must be one of %s", strings.Join(sslModes, ", "))
	}
	if c.Table != "" && !identifier.MatchString(c.Table) {
		return errors.New("table must be a name such as karakuri_history or schema.karakuri_history")
	}
	return nil
}

func (c Config) port() int {
	if c.Port == 0 {
		return 5432
	}
	return int(c.Port)
}

func (c Config) sslMode() string {
	if c.SSLMode == "" {
		return "prefer"
	}
	return c.SSLMode
}

func (c Config) table() string {
	if c.Table == "" {
		return DefaultTable
	}
	return c.Table
}

// poolConfig builds the pool settings. The password goes on the struct
// rather than into the connection string, so no parse error can quote it.
func (c Config) poolConfig() (*pgxpool.Config, error) {
	parts := []string{
		"host=" + dsnValue(c.Host),
		"port=" + strconv.Itoa(c.port()),
		"dbname=" + dsnValue(c.Database),
		"sslmode=" + c.sslMode(),
		"connect_timeout=10",
		"application_name=karakuri",
	}
	if c.Username != "" {
		parts = append(parts, "user="+dsnValue(c.Username))
	}
	cfg, err := pgxpool.ParseConfig(strings.Join(parts, " "))
	if err != nil {
		return nil, fmt.Errorf("invalid connection settings: %s", tsdb.Redact(err.Error()))
	}
	cfg.ConnConfig.Password = c.Password
	cfg.MaxConns = maxConns
	return cfg, nil
}

// dsnValue quotes a keyword/value connection string value.
func dsnValue(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func parseConfig(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("invalid postgres config: %w", err)
	}
	return c, c.Validate()
}

// KindDef returns the PostgreSQL connection kind.
func KindDef() integrations.ConnectionKind {
	sslOptions := make([]flow.Option, 0, len(sslModes))
	for _, m := range sslModes {
		sslOptions = append(sslOptions, flow.Option{Value: m, Label: m})
	}
	spec := []flow.FieldSpec{
		{ID: "host", Label: "Host", Type: flow.FieldString, Required: true, Placeholder: "localhost"},
		{ID: "port", Label: "Port", Type: flow.FieldNumber, Placeholder: "5432"},
		{ID: "database", Label: "Database", Type: flow.FieldString, Required: true},
		{ID: "username", Label: "Username", Type: flow.FieldString},
		{ID: "password", Label: "Password", Type: flow.FieldString, Control: "password"},
		{
			ID: "sslmode", Label: "SSL mode", Type: flow.FieldSelect, Default: "prefer", Options: sslOptions,
			Help: "prefer tries TLS and falls back to plaintext; verify-full checks the certificate and host name.",
		},
		tsdb.TimeoutField(),
		{
			ID: "write_enabled", Label: "Write history", Type: flow.FieldBoolean,
			Help: "Accept forwarded entity history into a table.",
		},
		{
			ID: "table", Label: "History table", Type: flow.FieldString, Placeholder: DefaultTable,
			ShowIf: &flow.ShowIf{Field: "write_enabled", Equals: true},
			Help:   "Created on the first write, as a hypertable when TimescaleDB is installed. May be schema-qualified.",
		},
	}
	spec = append(spec, tsdb.WriteFields("write_enabled", "0")...)
	return integrations.ConnectionKind{
		Kind:       Kind,
		Label:      "PostgreSQL",
		ConfigSpec: spec,
		Secrets:    []string{"password"},
		Factory: func(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
			d, err := New(model, deps)
			if err != nil {
				return nil, err
			}
			return d, nil
		},
		Validate: func(cfg []byte) error {
			_, err := parseConfig(cfg)
			return err
		},
		Test: TestConnection,
	}
}

// TestConnection connects, runs SELECT 1, and when writing is on checks that
// the history table can be created, in a transaction it rolls back.
func TestConnection(ctx context.Context, _ uuid.UUID, raw []byte) error {
	c, err := parseConfig(raw)
	if err != nil {
		return err
	}
	pcfg, err := c.poolConfig()
	if err != nil {
		return err
	}
	pcfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return classify(err)
	}
	defer pool.Close()
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return classify(err)
	}
	if !c.WriteEnabled {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return classify(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, stmt := range createStatements(c.table()) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return classify(err)
		}
	}
	return nil
}

// Driver is a running PostgreSQL connection.
type Driver struct {
	id     uuid.UUID
	cfg    Config
	deps   integrations.Deps
	pool   *pgxpool.Pool
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	status  integrations.Status
	probing bool
	runCtx  context.Context

	// writeMu serializes table creation with the writes that depend on it.
	writeMu sync.Mutex
	ensured bool
}

// New builds a driver. The pool connects lazily, so New does no I/O.
func New(model integrations.ConnModel, deps integrations.Deps) (*Driver, error) {
	c, err := parseConfig(model.Config)
	if err != nil {
		return nil, err
	}
	pcfg, err := c.poolConfig()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pcfg)
	if err != nil {
		return nil, classify(err)
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.PublishStatus == nil {
		deps.PublishStatus = func(integrations.Status) {}
	}
	return &Driver{
		id:     model.ID,
		cfg:    c,
		deps:   deps,
		pool:   pool,
		status: integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}, nil
}

// ID returns the connection ID.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the connection kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the last probed state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Start probes the server in the background and returns.
func (d *Driver) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.runCtx = ctx
	d.cancel = cancel
	d.mu.Unlock()
	d.probe()
	return nil
}

// Stop ends probing and closes the pool.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	d.pool.Close()
}

// probe starts the reachability loop unless one is running. It pings until
// the server answers, every retryInterval.
func (d *Driver) probe() {
	d.mu.Lock()
	if d.probing || d.runCtx == nil || d.runCtx.Err() != nil {
		d.mu.Unlock()
		return
	}
	ctx := d.runCtx
	d.probing = true
	d.done = make(chan struct{})
	done := d.done
	d.mu.Unlock()
	go func() {
		defer close(done)
		defer func() {
			d.mu.Lock()
			d.probing = false
			d.mu.Unlock()
		}()
		for {
			pctx, cancel := context.WithTimeout(ctx, d.QueryTimeout())
			err := d.pool.Ping(pctx)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				d.setStatus(integrations.StateConnected, "")
				return
			}
			d.setStatus(integrations.StateError, classify(err).Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryInterval):
			}
		}
	}()
}

func (d *Driver) setStatus(state, msg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == msg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: msg, Since: time.Now()}
	st := d.status
	d.mu.Unlock()
	if state == integrations.StateError {
		d.deps.Logger.Error("postgres connection unreachable", zap.Stringer("connection", d.id), zap.String("error", msg))
	} else {
		d.deps.Logger.Info("postgres connection reachable", zap.Stringer("connection", d.id))
	}
	d.deps.PublishStatus(st)
}

// observe updates the status from an operation's outcome: an unavailable
// failure marks the connection down and restarts probing.
func (d *Driver) observe(ctx context.Context, err *tsdb.Error) {
	if err == nil {
		d.setStatus(integrations.StateConnected, "")
		return
	}
	// A caller that went away says nothing about the server. A deadline still
	// counts, because a server that stopped answering shows up as one.
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err.Class == tsdb.ClassUnavailable {
		d.setStatus(integrations.StateError, err.Error())
		d.probe()
	}
}

// QueryTimeout returns the configured query timeout.
func (d *Driver) QueryTimeout() time.Duration {
	return tsdb.Seconds(d.cfg.TimeoutS, tsdb.DefaultTimeout)
}

// Languages returns the accepted query languages.
func (d *Driver) Languages() []string { return []string{tsdb.LangSQL} }

// SinkInfo describes the history table writer settings.
func (d *Driver) SinkInfo() tsdb.SinkInfo {
	info := tsdb.SinkInfo{Writable: d.cfg.WriteEnabled, BufferBytes: tsdb.MiB(d.cfg.BufferMiB)}
	if d.cfg.HeartbeatS != nil && *d.cfg.HeartbeatS > 0 {
		info.Heartbeat = time.Duration(*d.cfg.HeartbeatS * float64(time.Second))
	}
	return info
}

// rejectedClasses are SQLSTATE classes for statements the server will refuse
// again unchanged: feature not supported, cardinality, data exceptions,
// constraint violations, invalid transaction state (including a write in a
// read-only transaction), syntax or access errors, check option violations,
// and PL/pgSQL raises.
var rejectedClasses = []string{"0A", "21", "22", "23", "25", "2B", "42", "44", "P0"}

// classify maps a pgx failure to a tsdb error class. Server errors outside
// the rejected classes, connection failures, and timeouts are unavailable so
// the writer keeps the batch.
func classify(err error) *tsdb.Error {
	if err == nil {
		return nil
	}
	var te *tsdb.Error
	if errors.As(err, &te) {
		return te
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		msg := pgErr.Message
		if pgErr.Detail != "" {
			msg += ": " + pgErr.Detail
		}
		msg = fmt.Sprintf("%s (SQLSTATE %s)", msg, pgErr.Code)
		if pgErr.Code == "57014" {
			return &tsdb.Error{Class: tsdb.ClassUnavailable, Message: "timed out: " + msg, Err: err}
		}
		if len(pgErr.Code) >= 2 && contains(rejectedClasses, pgErr.Code[:2]) {
			return &tsdb.Error{Class: tsdb.ClassRejected, Message: tsdb.Redact(msg), Err: err}
		}
		return &tsdb.Error{Class: tsdb.ClassUnavailable, Message: tsdb.Redact(msg), Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &tsdb.Error{Class: tsdb.ClassUnavailable, Message: "timed out", Err: err}
	}
	return tsdb.Unavailable(err)
}

// quoteTable quotes a validated, optionally schema-qualified table name.
func quoteTable(name string) string {
	return pgx.Identifier(strings.Split(name, ".")).Sanitize()
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var (
	_ integrations.Driver = (*Driver)(nil)
	_ tsdb.Querier        = (*Driver)(nil)
	_ tsdb.Sink           = (*Driver)(nil)
	_ tsdb.TimeoutSource  = (*Driver)(nil)
	_ tsdb.Languages      = (*Driver)(nil)
)
