package ca

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
)

// GenerateAgentKey creates a fresh Ed25519 private key for an agent and returns
// it in PEM form. The agent runs this once at enrollment and never sends the
// key anywhere; only the CSR built from it leaves the machine.
func GenerateAgentKey() (keyPEM []byte, err error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating agent key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent key: %w", err)
	}
	return pemBlock("PRIVATE KEY", der), nil
}

// CreateCSR builds a certificate signing request from an agent's stored key.
// The common name is informational; the host replaces the subject with the
// agent's assigned id when it signs, so the CSR cannot claim an identity.
func CreateCSR(keyPEM []byte, commonName string) (csrPEM []byte, err error) {
	key, err := parseAgentKey(keyPEM)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("creating CSR: %w", err)
	}
	return pemBlock("CERTIFICATE REQUEST", der), nil
}

// certMatchesKey reports whether a certificate was issued for the public half
// of the given private key.
func certMatchesKey(cert *x509.Certificate, keyPEM []byte) (bool, error) {
	priv, err := parseAgentKey(keyPEM)
	if err != nil {
		return false, err
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return false, nil
	}
	return priv.Public().(ed25519.PublicKey).Equal(pub), nil
}

// parseAgentKey decodes an agent's stored PEM private key.
func parseAgentKey(keyPEM []byte) (ed25519.PrivateKey, error) {
	der, err := decodePEM(keyPEM, "PRIVATE KEY")
	if err != nil {
		return nil, fmt.Errorf("reading agent key: %w", err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parsing agent key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("agent key is not Ed25519")
	}
	return key, nil
}

// ParseCertPEM decodes a single PEM CERTIFICATE block into a certificate, for
// callers that need to read a signed leaf's serial or SAN.
func ParseCertPEM(pemBytes []byte) (*x509.Certificate, error) { return parseCert(pemBytes) }

// parseCert decodes a single PEM CERTIFICATE block.
func parseCert(pemBytes []byte) (*x509.Certificate, error) {
	der, err := decodePEM(pemBytes, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// parseCSR decodes a PEM CERTIFICATE REQUEST block.
func parseCSR(pemBytes []byte) (*x509.CertificateRequest, error) {
	der, err := decodePEM(pemBytes, "CERTIFICATE REQUEST")
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificateRequest(der)
}

// pemBlock wraps DER bytes in a PEM block of the given type.
func pemBlock(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

// decodePEM reads the first PEM block and checks its type.
func decodePEM(pemBytes []byte, want string) ([]byte, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if block.Type != want {
		return nil, fmt.Errorf("expected a %q PEM block, got %q", want, block.Type)
	}
	return block.Bytes, nil
}
