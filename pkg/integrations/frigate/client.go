package frigate

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tokenCookie is the cookie Frigate's login sets.
const tokenCookie = "frigate_token"

// Config is a Frigate connection's settings.
type Config struct {
	URL           string `json:"url"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	TLSSkipVerify bool   `json:"tls_skip_verify,omitempty"`
	CACert        string `json:"ca_cert,omitempty"`
}

// validate checks a config before anything dials.
func (c Config) validate() error {
	raw := strings.TrimSpace(c.URL)
	if raw == "" {
		return errors.New("frigate config requires url")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be an http or https address such as http://frigate.local:5000")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("url must be the server's base address with no path")
	}
	if (c.Username == "") != (c.Password == "") {
		return errors.New("set both username and password, or neither")
	}
	if c.CACert != "" {
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CACert)) {
			return errors.New("ca_cert holds no PEM certificate")
		}
	}
	return nil
}

// base returns the URL without a trailing slash.
func (c Config) base() string { return strings.TrimRight(strings.TrimSpace(c.URL), "/") }

// client talks to Frigate's HTTP API, logging in when credentials are set.
type client struct {
	cfg  Config
	http *http.Client

	mu    sync.Mutex
	token string
}

// newClient builds a client with the config's TLS settings. Timeouts are per
// request, because a stream stays open for as long as a viewer watches.
func newClient(cfg Config) *client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSSkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // operator's explicit choice
	}
	if cfg.CACert != "" {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(cfg.CACert))
		tlsCfg.RootCAs = pool
	}
	tr.TLSClientConfig = tlsCfg
	// A stream has no overall timeout, but Frigate must start answering.
	tr.ResponseHeaderTimeout = 15 * time.Second
	return &client{cfg: cfg, http: &http.Client{Transport: tr}}
}

// tlsConfig returns the transport's TLS settings for the websocket dial.
func (c *client) tlsConfig() *tls.Config {
	return c.http.Transport.(*http.Transport).TLSClientConfig
}

// login posts the credentials and keeps the session token.
func (c *client) login(ctx context.Context) error {
	if c.cfg.Username == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"user": c.cfg.Username, "password": c.cfg.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.base()+"/api/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
		return errors.New("login refused: check the username and password")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("logging in: frigate answered %s", resp.Status)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == tokenCookie {
			c.mu.Lock()
			c.token = ck.Value
			c.mu.Unlock()
			return nil
		}
	}
	return errors.New("logging in: frigate set no session cookie")
}

// cookie returns the session cookie header value, empty without login.
func (c *client) cookie() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" {
		return ""
	}
	return tokenCookie + "=" + c.token
}

// get requests a path. A 401 logs in again once and retries, because
// Frigate's session token expires on its own schedule.
func (c *client) get(ctx context.Context, path string, header http.Header) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.base()+path, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range header {
			req.Header[k] = v
		}
		if ck := c.cookie(); ck != "" {
			req.Header.Set("Cookie", ck)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 && c.cfg.Username != "" {
			resp.Body.Close()
			if err := c.login(ctx); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			if c.cfg.Username == "" {
				return nil, errors.New("frigate requires a login; set username and password, or use the internal port 5000")
			}
			return nil, errors.New("login refused: check the username and password")
		}
		return resp, nil
	}
}

// getJSON reads a JSON answer of at most limit bytes.
func (c *client) getJSON(ctx context.Context, path string, limit int64, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.get(ctx, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("%s answered more than %d bytes", path, limit)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: frigate answered %s", path, resp.Status)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: not a Frigate answer: %w", path, err)
	}
	return nil
}

// version reads Frigate's version, which is plain text.
func (c *client) version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.get(ctx, "/api/version", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("not a Frigate server: /api/version answered %s", resp.Status)
	}
	v := strings.TrimSpace(string(body))
	if v == "" || strings.ContainsAny(v, "<{") {
		return "", errors.New("not a Frigate server: /api/version answered no version")
	}
	// Frigate appends the build's commit: 0.18.2-cfa08c5.
	if i := strings.IndexByte(v, '-'); i > 0 {
		v = v[:i]
	}
	return v, nil
}
