package models

import (
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIKeyRawValueRoundTrip covers the recoverable copy used by the local
// token command. Authentication still checks the salted hash, while an
// operator with the database and security.encryption_key can retrieve the
// bearer token without keeping it in plaintext at rest. Rows created before
// this format remain valid for authentication but cannot be reconstructed.
func TestAPIKeyRawValueRoundTrip(t *testing.T) {
	previous := config.C
	config.C = &config.Config{}
	config.C.Security.EncryptionKey = "6368616e676520746869732070617373776f726420746f206120736563726574"
	t.Cleanup(func() { config.C = previous })

	key := &APIKey{}
	raw, err := key.Generate()
	require.NoError(t, err, "generating a recoverable API key must succeed with the configured encryption key")

	assert.True(t, CheckTokenHash(key.KeyHash, raw), "the authentication hash must still accept the generated token")
	assert.NotContains(t, key.EncryptedKey, raw, "the recoverable database value must not contain the bearer token in plaintext")

	recovered, err := key.Raw()
	require.NoError(t, err, "the configured encryption key must open the recoverable token")
	assert.Equal(t, raw, recovered, "the token command must print the exact bearer token that authentication accepts")

	_, err = (&APIKey{}).Raw()
	assert.ErrorIs(t, err, ErrAPIKeyNotRecoverable,
		"a hash-only row predating recoverable storage must be identified instead of returning an empty token")
}
