package ca

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
)

// Bundle is the set of roots an agent trusts. It is a set rather than one
// certificate because rolling the CA over means a period where two roots are
// live: agents pick up the successor on their next renewal, and the server
// keeps signing with the old one until they all have.
type Bundle struct {
	Certs []*x509.Certificate
}

// ParseBundle reads one or more concatenated PEM certificates.
func ParseBundle(pemBytes []byte) (Bundle, error) {
	var b Bundle
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return Bundle{}, fmt.Errorf("expected a %q PEM block, got %q", "CERTIFICATE", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return Bundle{}, fmt.Errorf("parsing certificate in bundle: %w", err)
		}
		b.Certs = append(b.Certs, cert)
	}
	if len(b.Certs) == 0 {
		return Bundle{}, fmt.Errorf("no certificate found")
	}
	return b, nil
}

// NewBundle builds a bundle from parsed certificates.
func NewBundle(certs ...*x509.Certificate) Bundle { return Bundle{Certs: certs} }

// PEM renders the bundle, one block per root, in order.
func (b Bundle) PEM() []byte {
	var out []byte
	for _, c := range b.Certs {
		out = append(out, pemBlock("CERTIFICATE", c.Raw)...)
	}
	return out
}

// Pool builds a cert pool trusting every root in the bundle.
func (b Bundle) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	for _, c := range b.Certs {
		pool.AddCert(c)
	}
	return pool
}

// Fingerprint identifies the bundle's exact contents, so an agent can say what
// it holds in a few bytes and the host can answer "nothing to do".
func (b Bundle) Fingerprint() string {
	h := sha256.New()
	for _, c := range b.Certs {
		h.Write(c.Raw)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Contains reports whether a certificate is already in the bundle.
func (b Bundle) Contains(cert *x509.Certificate) bool {
	for _, c := range b.Certs {
		if bytes.Equal(c.Raw, cert.Raw) {
			return true
		}
	}
	return false
}

// AcceptBundle decides whether an offered trust bundle may replace the one an
// agent holds. The rule is that trust is only ever extended, never handed over:
// the offered set has to still contain a root the agent trusts today, so a
// reply can add the successor root and later drop the retired one, but can
// never move an agent onto a CA it has no existing reason to believe. At
// enrollment the join token's CA is what seeds "trusted today".
func AcceptBundle(currentPEM, offeredPEM []byte) (Bundle, error) {
	current, err := ParseBundle(currentPEM)
	if err != nil {
		return Bundle{}, fmt.Errorf("reading current trust bundle: %w", err)
	}
	offered, err := ParseBundle(offeredPEM)
	if err != nil {
		return Bundle{}, fmt.Errorf("reading offered trust bundle: %w", err)
	}
	for _, c := range offered.Certs {
		if current.Contains(c) {
			return offered, nil
		}
	}
	return Bundle{}, fmt.Errorf("offered trust bundle shares no root with the one already trusted")
}
