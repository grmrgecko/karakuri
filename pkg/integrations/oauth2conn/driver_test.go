package oauth2conn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newDriver builds a driver against a test server from raw config JSON.
func newDriver(t *testing.T, cfg Config) *Driver {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	d, err := New(integrations.ConnModel{Kind: Kind, Config: raw}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err, "a valid config must build a driver")
	return d.(*Driver)
}

// TestClientCredentialsExchange pins the RFC 6749 §4.4 request shape servers
// actually check: a form-encoded POST carrying grant_type and scope, with the
// client id and secret in a basic auth header whose halves are form-urlencoded
// first (§2.3.1) — the encoding several providers reject when it is missing.
// It also pins the cache: a second call inside the token's lifetime must not
// touch the endpoint again.
func TestClientCredentialsExchange(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.PostForm.Get("grant_type"), "the grant rides the form body")
		assert.Equal(t, "read write", r.PostForm.Get("scope"), "scopes are sent space-separated as configured")
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		user, pass, ok := r.BasicAuth()
		require.True(t, ok, "the default auth style is HTTP basic")
		id, _ := url.QueryUnescape(user)
		secret, _ := url.QueryUnescape(pass)
		assert.Equal(t, "my client", id, "the client id form-urlencodes before basic auth")
		assert.Equal(t, "s3cr&t", secret, "the secret form-urlencodes before basic auth")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-1","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)

	d := newDriver(t, Config{
		TokenURL: srv.URL, ClientID: "my client", ClientSecret: "s3cr&t", Scopes: "read write",
	})
	tok, err := d.AccessToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-1", tok)

	tok, err = d.AccessToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-1", tok)
	assert.Equal(t, 1, calls, "a token inside its lifetime is served from cache, not re-exchanged")
}

// TestRefreshGrantFollowsRotation pins the §6 refresh exchange and the
// rotation contract several providers enforce: when the server hands back a
// new refresh token, the next exchange must present that one, because the old
// one has been revoked. The body auth style is exercised here, since it is
// the other place providers read the client credential.
func TestRefreshGrantFollowsRotation(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
		assert.Equal(t, "cid", r.PostForm.Get("client_id"), "the body auth style carries the id in the form")
		got = append(got, r.PostForm.Get("refresh_token"))

		w.Header().Set("Content-Type", "application/json")
		// Rotate on every exchange, and expire immediately so the next
		// AccessToken call exchanges again.
		_, _ = w.Write([]byte(`{"access_token":"tok-` + r.PostForm.Get("refresh_token") + `","expires_in":1,"refresh_token":"rotated"}`))
	}))
	t.Cleanup(srv.Close)

	d := newDriver(t, Config{
		Grant: GrantRefreshToken, TokenURL: srv.URL,
		ClientID: "cid", RefreshToken: "initial", AuthStyle: "body",
	})
	_, err := d.AccessToken(context.Background())
	require.NoError(t, err)
	// Push the cached token past its margin so the next call re-exchanges.
	d.mu.Lock()
	d.expiry = time.Now().Add(-time.Minute)
	d.mu.Unlock()
	_, err = d.AccessToken(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"initial", "rotated"}, got,
		"the second exchange must present the rotated refresh token, not the configured one")
}

// TestErrorReplySurfacesTheServersWords pins the §5.2 failure shape: servers
// answer 400 with a JSON error code and description, and that wording — not a
// bare status — is what the connections panel and the node's error branch
// need to show.
func TestErrorReplySurfacesTheServersWords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"client authentication failed"}`))
	}))
	t.Cleanup(srv.Close)

	d := newDriver(t, Config{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "wrong"})
	_, err := d.AccessToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_client", "the server's error code must reach the operator")
	assert.Contains(t, err.Error(), "client authentication failed", "and so must its description")
	assert.Equal(t, integrations.StateError, d.Status().State, "a refused exchange flips the connection status")
}

// TestExpiresInAsText locks that a token reply whose expires_in is a quoted
// number is accepted and its lifetime honoured. Several token endpoints send
// it as text, and the reply then failed to decode as a whole, so a working
// credential was reported as "token response is not JSON".
func TestExpiresInAsText(t *testing.T) {
	tests := []struct {
		name      string
		expiresIn string
		cached    bool
	}{
		{name: "quoted lifetime", expiresIn: `"3600"`, cached: true},
		{name: "quoted lifetime inside the margin", expiresIn: `"5"`, cached: false},
		{name: "number", expiresIn: `3600`, cached: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":` + tt.expiresIn + `}`))
			}))
			t.Cleanup(srv.Close)

			d := newDriver(t, Config{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "secret"})
			tok, err := d.AccessToken(context.Background())
			require.NoError(t, err, "a lifetime sent as text is still a valid reply")
			assert.Equal(t, "tok", tok)

			_, err = d.AccessToken(context.Background())
			require.NoError(t, err)
			want := 2
			if tt.cached {
				want = 1
			}
			assert.Equal(t, want, calls, "the lifetime read from the reply decides when the token is exchanged again")
		})
	}
}

// TestCanceledExchangeKeepsStatus locks that a token exchange whose caller
// went away, such as a canceled flow run, leaves the connection's status
// alone, while an exchange the endpoint refuses still reports the failure.
func TestCanceledExchangeKeepsStatus(t *testing.T) {
	refuse := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":1}`))
	}))
	t.Cleanup(srv.Close)
	d := newDriver(t, Config{TokenURL: srv.URL, ClientID: "id", ClientSecret: "secret"})

	// A one-second token is always inside the expiry margin, so every call
	// exchanges again. The first proves the endpoint healthy.
	_, err := d.AccessToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, integrations.StateConnected, d.Status().State, "a granted token reports a connected endpoint")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.AccessToken(ctx)
	require.Error(t, err, "the canceled exchange fails for its caller")
	status := d.Status()
	assert.Equal(t, integrations.StateConnected, status.State,
		"a caller's cancel must not mark the connection failed (error: %q)", status.Error)

	refuse = true
	_, err = d.AccessToken(context.Background())
	require.Error(t, err, "the endpoint refused the client")
	assert.Equal(t, integrations.StateError, d.Status().State, "a refusal from the endpoint is the connection's failure")
}
