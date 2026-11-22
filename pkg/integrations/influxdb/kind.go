// Package influxdb is the influxdb connection kind: InfluxQL, Flux, and SQL
// queries against InfluxDB 1, 2, and 3, and line protocol writes for the
// history writer.
package influxdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// Kind is the connection kind identifier.
const Kind = "influxdb"

// InfluxDB major versions the version setting selects.
const (
	V1 = "1"
	V2 = "2"
	V3 = "3"
)

// Config is an influxdb connection's settings.
type Config struct {
	Version         string  `json:"version"`
	URL             string  `json:"url"`
	Database        string  `json:"database"`
	RetentionPolicy string  `json:"retention_policy"`
	Username        string  `json:"username"`
	Password        string  `json:"password"`
	Org             string  `json:"org"`
	Bucket          string  `json:"bucket"`
	Token           string  `json:"token"`
	TLSSkipVerify   bool    `json:"tls_skip_verify"`
	TimeoutS        float64 `json:"timeout_s"`
	WriteEnabled    bool    `json:"write_enabled"`
	// HeartbeatS is a pointer so an explicit 0 (off) differs from unset.
	HeartbeatS *float64 `json:"heartbeat_s"`
	BufferMiB  float64  `json:"buffer_mib"`
}

// Validate checks the settings the selected version needs.
func (c Config) Validate() error {
	if err := tsdb.RequireURL("url", c.URL); err != nil {
		return err
	}
	switch c.Version {
	case V1, V3:
		if c.Database == "" {
			return fmt.Errorf("database is required for InfluxDB %s", c.Version)
		}
	case V2:
		if c.Org == "" || c.Bucket == "" {
			return fmt.Errorf("org and bucket are required for InfluxDB 2")
		}
		if c.Token == "" {
			return fmt.Errorf("token is required for InfluxDB 2")
		}
	default:
		return fmt.Errorf("version must be 1, 2, or 3")
	}
	return nil
}

func parseConfig(cfg []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return c, fmt.Errorf("invalid influxdb config: %w", err)
	}
	return c, c.Validate()
}

// KindDef returns the influxdb connection kind.
func KindDef() integrations.ConnectionKind {
	v1 := &flow.ShowIf{Field: "version", Equals: V1}
	v2 := &flow.ShowIf{Field: "version", Equals: V2}
	v13 := &flow.ShowIf{Field: "version", Equals: []any{V1, V3}}
	v23 := &flow.ShowIf{Field: "version", Equals: []any{V2, V3}}
	spec := []flow.FieldSpec{
		{
			ID: "version", Label: "Version", Type: flow.FieldSelect, Required: true, Default: V2,
			Options: []flow.Option{
				{Value: V1, Label: "InfluxDB 1"},
				{Value: V2, Label: "InfluxDB 2"},
				{Value: V3, Label: "InfluxDB 3"},
			},
			Help: "Major version of the server. InfluxQL works on every version, Flux on 2, and SQL on 3.",
		},
		{
			ID: "url", Label: "URL", Type: flow.FieldString, Required: true,
			Placeholder: "http://influxdb.local:8086",
		},
		{
			ID: "database", Label: "Database", Type: flow.FieldString, ShowIf: v13,
			Placeholder: "home",
		},
		{
			ID: "retention_policy", Label: "Retention policy", Type: flow.FieldString, ShowIf: v1,
			Help: "Leave empty for the database's default policy.",
		},
		{ID: "username", Label: "Username", Type: flow.FieldString, ShowIf: v1},
		{ID: "password", Label: "Password", Type: flow.FieldString, Control: "password", ShowIf: v1},
		{ID: "org", Label: "Organization", Type: flow.FieldString, ShowIf: v2},
		{
			ID: "bucket", Label: "Bucket", Type: flow.FieldString, ShowIf: v2,
			Help: "Bucket written to, and the database InfluxQL queries read through its DBRP mapping.",
		},
		{
			ID: "token", Label: "Token", Type: flow.FieldString, Control: "password", ShowIf: v23,
			Help: "API token. InfluxDB 3 Core started without authentication needs none.",
		},
		{ID: "tls_skip_verify", Label: "Skip TLS verification", Type: flow.FieldBoolean},
		tsdb.TimeoutField(),
		{
			ID: "write_enabled", Label: "Allow writing", Type: flow.FieldBoolean,
			Help: "Let history forwards and write nodes send points to this database.",
		},
	}
	spec = append(spec, tsdb.WriteFields("write_enabled", "0")...)
	return integrations.ConnectionKind{
		Kind:       Kind,
		Label:      "InfluxDB",
		ConfigSpec: spec,
		Secrets:    []string{"password", "token"},
		Factory: func(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
			return New(model, deps)
		},
		Validate: func(cfg []byte) error {
			_, err := parseConfig(cfg)
			return err
		},
		Test: TestConnection,
	}
}

// TestConnection checks reachability and credentials, then, when writing is
// on, sends an empty write so a missing write permission shows without
// storing a point.
func TestConnection(ctx context.Context, _ uuid.UUID, cfg []byte) error {
	c, err := parseConfig(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cl := newClient(c)
	if err := cl.ping(ctx); err != nil {
		return err
	}
	switch c.Version {
	case V1:
		if _, err := cl.influxQL(ctx, "SHOW DATABASES", false); err != nil {
			return err
		}
	case V2:
		q := url.Values{"org": {c.Org}, "name": {c.Bucket}}
		body, err := cl.get(ctx, "/api/v2/buckets", q)
		if err != nil {
			return err
		}
		var res struct {
			Buckets []struct {
				Name string `json:"name"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return tsdb.Rejectedf("decode buckets: %v", err)
		}
		if len(res.Buckets) == 0 {
			return fmt.Errorf("bucket %q not found in org %q", c.Bucket, c.Org)
		}
	case V3:
		// The database exists only after its first write, so a query that
		// names it would fail on a fresh server; SHOW DATABASES checks the
		// token without depending on it.
		if _, err := cl.influxQL(ctx, "SHOW DATABASES", false); err != nil {
			return err
		}
	}
	if c.WriteEnabled && c.Version != V3 {
		// InfluxDB 1 and 2 answer an empty body with 204 after checking
		// write permission. InfluxDB 3 rejects an empty body, so it is not
		// checked.
		if err := cl.write(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

// newClient builds the HTTP side of a connection.
func newClient(c Config) *client {
	return &client{cfg: c, http: tsdb.NewHTTPClient(c.TLSSkipVerify)}
}

// client speaks the InfluxDB HTTP APIs for one configuration.
type client struct {
	cfg  Config
	http *http.Client
}
