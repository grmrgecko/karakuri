package influxdb

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"go.uber.org/zap"
)

// probeInterval is how often an unreachable server is probed again.
const probeInterval = 30 * time.Second

// Driver is a running influxdb connection. Queries and writes do not depend
// on the probed status; each reports its own failure.
type Driver struct {
	id     uuid.UUID
	cfg    Config
	cl     *client
	log    *zap.Logger
	notify func(integrations.Status)

	mu     sync.Mutex
	status integrations.Status
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a driver from a stored connection.
func New(model integrations.ConnModel, deps integrations.Deps) (*Driver, error) {
	c, err := parseConfig(model.Config)
	if err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:     model.ID,
		cfg:    c,
		cl:     newClient(c),
		log:    log.With(zap.String("connection", model.ID.String()), zap.String("kind", Kind)),
		notify: deps.PublishStatus,
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

// Start launches the reachability probe.
func (d *Driver) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		return nil
	}
	ctx, d.cancel = context.WithCancel(ctx)
	d.done = make(chan struct{})
	go d.probe(ctx, d.done)
	return nil
}

// Stop ends the probe.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.cancel, d.done = nil, nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// probe pings the server until it answers, then stops: later failures show
// up in the queries and writes that hit them.
func (d *Driver) probe(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := d.cl.ping(pctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			d.setStatus(integrations.StateConnected, "")
			d.log.Info("influxdb reachable", zap.String("version", d.cfg.Version))
			return
		}
		d.setStatus(integrations.StateError, err.Error())
		d.log.Error("influxdb unreachable", zap.Error(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(probeInterval):
		}
	}
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
	if d.notify != nil {
		d.notify(st)
	}
}

// QueryTimeout returns the configured query timeout.
func (d *Driver) QueryTimeout() time.Duration {
	return tsdb.Seconds(d.cfg.TimeoutS, tsdb.DefaultTimeout)
}

// Languages lists the query languages of the configured version.
func (d *Driver) Languages() []string {
	switch d.cfg.Version {
	case V2:
		return []string{tsdb.LangInfluxQL, tsdb.LangFlux}
	case V3:
		return []string{tsdb.LangInfluxQL, tsdb.LangSQL}
	}
	return []string{tsdb.LangInfluxQL}
}

// SinkInfo describes the connection as a history destination.
func (d *Driver) SinkInfo() tsdb.SinkInfo {
	var hb time.Duration
	if d.cfg.HeartbeatS != nil && *d.cfg.HeartbeatS > 0 {
		hb = time.Duration(*d.cfg.HeartbeatS * float64(time.Second))
	}
	return tsdb.SinkInfo{
		Writable:    d.cfg.WriteEnabled,
		Heartbeat:   hb,
		BufferBytes: tsdb.MiB(d.cfg.BufferMiB),
	}
}

// Write sends points as one line protocol request.
func (d *Driver) Write(ctx context.Context, points []tsdb.Point) error {
	if !d.cfg.WriteEnabled {
		return tsdb.Unsupportedf("writing is turned off for this connection")
	}
	body := encodeLines(points)
	if len(body) == 0 {
		return nil
	}
	if err := d.cl.write(ctx, body); err != nil {
		return tsdb.WriteError(err)
	}
	d.log.Debug("influxdb write", zap.Int("points", len(points)), zap.Int("bytes", len(body)))
	return nil
}

// auth sets the version's credentials on a request.
func (c *client) auth(req *http.Request) {
	switch c.cfg.Version {
	case V1:
		if c.cfg.Username != "" || c.cfg.Password != "" {
			req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
		}
	case V2:
		req.Header.Set("Authorization", "Token "+c.cfg.Token)
	case V3:
		if c.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		}
	}
}

func (c *client) do(ctx context.Context, method, path string, q url.Values, body []byte, header map[string]string) ([]byte, error) {
	u := tsdb.JoinURL(c.cfg.URL, path)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, tsdb.Rejectedf("build request: %v", err)
	}
	c.auth(req)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return tsdb.Do(c.http, req)
}

func (c *client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, q, nil, nil)
}

// ping checks that the server answers, without credentials on v1 where
// /ping is open, and through /health on v2 and v3.
func (c *client) ping(ctx context.Context) error {
	path := "/health"
	if c.cfg.Version == V1 {
		path = "/ping"
	}
	_, err := c.get(ctx, path, nil)
	return err
}

// write posts line protocol to the version's write endpoint at millisecond
// precision.
func (c *client) write(ctx context.Context, body []byte) error {
	var path string
	q := url.Values{}
	switch c.cfg.Version {
	case V1:
		path = "/write"
		q.Set("db", c.cfg.Database)
		if c.cfg.RetentionPolicy != "" {
			q.Set("rp", c.cfg.RetentionPolicy)
		}
		q.Set("precision", "ms")
	case V2:
		path = "/api/v2/write"
		q.Set("org", c.cfg.Org)
		q.Set("bucket", c.cfg.Bucket)
		q.Set("precision", "ms")
	case V3:
		path = "/api/v3/write_lp"
		q.Set("db", c.cfg.Database)
		q.Set("precision", "millisecond")
	}
	_, err := c.do(ctx, http.MethodPost, path, q, body, map[string]string{"Content-Type": "text/plain; charset=utf-8"})
	return err
}
