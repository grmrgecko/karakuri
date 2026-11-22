// Package identity is what an enrolled agent keeps on disk: the key that never
// leaves the host, the certificate the server signed for it, the trust bundle
// it verifies the server by, and the enrollment and renewal exchanges that
// obtain them. Nothing here is renewed by hand — RenewLoop runs for as long as
// the agent does and the host decides what to hand back.
package identity

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Identity file names inside the agent's data directory. The key never leaves
// this directory; the cert and CA are what the connection presents and trusts.
const (
	fileKey  = "key.pem"
	fileCert = "cert.pem"
	fileCA   = "ca.pem"
	fileMeta = "agent.json"
)

// Identity is what an enrolled agent remembers between runs.
type Identity struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Server    string `json:"server"`
}

// Load reads the stored identity and its key material from dir. It is
// read fresh for every dial, so a renewal that landed since the last connection
// is picked up without restarting the agent.
func Load(dir string) (id Identity, cert tls.Certificate, caPool *x509.CertPool, err error) {
	metaRaw, err := os.ReadFile(filepath.Join(dir, fileMeta))
	if err != nil {
		return id, cert, nil, fmt.Errorf("reading identity: %w (run enroll first)", err)
	}
	if err := json.Unmarshal(metaRaw, &id); err != nil {
		return id, cert, nil, fmt.Errorf("parsing identity: %w", err)
	}

	cert, err = tls.LoadX509KeyPair(filepath.Join(dir, fileCert), filepath.Join(dir, fileKey))
	if err != nil {
		return id, cert, nil, fmt.Errorf("loading certificate: %w", err)
	}

	caRaw, err := os.ReadFile(filepath.Join(dir, fileCA))
	if err != nil {
		return id, cert, nil, fmt.Errorf("reading CA: %w", err)
	}
	caPool = x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caRaw) {
		return id, cert, nil, fmt.Errorf("CA file holds no certificate")
	}
	return id, cert, caPool, nil
}

// IsEnrolled reports whether dir already holds an identity.
func IsEnrolled(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, fileMeta))
	return err == nil
}

// Save writes the key, cert, CA, and metadata into dir, creating it if
// needed. The key is written before it is used, mode 0600.
func Save(dir string, id Identity, keyPEM, certPEM, caPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, fileKey), keyPEM, 0o600); err != nil {
		return fmt.Errorf("writing key: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, fileCert), certPEM, 0o644); err != nil {
		return fmt.Errorf("writing cert: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, fileCA), caPEM, 0o644); err != nil {
		return fmt.Errorf("writing CA: %w", err)
	}
	metaRaw, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, fileMeta), metaRaw, 0o644); err != nil {
		return fmt.Errorf("writing identity: %w", err)
	}
	return nil
}

// StoreRenewal replaces the certificate and trust bundle a renewal produced.
// The key is untouched, since renewal reissues the same identity. The bundle is
// written first: it only ever extends the roots already trusted, so a failure
// between the two writes leaves the old certificate beside a bundle that still
// verifies the host, and the next check reissues the certificate because the
// host compares what the agent presents.
func StoreRenewal(dir string, certPEM, caPEM []byte) error {
	if len(caPEM) > 0 {
		if err := writeAtomic(filepath.Join(dir, fileCA), caPEM, 0o644); err != nil {
			return fmt.Errorf("writing CA: %w", err)
		}
	}
	if len(certPEM) > 0 {
		if err := writeAtomic(filepath.Join(dir, fileCert), certPEM, 0o644); err != nil {
			return fmt.Errorf("writing cert: %w", err)
		}
	}
	return nil
}

// readCA returns the trust bundle currently stored.
func readCA(dir string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, fileCA))
}

// writeAtomic writes through a temporary file and renames it into place, so a
// dial happening at the same moment reads either the old file or the new one,
// never half of one.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// writeKey stores just the generated key, so it exists before the CSR is signed
// and a failed enrollment does not lose it.
func writeKey(dir string, keyPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, fileKey), keyPEM, 0o600)
}

// readKey loads a previously written key, so a retried enrollment reuses it.
func readKey(dir string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, fileKey))
}
