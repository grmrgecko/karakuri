// Package oauth2conn implements the OAuth2 connection kind: a stored
// credential that is exchanged at a token endpoint, cached, and re-exchanged
// on expiry, shared by every node that talks to the same provider. The two
// grants that make sense without a browser are covered — client_credentials
// for machine-to-machine APIs and refresh_token for a token minted once
// through a provider's own consent flow — hand-rolled over HTTP (RFC 6749
// §4.4 and §6) the way the AI drivers speak their wire formats, so no
// provider SDK rides into the binary.
package oauth2conn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"go.uber.org/zap"
)

// Kind is the connection kind identifier.
const Kind = "oauth2"

// Grant names.
const (
	GrantClientCredentials = "client_credentials"
	GrantRefreshToken      = "refresh_token"
)

// probeTimeout bounds the startup and test-hook token fetch.
const probeTimeout = 15 * time.Second

// expiryMargin is refetched-early headroom: a token presented right at its
// expiry loses the race against the clock on the server that issued it.
const expiryMargin = 30 * time.Second

// defaultLifetime stands in when the token response carries no expires_in.
// The spec makes the field optional, and refetching hourly is cheaper than
// trusting an unknown lifetime forever.
const defaultLifetime = time.Hour

// Config is the driver configuration stored (encrypted) on a connection.
type Config struct {
	Grant        string `json:"grant"`
	TokenURL     string `json:"token_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	Scopes       string `json:"scopes"`
	AuthStyle    string `json:"auth_style"`
}

// normalize trims what users paste and fills the defaults.
func normalize(cfg Config) Config {
	cfg.Grant = strings.TrimSpace(cfg.Grant)
	if cfg.Grant == "" {
		cfg.Grant = GrantClientCredentials
	}
	cfg.TokenURL = strings.TrimSpace(cfg.TokenURL)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.RefreshToken = strings.TrimSpace(cfg.RefreshToken)
	cfg.Scopes = strings.TrimSpace(cfg.Scopes)
	if cfg.AuthStyle == "" {
		cfg.AuthStyle = "basic"
	}
	return cfg
}

// validate checks a config beyond required fields.
func validate(cfg Config) error {
	cfg = normalize(cfg)
	u, err := url.Parse(cfg.TokenURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("token_url %q is not an http(s) URL", cfg.TokenURL)
	}
	switch cfg.Grant {
	case GrantClientCredentials:
		if cfg.ClientSecret == "" {
			return fmt.Errorf("the client credentials grant needs a client secret")
		}
	case GrantRefreshToken:
		if cfg.RefreshToken == "" {
			return fmt.Errorf("the refresh token grant needs a refresh token")
		}
	default:
		return fmt.Errorf("unknown grant %q", cfg.Grant)
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("config has no client_id")
	}
	return nil
}

// Driver is one OAuth2 connection. There is no session to supervise — the
// token endpoint is stateless HTTP — so its lifecycle is a startup probe and
// a cancel, and the driver's job is the cache: one valid access token shared
// by every node on the connection, refetched under its own lock when the
// margin runs out.
type Driver struct {
	id   uuid.UUID
	kind string
	cfg  Config

	log           *zap.Logger
	publishStatus func(integrations.Status)
	client        *http.Client

	mu     sync.Mutex
	status integrations.Status
	cancel context.CancelFunc
	// token and expiry are the cache; refreshToken is the current refresh
	// token, which starts as the configured one and follows server rotation.
	// A rotated token lives only in memory, so a provider that both rotates
	// and revokes the old token needs the connection updated after a restart.
	token        string
	expiry       time.Time
	refreshToken string
}

// New builds an OAuth2 driver from a connection model.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid oauth2 config: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	cfg = normalize(cfg)

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:            model.ID,
		kind:          model.Kind,
		cfg:           cfg,
		log:           log,
		publishStatus: deps.PublishStatus,
		client:        &http.Client{Timeout: probeTimeout},
		status:        integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
		refreshToken:  cfg.RefreshToken,
	}, nil
}

// ID returns the connection's database identifier.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return d.kind }

// Status returns the connection's current lifecycle state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// setStatus records a state transition and relays it to the manager. Callers
// must not hold d.mu.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	publish := d.publishStatus
	d.mu.Unlock()

	d.log.Info("oauth2 connection status", zap.String("state", state), zap.String("error", errMsg))
	if publish != nil {
		publish(status)
	}
}

// Start fetches a token once in the background so the status reflects a
// credential the endpoint actually accepts rather than a config that merely
// parsed. After that, expiry drives the refetches.
func (d *Driver) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	go func() {
		probeCtx, done := context.WithTimeout(runCtx, probeTimeout)
		defer done()
		if _, err := d.AccessToken(probeCtx); err != nil && runCtx.Err() == nil {
			d.setStatus(integrations.StateError, err.Error())
		}
	}()
	return nil
}

// Stop marks the connection disabled.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// AccessToken returns a valid access token, exchanging the stored credential
// when the cached one is missing or inside the expiry margin. The lock is
// held across the exchange on purpose: concurrent callers all need the same
// token, and one fetch is what they should share. A failure flips the status
// to error so the connections panel reports what a run just learned; a
// success flips it back.
func (d *Driver) AccessToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	if d.token != "" && time.Now().Before(d.expiry.Add(-expiryMargin)) {
		token := d.token
		d.mu.Unlock()
		return token, nil
	}
	token, err := d.fetchLocked(ctx)
	d.mu.Unlock()

	if err != nil {
		// A caller that went away says nothing about the token endpoint. A
		// deadline still counts, because an endpoint that stopped answering
		// shows up as one.
		if !errors.Is(ctx.Err(), context.Canceled) {
			d.setStatus(integrations.StateError, err.Error())
		}
		return "", err
	}
	d.setStatus(integrations.StateConnected, "")
	return token, nil
}

// tokenResponse is the token endpoint's reply, success and failure fields in
// one shape because servers answer errors with 400 and a JSON body.
type tokenResponse struct {
	AccessToken      string   `json:"access_token"`
	ExpiresIn        lifetime `json:"expires_in"`
	RefreshToken     string   `json:"refresh_token"`
	Error            string   `json:"error"`
	ErrorDescription string   `json:"error_description"`
}

// lifetime is expires_in in seconds. The spec makes it a number, but several
// token endpoints send it quoted, and refusing the whole reply over that would
// turn a working credential into a connection error.
type lifetime float64

// UnmarshalJSON reads a number or a quoted number. Anything else reads as
// zero, which falls back to the default lifetime.
func (l *lifetime) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		*l = 0
		return nil
	}
	*l = lifetime(n)
	return nil
}

// fetchLocked performs one token exchange. Callers hold d.mu.
func (d *Driver) fetchLocked(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {d.cfg.Grant}}
	if d.cfg.Grant == GrantRefreshToken {
		form.Set("refresh_token", d.refreshToken)
	}
	if d.cfg.Scopes != "" {
		form.Set("scope", d.cfg.Scopes)
	}

	// The client credential rides basic auth by default (what the spec says
	// servers must support) or the form body for the servers that only read
	// it there; the config names which, so what a connection does is what it
	// says.
	if d.cfg.AuthStyle == "body" {
		form.Set("client_id", d.cfg.ClientID)
		if d.cfg.ClientSecret != "" {
			form.Set("client_secret", d.cfg.ClientSecret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	if d.cfg.AuthStyle != "body" {
		// RFC 6749 §2.3.1: the id and secret are form-urlencoded before they
		// go into the basic auth header.
		req.SetBasicAuth(url.QueryEscape(d.cfg.ClientID), url.QueryEscape(d.cfg.ClientSecret))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("token endpoint returned %s", resp.Status)
		}
		return "", fmt.Errorf("token response is not JSON: %w", err)
	}
	if resp.StatusCode >= 400 || tr.Error != "" {
		if tr.Error != "" {
			msg := tr.Error
			if tr.ErrorDescription != "" {
				msg += ": " + tr.ErrorDescription
			}
			return "", fmt.Errorf("token endpoint refused the exchange (%s)", msg)
		}
		return "", fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token endpoint answered without an access token")
	}

	d.token = tr.AccessToken
	valid := defaultLifetime
	if tr.ExpiresIn > 0 {
		valid = time.Duration(tr.ExpiresIn) * time.Second
	}
	d.expiry = time.Now().Add(valid)
	// Follow rotation: a server that hands back a new refresh token expects
	// the old one to stop being used.
	if tr.RefreshToken != "" {
		d.refreshToken = tr.RefreshToken
	}
	return d.token, nil
}

// TestConnection verifies a config by performing one token exchange with a
// throwaway driver. Used as the kind's Test hook.
func TestConnection(ctx context.Context, id uuid.UUID, cfgJSON []byte) error {
	var cfg Config
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return fmt.Errorf("invalid oauth2 config: %w", err)
	}
	if err := validate(cfg); err != nil {
		return err
	}
	cfg = normalize(cfg)

	testCtx, done := context.WithTimeout(ctx, probeTimeout)
	defer done()
	d := &Driver{cfg: cfg, log: zap.NewNop(), client: &http.Client{Timeout: probeTimeout}, refreshToken: cfg.RefreshToken}
	d.mu.Lock()
	_, err := d.fetchLocked(testCtx)
	d.mu.Unlock()
	return err
}

// KindDef returns the OAuth2 connection kind: config schema, secrets, the
// driver factory, and the validation and test hooks. Registered by the core
// plugin, since http-request is what consumes it.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "OAuth2",
		ConfigSpec: []flow.FieldSpec{
			{
				ID: "grant", Label: "Grant", Type: flow.FieldSelect,
				Default: GrantClientCredentials,
				Options: []flow.Option{
					{Value: GrantClientCredentials, Label: "Client credentials"},
					{Value: GrantRefreshToken, Label: "Refresh token"},
				},
				Help: "Client credentials is machine-to-machine. Refresh token reuses a token minted once through the provider's own consent flow.",
			},
			{
				ID: "token_url", Label: "Token URL", Type: flow.FieldString,
				Required: true, Placeholder: "https://auth.example.com/oauth/token",
				Help: "The provider's token endpoint.",
			},
			{
				ID: "client_id", Label: "Client ID", Type: flow.FieldString,
				Required: true,
			},
			{
				ID: "client_secret", Label: "Client secret", Type: flow.FieldString,
				Control: "password",
				Help:    "Required for the client credentials grant; a public client on the refresh grant may leave it empty.",
			},
			{
				ID: "refresh_token", Label: "Refresh token", Type: flow.FieldString,
				Control: "password",
				ShowIf:  &flow.ShowIf{Field: "grant", Equals: GrantRefreshToken},
				Help: "Obtained once from the provider's consent flow. If the provider rotates refresh tokens, " +
					"the rotated one is kept in memory only — after a restart this stored one is used again.",
			},
			{
				ID: "scopes", Label: "Scopes", Type: flow.FieldString,
				Placeholder: "read:devices write:devices",
				Help:        "Space-separated scopes sent with the exchange. Empty sends none.",
			},
			{
				ID: "auth_style", Label: "Client authentication", Type: flow.FieldSelect,
				Default: "basic",
				Options: []flow.Option{
					{Value: "basic", Label: "HTTP basic auth"},
					{Value: "body", Label: "In the form body"},
				},
				Help: "Where the client ID and secret ride on the token request. Basic auth is the standard; some providers only read the body.",
			},
		},
		Secrets: []string{"client_secret", "refresh_token"},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid oauth2 config: %w", err)
			}
			return validate(c)
		},
		Test: TestConnection,
	}
}
