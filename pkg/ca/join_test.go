package ca

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJoinTokenRoundTrip proves the operator's single string carries both
// halves back out intact: the CA to trust and the secret to spend.
func TestJoinTokenRoundTrip(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)

	token, err := EncodeJoin(authority.CertPEM(), "s3cret-token")
	require.NoError(t, err)

	join, err := ParseJoin(token)
	require.NoError(t, err)
	assert.Equal(t, "s3cret-token", join.Secret)
	require.Len(t, join.Bundle.Certs, 1)
	assert.Equal(t, authority.Certificate().Raw, join.Bundle.Certs[0].Raw)
	assert.Equal(t, string(authority.CertPEM()), string(join.CAPEM))
}

// TestParseJoinRejectsGarbage proves a mistyped or truncated token fails where
// it is read, not later as a handshake error.
func TestParseJoinRejectsGarbage(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)
	good, err := EncodeJoin(authority.CertPEM(), "secret")
	require.NoError(t, err)

	cases := map[string]string{
		"empty":        "",
		"no separator": "justatoken",
		"no secret":    good[:len(good)-len("secret")],
		"no CA":        ":secret",
		"bad base64":   "not-base64!!:secret",
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseJoin(token)
			assert.Error(t, err)
		})
	}
}

// TestVerifyIssuedCert proves the agent checks what came back before storing
// it: right CA, right key, and an identity in the SAN.
func TestVerifyIssuedCert(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)
	keyPEM, err := GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := CreateCSR(keyPEM, "nas")
	require.NoError(t, err)
	certPEM, err := authority.SignAgent(csrPEM, "agent-7", time.Hour)
	require.NoError(t, err)

	id, err := VerifyIssuedCert(certPEM, keyPEM, authority.Pool())
	require.NoError(t, err)
	assert.Equal(t, "agent-7", id)

	// A certificate from another CA is refused even though it is well formed.
	other, err := NewAuthority()
	require.NoError(t, err)
	foreign, err := other.SignAgent(csrPEM, "agent-7", time.Hour)
	require.NoError(t, err)
	_, err = VerifyIssuedCert(foreign, keyPEM, authority.Pool())
	assert.ErrorContains(t, err, "does not chain")

	// A certificate for someone else's key is refused too.
	otherKey, err := GenerateAgentKey()
	require.NoError(t, err)
	otherCSR, err := CreateCSR(otherKey, "nas")
	require.NoError(t, err)
	wrongKey, err := authority.SignAgent(otherCSR, "agent-7", time.Hour)
	require.NoError(t, err)
	_, err = VerifyIssuedCert(wrongKey, keyPEM, authority.Pool())
	assert.ErrorContains(t, err, "different key")
}
