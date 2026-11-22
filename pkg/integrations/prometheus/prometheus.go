// Package prometheus is the Prometheus connection kind: PromQL queries and
// metadata over the HTTP API, and history writes by remote write 1.0. It
// serves any server speaking that API, such as VictoriaMetrics, Mimir, or
// Thanos.
package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"go.uber.org/zap"
)

// Kind is the connection kind identifier.
const Kind = "prometheus"

// Authentication modes.
const (
	AuthNone   = "none"
	AuthBasic  = "basic"
	AuthBearer = "bearer"
)

// Mask is the connection settings sentinel for an unchanged secret. It must
// equal connops.PasswordSentinel, which the API compares top-level secrets
// against.
const Mask = "••••••"

// retryInterval is how often a connection in the error state is probed again.
const retryInterval = 30 * time.Second

// defaultHeartbeat re-sends forwarded series often enough that Prometheus's
// five-minute staleness window never marks a quiet series stale.
const defaultHeartbeat = 60 * time.Second

// Config is a Prometheus connection's stored settings.
type Config struct {
	URL             string            `json:"url"`
	Auth            string            `json:"auth"`
	Username        string            `json:"username"`
	Password        string            `json:"password"`
	Token           string            `json:"token"`
	Headers         map[string]string `json:"headers"`
	TLSSkipVerify   bool              `json:"tls_skip_verify"`
	TimeoutS        float64           `json:"timeout_s"`
	ScrapeIntervalS float64           `json:"scrape_interval_s"`
	WriteEnabled    bool              `json:"write_enabled"`
	WriteURL        string            `json:"write_url"`
	// HeartbeatS is a pointer so an explicit 0, which turns heartbeats off,
	// differs from unset, which takes the default.
	HeartbeatS *float64 `json:"heartbeat_s"`
	BufferMiB  float64  `json:"buffer_mib"`
}

// Validate checks the settings a driver cannot work without.
func (c Config) Validate() error {
	if err := tsdb.RequireURL("url", c.URL); err != nil {
		return err
	}
	switch c.Auth {
	case "", AuthNone:
	case AuthBasic:
		if c.Username == "" {
			return fmt.Errorf("username is required for basic authentication")
		}
	case AuthBearer:
		if c.Token == "" {
			return fmt.Errorf("token is required for bearer authentication")
		}
	default:
		return fmt.Errorf("auth must be none, basic, or bearer")
	}
	for name := range c.Headers {
		if name == "" || strings.ContainsAny(name, " \t\r\n:") {
			return fmt.Errorf("header name %q is not valid", name)
		}
	}
	if c.WriteEnabled && c.WriteURL != "" {
		if err := tsdb.RequireURL("write_url", c.WriteURL); err != nil {
			return err
		}
	}
	if c.TimeoutS < 0 || c.ScrapeIntervalS < 0 || c.BufferMiB < 0 || (c.HeartbeatS != nil && *c.HeartbeatS < 0) {
		return fmt.Errorf("durations and sizes cannot be negative")
	}
	return nil
}

// writeURL returns the remote write endpoint.
func (c Config) writeURL() string {
	if c.WriteURL != "" {
		return c.WriteURL
	}
	return tsdb.JoinURL(c.URL, "/api/v1/write")
}

func decodeConfig(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("invalid prometheus config: %w", err)
	}
	return c, c.Validate()
}

// KindDef returns the Prometheus connection kind.
func KindDef() integrations.ConnectionKind {
	writeOn := &flow.ShowIf{Field: "write_enabled", Equals: true}
	spec := []flow.FieldSpec{
		{
			ID: "url", Label: "URL", Type: flow.FieldString, Required: true,
			Placeholder: "http://prometheus:9090",
			Help:        "Base URL of the Prometheus HTTP API, without /api/v1. VictoriaMetrics, Mimir, and Thanos work too.",
		},
		{
			ID: "auth", Label: "Authentication", Type: flow.FieldSelect, Default: AuthNone,
			Options: []flow.Option{
				{Value: AuthNone, Label: "None"},
				{Value: AuthBasic, Label: "Basic"},
				{Value: AuthBearer, Label: "Bearer token"},
			},
		},
		{
			ID: "username", Label: "Username", Type: flow.FieldString,
			ShowIf: &flow.ShowIf{Field: "auth", Equals: AuthBasic},
		},
		{
			ID: "password", Label: "Password", Type: flow.FieldString, Control: "password",
			ShowIf: &flow.ShowIf{Field: "auth", Equals: AuthBasic},
		},
		{
			ID: "token", Label: "Token", Type: flow.FieldString, Control: "password",
			ShowIf: &flow.ShowIf{Field: "auth", Equals: AuthBearer},
			Help:   "Sent as an Authorization: Bearer header.",
		},
		{
			ID: "headers", Label: "Headers", Type: flow.FieldJSON, Control: "map",
			Help: "Extra headers sent on every request, such as X-Scope-OrgID for Mimir. Values are stored as secrets.",
		},
		{
			ID: "tls_skip_verify", Label: "Skip TLS verification", Type: flow.FieldBoolean,
			Help: "Accept any server certificate. Use only for self-signed servers on a trusted network.",
		},
		tsdb.TimeoutField(),
		{
			ID: "scrape_interval_s", Label: "Scrape interval (seconds)", Type: flow.FieldNumber,
			Placeholder: "15",
			Help:        "The server's scrape interval. $__rate_interval is never shorter than four of these.",
		},
		{
			ID: "write_enabled", Label: "Write history", Type: flow.FieldBoolean,
			Help: "Send forwarded history by remote write. Prometheus needs --web.enable-remote-write-receiver.",
		},
		{
			ID: "write_url", Label: "Remote write URL", Type: flow.FieldString, ShowIf: writeOn,
			Placeholder: "<url>/api/v1/write",
			Help:        "Leave empty to use /api/v1/write under the URL.",
		},
	}
	spec = append(spec, tsdb.WriteFields("write_enabled", "60")...)
	return integrations.ConnectionKind{
		Kind:          Kind,
		Label:         "Prometheus",
		ConfigSpec:    spec,
		Secrets:       []string{"password", "token"},
		MaskConfig:    MaskConfig,
		RestoreConfig: RestoreConfig,
		Factory:       New,
		Validate: func(cfg []byte) error {
			_, err := decodeConfig(cfg)
			return err
		},
		Test: TestConnection,
	}
}

// MaskConfig replaces every header value with the sentinel, since headers
// such as X-Scope-OrgID or API keys often authenticate.
func MaskConfig(cfg map[string]any) error {
	headers, ok := cfg["headers"].(map[string]any)
	if !ok {
		return nil
	}
	for k, v := range headers {
		if s, ok := v.(string); ok && s != "" {
			headers[k] = Mask
		}
	}
	return nil
}

// RestoreConfig puts stored header values back in place of sentinels the
// client returned unchanged. Headers are matched by name.
func RestoreConfig(cfg, stored map[string]any) error {
	headers, ok := cfg["headers"].(map[string]any)
	if !ok {
		return nil
	}
	old, _ := stored["headers"].(map[string]any)
	for k, v := range headers {
		if s, ok := v.(string); !ok || s != Mask {
			continue
		}
		value, ok := old[k].(string)
		if !ok || value == "" {
			return fmt.Errorf("re-enter the value of header %q", k)
		}
		headers[k] = value
	}
	return nil
}

// Driver is a running Prometheus connection.
type Driver struct {
	id     uuid.UUID
	cfg    Config
	client *http.Client
	deps   integrations.Deps
	log    *zap.Logger

	mu     sync.Mutex
	status integrations.Status
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a driver for one connection.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	cfg, err := decodeConfig(model.Config)
	if err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:     model.ID,
		cfg:    cfg,
		client: tsdb.NewHTTPClient(cfg.TLSSkipVerify),
		deps:   deps,
		log:    log.With(zap.String("kind", Kind), zap.Stringer("connection", model.ID)),
		status: integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}, nil
}

// ID returns the connection ID.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the connection kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the last probe result.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Start probes the server in the background until it answers.
func (d *Driver) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.done = make(chan struct{})
	done := d.done
	d.mu.Unlock()
	go d.supervise(ctx, done)
	return nil
}

// Stop ends background probing.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// supervise publishes reachability and retries while the server is down.
// Queries and writes report their own failures, so a connected driver stops
// probing.
func (d *Driver) supervise(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		pctx, cancel := context.WithTimeout(ctx, d.QueryTimeout())
		err := probe(pctx, d.client, d.cfg)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			d.setStatus(integrations.StateConnected, "")
			d.log.Info("prometheus connection reachable")
			return
		}
		d.setStatus(integrations.StateError, err.Error())
		d.log.Error("prometheus connection unreachable", zap.Error(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryInterval):
		}
	}
}

func (d *Driver) setStatus(state, msg string) {
	st := integrations.Status{State: state, Error: msg, Since: time.Now()}
	d.mu.Lock()
	if d.status.State == state && d.status.Error == msg {
		d.mu.Unlock()
		return
	}
	d.status = st
	d.mu.Unlock()
	if d.deps.PublishStatus != nil {
		d.deps.PublishStatus(st)
	}
}

// QueryTimeout returns the configured query timeout.
func (d *Driver) QueryTimeout() time.Duration {
	return tsdb.Seconds(d.cfg.TimeoutS, tsdb.DefaultTimeout)
}

// ScrapeInterval returns the server's scrape interval for $__rate_interval.
func (d *Driver) ScrapeInterval() time.Duration {
	return tsdb.Seconds(d.cfg.ScrapeIntervalS, tsdb.DefaultScrapeInterval)
}

// Languages returns the query languages the server accepts.
func (d *Driver) Languages() []string { return []string{tsdb.LangPromQL} }

// SinkInfo describes how the history writer sends points here.
func (d *Driver) SinkInfo() tsdb.SinkInfo {
	heartbeat := defaultHeartbeat
	if d.cfg.HeartbeatS != nil {
		heartbeat = time.Duration(*d.cfg.HeartbeatS * float64(time.Second))
	}
	return tsdb.SinkInfo{
		Writable:    d.cfg.WriteEnabled,
		NumericOnly: true,
		Heartbeat:   heartbeat,
		BufferBytes: tsdb.MiB(d.cfg.BufferMiB),
	}
}

// TestConnection checks that the server answers with the configured
// credentials and, with writing on, that it accepts remote write.
func TestConnection(ctx context.Context, _ uuid.UUID, raw []byte) error {
	cfg, err := decodeConfig(raw)
	if err != nil {
		return err
	}
	client := tsdb.NewHTTPClient(cfg.TLSSkipVerify)
	ctx, cancel := context.WithTimeout(ctx, tsdb.Seconds(cfg.TimeoutS, tsdb.DefaultTimeout))
	defer cancel()
	if err := probe(ctx, client, cfg); err != nil {
		return err
	}
	if !cfg.WriteEnabled {
		return nil
	}
	// An empty WriteRequest checks the endpoint and its authentication
	// without storing anything.
	if err := remoteWrite(ctx, client, cfg, nil); err != nil {
		return fmt.Errorf("remote write: %w", err)
	}
	return nil
}

// probe checks reachability and authentication. Servers without the build
// information endpoint answer a trivial query instead.
func probe(ctx context.Context, client *http.Client, cfg Config) error {
	_, err := get(ctx, client, cfg, "/api/v1/status/buildinfo", nil)
	if err == nil {
		return nil
	}
	if e := tsdb.Classify(err); e.Class != tsdb.ClassRejected {
		return err
	}
	_, err = get(ctx, client, cfg, "/api/v1/query", map[string][]string{"query": {"1"}})
	return err
}

// authorize applies the configured headers and credentials. Credentials go
// last so a stray Authorization header cannot replace them.
func authorize(req *http.Request, cfg Config) {
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	switch cfg.Auth {
	case AuthBasic:
		req.SetBasicAuth(cfg.Username, cfg.Password)
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
}
