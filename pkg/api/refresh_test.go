package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/auth"
)

// TestRefreshTokensWorkOnce pins refresh rotation. A refresh token used to stay
// valid for the session's whole life, so a copied one kept minting access
// tokens alongside its owner. Now a replay after the grace period revokes the
// session for everyone holding it, while a duplicate inside the grace period,
// two tabs refreshing together, still succeeds.
func TestRefreshTokensWorkOnce(t *testing.T) {
	ts, _ := setupTestServer(t)
	login := func() string {
		t.Helper()
		status, resp := doJSON(t, ts, "", http.MethodPost, "/api/auth/login", map[string]any{"username": "admin", "password": "password"})
		require.Equalf(t, http.StatusOK, status, "login must succeed: %v", resp)
		return resp["data"].(map[string]any)["refresh_token"].(string)
	}
	refresh := func(token string) (int, string) {
		t.Helper()
		status, resp := doJSON(t, ts, "", http.MethodPost, "/api/auth/refresh", map[string]any{"refresh_token": token})
		if status != http.StatusOK {
			return status, ""
		}
		return status, resp["data"].(map[string]any)["refresh_token"].(string)
	}

	t.Run("a duplicate inside the grace period succeeds", func(t *testing.T) {
		first := login()
		status, second := refresh(first)
		require.Equal(t, http.StatusOK, status)
		status, again := refresh(first)
		assert.Equal(t, http.StatusOK, status, "a concurrent refresh with the same token must not sign the user out")
		status, _ = refresh(again)
		assert.Equal(t, http.StatusOK, status, "the duplicate's token is the session's current one")
		status, _ = refresh(second)
		assert.Equal(t, http.StatusOK, status, "the first refresh's token is still inside the grace period")
	})

	t.Run("a replay after the grace period revokes the session", func(t *testing.T) {
		grace := auth.RefreshReuseGrace
		auth.RefreshReuseGrace = 0
		t.Cleanup(func() { auth.RefreshReuseGrace = grace })

		first := login()
		status, second := refresh(first)
		require.Equal(t, http.StatusOK, status)
		time.Sleep(time.Millisecond)
		status, _ = refresh(first)
		assert.Equal(t, http.StatusUnauthorized, status, "an exchanged refresh token must not work again")
		status, _ = refresh(second)
		assert.Equal(t, http.StatusUnauthorized, status, "the replay must revoke the session the legitimate holder uses too")
	})
}

// TestPasswordResetEndsExistingSessions locks the rule that a password reset
// shuts out whoever signed in with the old one. The reset the command line
// performs is the recovery for an account somebody else got into, and it used
// to replace the hash and leave every session in place, so a session opened
// with the old password kept working and kept refreshing itself.
func TestPasswordResetEndsExistingSessions(t *testing.T) {
	ts, bearer := setupTestServer(t)

	status, resp := doJSON(t, ts, bearer, http.MethodGet, "/api/auth/me", nil)
	require.Equalf(t, http.StatusOK, status, "the session must work before the reset: %v", resp)

	require.NoError(t, auth.ResetPassword("admin", "a new password"), "the reset must succeed")

	status, _ = doJSON(t, ts, bearer, http.MethodGet, "/api/auth/me", nil)
	assert.Equal(t, http.StatusUnauthorized, status, "a session opened with the old password must end when the password is reset")
	status, _ = doJSON(t, ts, "", http.MethodPost, "/api/auth/login", map[string]any{"username": "admin", "password": "a new password"})
	assert.Equal(t, http.StatusOK, status, "the new password must still sign in")
}
