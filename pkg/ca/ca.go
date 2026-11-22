// Package ca is karakuri's private certificate authority. It is the trust root
// for remote agents: the host generates an Ed25519 root once, signs its own
// server leaf and one client leaf per enrolled agent, and verifies an agent's
// certificate back to that root at every handshake. No public PKI is involved.
//
// The package is pure crypto with no database or network knowledge. Storage of
// the root, the agent records, and the revocation list lives in the agent
// subsystem; policy (which serials are revoked) is passed in as a callback so a
// root loaded here can be verified against state held anywhere.
package ca

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

// spiffePrefix namespaces an agent's identity URI in its certificate SAN. It is
// not a SPIFFE deployment; the scheme just gives the agent id a structured,
// unambiguous home that reading code can match on.
const spiffePrefix = "spiffe://karakuri/agent/"

// ServerName is the name the host's own leaf is issued to, and the name an
// agent asks for in the handshake whatever address it dialed. Trust here comes
// from the CA in the join token, not from DNS, so the name is a constant both
// sides agree on rather than anything the network has to resolve.
const ServerName = "karakuri"

// serialBits bounds the random certificate serial. 128 bits is comfortably
// collision-free for a CA that signs a handful of certs.
const serialBits = 128

// Authority is a loaded root CA: the certificate agents chain to and the key
// that signs leaves. Build one with NewAuthority for a fresh install or
// LoadAuthority from stored PEM on every start after.
type Authority struct {
	cert    *x509.Certificate
	key     ed25519.PrivateKey
	certPEM []byte
	keyPEM  []byte
}

// NewAuthority generates a fresh Ed25519 root, self-signed and long-lived. The
// caller persists CertPEM and KeyPEM (the key encrypted at rest) and reloads
// them with LoadAuthority afterward.
func NewAuthority() (*Authority, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	// The root outlives leaves by a wide margin: rotating it re-enrolls every
	// agent, so it is deliberately rare.
	now := time.Now().Add(-1 * time.Minute)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "karakuri agent CA"},
		NotBefore:             now,
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, fmt.Errorf("self-signing CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing new CA cert: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling CA key: %w", err)
	}
	return &Authority{
		cert:    cert,
		key:     priv,
		certPEM: pemBlock("CERTIFICATE", der),
		keyPEM:  pemBlock("PRIVATE KEY", keyDER),
	}, nil
}

// LoadAuthority reconstructs an Authority from stored PEM. keyPEM is the
// decrypted root key.
func LoadAuthority(certPEM, keyPEM []byte) (*Authority, error) {
	cert, err := parseCert(certPEM)
	if err != nil {
		return nil, fmt.Errorf("loading CA cert: %w", err)
	}
	keyDER, err := decodePEM(keyPEM, "PRIVATE KEY")
	if err != nil {
		return nil, fmt.Errorf("loading CA key: %w", err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, fmt.Errorf("parsing CA key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("CA key is not Ed25519")
	}
	return &Authority{cert: cert, key: key, certPEM: certPEM, keyPEM: keyPEM}, nil
}

// CertPEM returns the root certificate in PEM form. It is public: it rides in
// the join token and becomes the agent's trust store.
func (a *Authority) CertPEM() []byte { return a.certPEM }

// KeyPEM returns the root private key in PEM form. Store it encrypted.
func (a *Authority) KeyPEM() []byte { return a.keyPEM }

// Certificate returns the parsed root, for building a cert pool.
func (a *Authority) Certificate() *x509.Certificate { return a.cert }

// Pool returns a cert pool trusting only this root, for verifying agent client
// certificates.
func (a *Authority) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(a.cert)
	return pool
}

// SignAgent issues a client leaf for one agent from its CSR. The CSR's public
// key and signature are verified; the subject is replaced with the agent's
// assigned id so a client cannot choose its own identity. ttl bounds the leaf's
// life; agents renew before it expires.
func (a *Authority) SignAgent(csrPEM []byte, agentID string, ttl time.Duration) (certPEM []byte, err error) {
	csr, err := parseCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature is invalid: %w", err)
	}
	uri, err := url.Parse(spiffePrefix + agentID)
	if err != nil {
		return nil, fmt.Errorf("building agent URI: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	// The start is backdated for a host clock running slightly behind; the
	// lifetime still counts from issuance.
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: agentID},
		URIs:         []*url.URL{uri},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, csr.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("signing agent cert: %w", err)
	}
	return pemBlock("CERTIFICATE", der), nil
}

// IssueServer mints the host's own server leaf, presented to agents so they can
// verify the host chains to the CA. It generates a fresh key and returns both
// halves. The leaf is always issued to ServerName and to no address: this CA
// signs one server, so an agent that has the CA has already identified it, and
// naming the dial address here would only mean re-enrolling every agent when
// the server moves or answers on a second interface.
func (a *Authority) IssueServer(ttl time.Duration) (certPEM, keyPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating server key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	// The start is backdated for an agent clock running slightly behind; the
	// lifetime still counts from issuance.
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: ServerName},
		DNSNames:     []string{ServerName},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, pub, a.key)
	if err != nil {
		return nil, nil, fmt.Errorf("signing server cert: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling server key: %w", err)
	}
	return pemBlock("CERTIFICATE", der), pemBlock("PRIVATE KEY", keyDER), nil
}

// VerifyAgentChain checks a presented client certificate chain against a set of
// roots and returns the agent id from its SAN. It takes a pool rather than one
// authority because a CA rollover leaves agents holding leaves from either
// root, and both are legitimate until the old one expires. isRevoked, when
// non-nil, rejects a certificate whose serial the operator has revoked. This is
// the body of the tls.Config VerifyPeerCertificate the agent listener installs.
func VerifyAgentChain(rawCerts [][]byte, roots *x509.CertPool, isRevoked func(serial string) bool) (agentID string, err error) {
	if len(rawCerts) == 0 {
		return "", fmt.Errorf("no client certificate presented")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return "", fmt.Errorf("parsing client certificate: %w", err)
	}
	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	for _, extra := range rawCerts[1:] {
		c, err := x509.ParseCertificate(extra)
		if err != nil {
			return "", fmt.Errorf("parsing intermediate certificate: %w", err)
		}
		if opts.Intermediates == nil {
			opts.Intermediates = x509.NewCertPool()
		}
		opts.Intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(opts); err != nil {
		return "", fmt.Errorf("certificate does not chain to a trusted CA: %w", err)
	}
	id, ok := AgentIDFromCert(leaf)
	if !ok {
		return "", fmt.Errorf("certificate carries no agent identity")
	}
	if isRevoked != nil && isRevoked(SerialString(leaf)) {
		return "", fmt.Errorf("agent certificate is revoked")
	}
	return id, nil
}

// AgentIDFromCert extracts the agent id from a leaf's URI SAN.
func AgentIDFromCert(cert *x509.Certificate) (string, bool) {
	for _, u := range cert.URIs {
		if u != nil && len(u.String()) > len(spiffePrefix) && u.String()[:len(spiffePrefix)] == spiffePrefix {
			return u.String()[len(spiffePrefix):], true
		}
	}
	return "", false
}

// Fingerprint identifies one certificate by its exact bytes. Agent records hold
// the fingerprint of the root that signed the leaf they last renewed with,
// which is how a rollover knows who is still on the old CA.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// SerialString renders a certificate serial as a stable lowercase hex string,
// the form the revocation list stores and compares.
func SerialString(cert *x509.Certificate) string {
	return fmt.Sprintf("%x", cert.SerialNumber)
}

// randomSerial draws a positive random certificate serial.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), serialBits)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("drawing serial: %w", err)
	}
	return serial, nil
}
