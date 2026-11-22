package ca

import (
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnrollmentRoundTrip proves the whole identity path: an agent generates a
// key locally, builds a CSR, the CA signs it, and the resulting client cert
// verifies back to the CA with the assigned id.
func TestEnrollmentRoundTrip(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)

	// The agent side never ships its key.
	keyPEM, err := GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := CreateCSR(keyPEM, "nas.local")
	require.NoError(t, err)

	// The host assigns the id and signs; the CSR's own subject is ignored.
	agentID := "11111111-2222-3333-4444-555555555555"
	certPEM, err := authority.SignAgent(csrPEM, agentID, 90*24*time.Hour)
	require.NoError(t, err)

	leaf, err := parseCert(certPEM)
	require.NoError(t, err)

	got, ok := AgentIDFromCert(leaf)
	require.True(t, ok)
	assert.Equal(t, agentID, got)

	id, err := VerifyAgentChain([][]byte{leaf.Raw}, authority.Pool(), nil)
	require.NoError(t, err)
	assert.Equal(t, agentID, id)
}

// TestVerifyRejectsForeignCert proves a cert from a different CA does not
// verify, which is the whole point of pinning the root.
func TestVerifyRejectsForeignCert(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)
	other, err := NewAuthority()
	require.NoError(t, err)

	keyPEM, err := GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := CreateCSR(keyPEM, "impostor")
	require.NoError(t, err)
	certPEM, err := other.SignAgent(csrPEM, "aaaa", time.Hour)
	require.NoError(t, err)
	leaf, err := parseCert(certPEM)
	require.NoError(t, err)

	_, err = VerifyAgentChain([][]byte{leaf.Raw}, authority.Pool(), nil)
	assert.Error(t, err)
}

// TestRevocation proves a revoked serial is refused even though the cert still
// chains to the CA and has not expired.
func TestRevocation(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)
	keyPEM, err := GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := CreateCSR(keyPEM, "nas")
	require.NoError(t, err)
	certPEM, err := authority.SignAgent(csrPEM, "revoked-agent", time.Hour)
	require.NoError(t, err)
	leaf, err := parseCert(certPEM)
	require.NoError(t, err)

	serial := SerialString(leaf)
	revoked := map[string]bool{serial: true}
	isRevoked := func(s string) bool { return revoked[s] }

	_, err = VerifyAgentChain([][]byte{leaf.Raw}, authority.Pool(), isRevoked)
	assert.ErrorContains(t, err, "revoked")

	// Cleared, it verifies again.
	delete(revoked, serial)
	_, err = VerifyAgentChain([][]byte{leaf.Raw}, authority.Pool(), isRevoked)
	assert.NoError(t, err)
}

// TestLoadAuthorityRoundTrip proves a stored root reloads and still signs certs
// that verify against a pool built from the stored cert.
func TestLoadAuthorityRoundTrip(t *testing.T) {
	original, err := NewAuthority()
	require.NoError(t, err)

	loaded, err := LoadAuthority(original.CertPEM(), original.KeyPEM())
	require.NoError(t, err)

	keyPEM, err := GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := CreateCSR(keyPEM, "x")
	require.NoError(t, err)
	certPEM, err := loaded.SignAgent(csrPEM, "agent-x", time.Hour)
	require.NoError(t, err)
	leaf, err := parseCert(certPEM)
	require.NoError(t, err)

	id, err := VerifyAgentChain([][]byte{leaf.Raw}, original.Pool(), nil)
	require.NoError(t, err)
	assert.Equal(t, "agent-x", id)
}

// TestServerCertIsIssuedToServerName proves the host leaf verifies as the fixed
// name an agent asks for, whatever address it dialed, so no dial address has to
// appear in a certificate.
func TestServerCertIsIssuedToServerName(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)
	certPEM, keyPEM, err := authority.IssueServer(time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, keyPEM)

	leaf, err := parseCert(certPEM)
	require.NoError(t, err)
	assert.Contains(t, leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	assert.Equal(t, []string{ServerName}, leaf.DNSNames)

	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     authority.Pool(),
		DNSName:   ServerName,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	assert.NoError(t, err)
}

// TestAgentCertIsNotAServerCert proves an enrolled agent cannot turn its own
// client certificate around and impersonate the host to another agent, which is
// what lets hostname verification go without a replacement.
func TestAgentCertIsNotAServerCert(t *testing.T) {
	authority, err := NewAuthority()
	require.NoError(t, err)
	keyPEM, err := GenerateAgentKey()
	require.NoError(t, err)
	csrPEM, err := CreateCSR(keyPEM, "impostor")
	require.NoError(t, err)
	certPEM, err := authority.SignAgent(csrPEM, "agent-1", time.Hour)
	require.NoError(t, err)
	leaf, err := parseCert(certPEM)
	require.NoError(t, err)

	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     authority.Pool(),
		DNSName:   ServerName,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	assert.Error(t, err)
}
