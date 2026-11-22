package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey returns a fresh random 32-byte key.
func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err, "the test needs a key before it can encrypt anything")
	return key
}

// TestSealedConfigRoundTrip covers the format connection secrets are stored in.
// A sealed value is written to the database and read back by a later process,
// so it has to survive the round trip exactly, hide the plaintext it came
// from, and differ every time — a repeated ciphertext would tell anyone with
// read access which connections share a password.
func TestSealedConfigRoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := []byte(`{"broker_url":"mqtt://localhost:1883","password":"s3cret"}`)

	sealed, err := Encrypt(key, plaintext)
	require.NoError(t, err, "sealing a connection config must succeed")
	assert.NotContains(t, sealed, "s3cret", "the ciphertext must not leak the password it wraps")

	opened, err := Decrypt(key, sealed)
	require.NoError(t, err, "a value sealed by this build must open again")
	assert.Equal(t, plaintext, opened, "the config must come back byte for byte")

	again, err := Encrypt(key, plaintext)
	require.NoError(t, err, "sealing the same config twice must succeed")
	assert.NotEqual(t, sealed, again, "a fresh nonce per seal is what keeps equal secrets from looking equal")
}

// TestDecryptRefusesWhatItCannotOpen pins the failure modes against the
// positive case above: opening must fail loudly rather than return partial or
// zero bytes, since the caller feeds the result straight into a driver config.
// The wrong-key case is the one that matters operationally — it is what a
// rotated encryption key looks like from the reading side.
func TestDecryptRefusesWhatItCannotOpen(t *testing.T) {
	key := testKey(t)
	sealed, err := Encrypt(key, []byte("data"))
	require.NoError(t, err, "the test needs a sealed value to corrupt")

	opened, err := Decrypt(key, sealed)
	require.NoError(t, err, "the matching key must still open it")
	assert.Equal(t, []byte("data"), opened, "the positive case anchors the rejections below")

	cases := []struct {
		name   string
		key    []byte
		sealed string
		why    string
	}{
		{
			name: "a rotated key", key: testKey(t), sealed: sealed,
			why: "this is what every stored secret looks like after the encryption key changes",
		},
		{
			name: "not base64", key: key, sealed: "not base64!!",
			why: "a hand-edited database row must not be read as bytes",
		},
		{
			name: "shorter than the nonce", key: key, sealed: "QUJD",
			why: "a truncated row must fail rather than index past the nonce",
		},
	}
	for _, c := range cases {
		_, err := Decrypt(c.key, c.sealed)
		assert.Errorf(t, err, "decrypting with %s must fail: %s", c.name, c.why)
	}
}

// TestKeyFromHex covers the config value operators paste in. The key is 32
// bytes written as 64 hex characters; anything else has to be refused at
// startup, because a short key accepted here fails later as an unreadable
// database.
func TestKeyFromHex(t *testing.T) {
	raw := testKey(t)
	key, err := KeyFromHex(hex.EncodeToString(raw))
	require.NoError(t, err, "a 64-character hex key is what config gen writes")
	assert.Equal(t, raw, key, "the decoded key must be the bytes the hex spelled")

	for _, bad := range []string{"", "zzzz", "abcd"} {
		_, err := KeyFromHex(bad)
		assert.Errorf(t, err, "the key %q is not 32 bytes of hex and must be refused at startup", bad)
	}
}
