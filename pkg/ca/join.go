package ca

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
)

// joinSeparator divides a join token's two halves. Neither half's alphabet
// contains it, so one split is unambiguous.
const joinSeparator = ":"

// JoinToken is the single string an operator carries to a host: the roots the
// server must chain to, then the one-time enrollment secret. The trust anchor
// travels with the secret because the agent has nothing pre-installed, and a CA
// it took from the server's own reply would be no anchor at all. It carries the
// whole bundle rather than the signing root alone so a token minted during a CA
// rollover still verifies the server after the successor takes over.
type JoinToken struct {
	Bundle Bundle
	CAPEM  []byte
	Secret string
}

// EncodeJoin renders a join token from the trust bundle and the raw enrollment
// secret. The bundle is carried as URL-safe base64 so the whole string survives
// a shell argument, a YAML scalar, and a copy button.
func EncodeJoin(caPEM []byte, secret string) (string, error) {
	bundle, err := ParseBundle(caPEM)
	if err != nil {
		return "", fmt.Errorf("reading CA for join token: %w", err)
	}
	if secret == "" {
		return "", fmt.Errorf("join token needs an enrollment secret")
	}
	return base64.RawURLEncoding.EncodeToString(bundle.PEM()) + joinSeparator + secret, nil
}

// ParseJoin splits and decodes a join token. A token that does not carry a
// readable CA is rejected here rather than at the handshake, where the failure
// would read as a network problem.
func ParseJoin(token string) (JoinToken, error) {
	encoded, secret, ok := strings.Cut(strings.TrimSpace(token), joinSeparator)
	if !ok || encoded == "" || secret == "" {
		return JoinToken{}, fmt.Errorf("malformed join token: expected <ca>%s<secret>", joinSeparator)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return JoinToken{}, fmt.Errorf("decoding CA from join token: %w", err)
	}
	bundle, err := ParseBundle(raw)
	if err != nil {
		return JoinToken{}, fmt.Errorf("parsing CA from join token: %w", err)
	}
	for _, cert := range bundle.Certs {
		if !cert.IsCA {
			return JoinToken{}, fmt.Errorf("join token carries a certificate that is not a CA")
		}
	}
	return JoinToken{Bundle: bundle, CAPEM: bundle.PEM(), Secret: secret}, nil
}

// Pool returns a cert pool trusting the token's roots, which is what the agent
// verifies the server with while enrolling. Hostnames play no part: the server
// leaf is always issued to ServerName, and these roots sign exactly one server.
func (j JoinToken) Pool() *x509.CertPool { return j.Bundle.Pool() }

// VerifyIssuedCert checks a certificate the host signed before the agent stores
// it: it must chain to a root the agent trusts, name an agent, and belong to
// the key this agent generated. A reply that fails any of the three is not an
// identity worth keeping. It guards enrollment and every renewal after.
func VerifyIssuedCert(certPEM, keyPEM []byte, roots *x509.CertPool) (agentID string, err error) {
	leaf, err := parseCert(certPEM)
	if err != nil {
		return "", fmt.Errorf("reading issued certificate: %w", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return "", fmt.Errorf("issued certificate does not chain to a trusted CA: %w", err)
	}
	id, ok := AgentIDFromCert(leaf)
	if !ok {
		return "", fmt.Errorf("issued certificate carries no agent identity")
	}
	match, err := certMatchesKey(leaf, keyPEM)
	if err != nil {
		return "", err
	}
	if !match {
		return "", fmt.Errorf("issued certificate was signed for a different key")
	}
	return id, nil
}
