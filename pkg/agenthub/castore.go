package agenthub

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/ca"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Setting keys for the CA. Roots are a JSON list so a rollover can hold the
// outgoing and incoming root at once; the signer key names which one currently
// signs the server leaf. Each root's private key is encrypted with the
// application key, the same protection stored connection secrets get.
const (
	settingCARoots  = "agent.ca_roots"
	settingCASigner = "agent.ca_signer"

	// Pre-rollover keys, read once and converted.
	settingCACertLegacy = "agent.ca_cert"
	settingCAKeyLegacy  = "agent.ca_key"
)

// Rollover timing. A root is long-lived, so the window to move every agent onto
// its successor is generous: a year of overlap, and a hard promotion a month
// out whether or not every agent showed up, because one dead record must not
// take the whole subsystem down with it.
const (
	caRotateLead      = 365 * 24 * time.Hour
	caPromoteDeadline = 30 * 24 * time.Hour
)

// leafRenewAt is the fraction of a leaf's life remaining at which it is
// reissued. A third leaves an agent weeks of failed check-ins before anything
// actually expires.
const leafRenewAt = 3

// caMaintainInterval is how often a running server checks its own certificate
// and the rollover state. Everything it watches moves on a scale of months, so
// twice a day is frequent enough and costs nothing.
const caMaintainInterval = 12 * time.Hour

// caStore is the host's certificate authority: every root agents may still be
// trusting, which one signs the server leaf, and the server leaf itself. The
// leaf lives on disk beside the rest of the install rather than in memory, so a
// restart does not mint a new one and a process running for months renews the
// one it has.
type caStore struct {
	mu      sync.Mutex
	roots   []*ca.Authority
	signer  int
	dir     string
	certTTL time.Duration
	log     *zap.Logger

	serverCert *tls.Certificate
	serverLeaf *x509.Certificate
}

// storedRoot is one root as persisted: public certificate as-is, private key
// sealed with the application key.
type storedRoot struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

// loadCAStore reads the CA state, creating a first root on a fresh install and
// converting the single-root layout written before rollovers existed.
func loadCAStore(certTTL time.Duration, log *zap.Logger) (*caStore, error) {
	key, err := crypto.KeyFromHex(config.C.Security.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("agent CA needs a configured encryption key: %w", err)
	}
	s := &caStore{
		dir:     filepath.Join(resources.GetDataPath(), "agents"),
		certTTL: certTTL,
		log:     log,
	}

	roots, signerPin, err := readRoots(key)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		root, err := ca.NewAuthority()
		if err != nil {
			return nil, err
		}
		roots = []*ca.Authority{root}
		signerPin = ca.Fingerprint(root.Certificate())
		log.Info("generated agent CA root",
			zap.Time("expires", root.Certificate().NotAfter))
	}
	s.roots = roots
	s.signer = 0
	for i, r := range roots {
		if ca.Fingerprint(r.Certificate()) == signerPin {
			s.signer = i
		}
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s, nil
}

// readRoots loads the stored roots, converting the legacy pair when that is
// what is there. It returns no roots on a fresh install rather than an error.
func readRoots(key []byte) (roots []*ca.Authority, signerPin string, err error) {
	rootsSetting, rootsErr := models.GetSetting(settingCARoots)
	if rootsErr == nil {
		var stored []storedRoot
		if err := json.Unmarshal([]byte(rootsSetting.Value), &stored); err != nil {
			return nil, "", fmt.Errorf("reading stored CA roots: %w", err)
		}
		for _, sr := range stored {
			keyPEM, err := crypto.Decrypt(key, sr.Key)
			if err != nil {
				return nil, "", fmt.Errorf("decrypting agent CA key: %w", err)
			}
			authority, err := ca.LoadAuthority([]byte(sr.Cert), keyPEM)
			if err != nil {
				return nil, "", err
			}
			roots = append(roots, authority)
		}
		if signer, err := models.GetSetting(settingCASigner); err == nil {
			signerPin = signer.Value
		}
		return roots, signerPin, nil
	}
	if rootsErr != gorm.ErrRecordNotFound {
		return nil, "", rootsErr
	}

	// Convert the single-root layout, if this install predates rollovers.
	certSetting, certErr := models.GetSetting(settingCACertLegacy)
	keySetting, keyErr := models.GetSetting(settingCAKeyLegacy)
	if certErr != nil || keyErr != nil {
		if certErr != nil && certErr != gorm.ErrRecordNotFound {
			return nil, "", certErr
		}
		if keyErr != nil && keyErr != gorm.ErrRecordNotFound {
			return nil, "", keyErr
		}
		return nil, "", nil
	}
	keyPEM, err := crypto.Decrypt(key, keySetting.Value)
	if err != nil {
		return nil, "", fmt.Errorf("decrypting agent CA key: %w", err)
	}
	authority, err := ca.LoadAuthority([]byte(certSetting.Value), keyPEM)
	if err != nil {
		return nil, "", err
	}
	return []*ca.Authority{authority}, ca.Fingerprint(authority.Certificate()), nil
}

// persist writes the current roots and signer, and clears the legacy keys once
// they have been converted.
func (s *caStore) persist() error {
	key, err := crypto.KeyFromHex(config.C.Security.EncryptionKey)
	if err != nil {
		return err
	}
	stored := make([]storedRoot, 0, len(s.roots))
	for _, r := range s.roots {
		sealed, err := crypto.Encrypt(key, r.KeyPEM())
		if err != nil {
			return fmt.Errorf("encrypting agent CA key: %w", err)
		}
		stored = append(stored, storedRoot{Cert: string(r.CertPEM()), Key: sealed})
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if err := models.SetSetting(settingCARoots, string(raw)); err != nil {
		return err
	}
	if err := models.SetSetting(settingCASigner, ca.Fingerprint(s.signerRoot().Certificate())); err != nil {
		return err
	}
	_ = models.DeleteSetting(settingCACertLegacy)
	_ = models.DeleteSetting(settingCAKeyLegacy)
	return nil
}

// signerRoot is the root the server leaf is signed by. Callers hold the lock.
func (s *caStore) signerRoot() *ca.Authority { return s.roots[s.signer] }

// issuerRoot is the root agent certificates are signed by: the newest one, so
// every renewal during a rollover moves that agent onto the successor.
func (s *caStore) issuerRoot() *ca.Authority { return s.roots[len(s.roots)-1] }

// Issuer returns the root that signs agent certificates.
func (s *caStore) Issuer() *ca.Authority {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issuerRoot()
}

// Bundle is every root agents should trust, which is what an enrollment or a
// renewal hands them.
func (s *caStore) Bundle() ca.Bundle {
	s.mu.Lock()
	defer s.mu.Unlock()
	certs := make([]*x509.Certificate, 0, len(s.roots))
	for _, r := range s.roots {
		certs = append(certs, r.Certificate())
	}
	return ca.NewBundle(certs...)
}

// Pool trusts every root, so a client leaf signed by either side of a rollover
// verifies.
func (s *caStore) Pool() *x509.CertPool { return s.Bundle().Pool() }

// Rotating reports whether a successor root exists and has not taken over yet.
func (s *caStore) Rotating() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signer != len(s.roots)-1
}

// ServerCertificate returns the host's own leaf, issuing and storing a fresh
// one when there is none, when it is close to expiry, or when the signing root
// changed under it. It is the tls.Config GetCertificate callback, so a server
// running for months renews without a restart and without a handshake ever
// seeing an expired leaf.
func (s *caStore) ServerCertificate() (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serverCert == nil {
		s.loadServerCert()
	}
	if s.serverUsable() {
		return s.serverCert, nil
	}
	return s.issueServerCert()
}

// serverUsable reports whether the cached leaf is still worth presenting.
// Callers hold the lock.
func (s *caStore) serverUsable() bool {
	if s.serverCert == nil || s.serverLeaf == nil {
		return false
	}
	// A leaf from a retired root would be refused by an agent that has already
	// dropped it, so the signer changing means reissue now.
	if _, err := s.serverLeaf.Verify(x509.VerifyOptions{
		Roots:     s.signerRoot().Pool(),
		DNSName:   ca.ServerName,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return false
	}
	return !renewDue(s.serverLeaf, time.Now())
}

// loadServerCert reads a previously issued leaf from disk. A missing or
// unreadable pair is not an error: the caller issues a new one. Callers hold
// the lock.
func (s *caStore) loadServerCert() {
	certPEM, err := os.ReadFile(s.serverCertPath())
	if err != nil {
		return
	}
	keyPEM, err := os.ReadFile(s.serverKeyPath())
	if err != nil {
		return
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		s.log.Warn("stored agent listener certificate is unreadable; issuing a new one", zap.Error(err))
		return
	}
	leaf, err := ca.ParseCertPEM(certPEM)
	if err != nil {
		return
	}
	s.serverCert = &pair
	s.serverLeaf = leaf
}

// issueServerCert mints and stores the host's leaf. Callers hold the lock.
func (s *caStore) issueServerCert() (*tls.Certificate, error) {
	certPEM, keyPEM, err := s.signerRoot().IssueServer(s.certTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing agent listener certificate: %w", err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading agent listener certificate: %w", err)
	}
	leaf, err := ca.ParseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating agent certificate directory: %w", err)
	}
	if err := writeFileAtomic(s.serverKeyPath(), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(s.serverCertPath(), certPEM, 0o644); err != nil {
		return nil, err
	}
	s.serverCert = &pair
	s.serverLeaf = leaf
	s.log.Info("issued agent listener certificate", zap.Time("expires", leaf.NotAfter))
	return &pair, nil
}

func (s *caStore) serverCertPath() string { return filepath.Join(s.dir, "server.pem") }
func (s *caStore) serverKeyPath() string  { return filepath.Join(s.dir, "server-key.pem") }

// renewDue reports whether a leaf has less than a third of its life left.
func renewDue(leaf *x509.Certificate, now time.Time) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return now.After(leaf.NotAfter.Add(-life / leafRenewAt))
}

// writeFileAtomic writes through a temporary file and renames it into place, so
// a reader loading the identity sees either the old file or the new one.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// StartRollover generates the successor root and begins the overlap. The new
// root does not take over signing yet: agents that have not renewed still trust
// only the outgoing one, and handing them a server leaf they cannot verify
// would lock them out of the very connection they renew over.
func (s *caStore) StartRollover() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signer != len(s.roots)-1 {
		return nil // already rolling over
	}
	root, err := ca.NewAuthority()
	if err != nil {
		return err
	}
	s.roots = append(s.roots, root)
	if err := s.persist(); err != nil {
		return err
	}
	s.log.Warn("agent CA rollover started; agents move to the new root as they renew",
		zap.Time("outgoing_expires", s.signerRoot().Certificate().NotAfter),
		zap.Time("incoming_expires", root.Certificate().NotAfter))
	return nil
}

// Maintain runs the rollover state machine: start one when the signing root is
// nearing expiry, promote the successor once every agent that can renew has,
// and drop roots nothing needs any more. migrated answers whether every enabled
// agent now holds a leaf from the given root; it is called outside the lock
// because it reads the database.
func (s *caStore) Maintain(migrated func(issuerFingerprint string) (bool, error)) error {
	// Begin a rollover well before the signing root expires.
	s.mu.Lock()
	rotating := s.signer != len(s.roots)-1
	signerExpiry := s.signerRoot().Certificate().NotAfter
	s.mu.Unlock()
	if !rotating && time.Until(signerExpiry) < caRotateLead {
		if err := s.StartRollover(); err != nil {
			return err
		}
		rotating = true
	}

	// Promote once the agents have moved, or once waiting longer risks the
	// outgoing root expiring under them.
	if rotating {
		s.mu.Lock()
		issuerPin := ca.Fingerprint(s.issuerRoot().Certificate())
		signerExpiry = s.signerRoot().Certificate().NotAfter
		s.mu.Unlock()

		promote := time.Until(signerExpiry) < caPromoteDeadline
		if promote {
			s.log.Warn("promoting the new agent CA root on the deadline; agents that never renewed must re-enroll")
		} else {
			done, err := migrated(issuerPin)
			if err != nil {
				return err
			}
			promote = done
		}
		if promote {
			s.mu.Lock()
			s.signer = len(s.roots) - 1
			err := s.persist()
			s.mu.Unlock()
			if err != nil {
				return err
			}
			s.log.Info("agent CA rollover promoted; the new root now signs the listener certificate")
		}
	}

	// Drop roots that have expired. The signer is never dropped, and neither is
	// the newest, so the bundle always holds something usable.
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	kept := make([]*ca.Authority, 0, len(s.roots))
	signerPin := ca.Fingerprint(s.signerRoot().Certificate())
	for i, r := range s.roots {
		if i == s.signer || i == len(s.roots)-1 || r.Certificate().NotAfter.After(now) {
			kept = append(kept, r)
		} else {
			s.log.Info("dropped an expired agent CA root", zap.Time("expired", r.Certificate().NotAfter))
		}
	}
	if len(kept) == len(s.roots) {
		return nil
	}
	s.roots = kept
	for i, r := range s.roots {
		if ca.Fingerprint(r.Certificate()) == signerPin {
			s.signer = i
		}
	}
	return s.persist()
}

// CAStatus describes the authority for the agents panel.
type CAStatus struct {
	Fingerprint string    `json:"fingerprint" description:"Fingerprint of the root currently signing."`
	ExpiresAt   time.Time `json:"expires_at" description:"When the signing root expires."`
	Rotating    bool      `json:"rotating" description:"Whether a successor root exists and has not taken over yet."`
	Roots       int       `json:"roots" description:"How many roots agents are told to trust."`
	ServerCert  time.Time `json:"server_cert_expires_at" description:"When the listener's own certificate expires."`
}

// Status reports the authority's state, so the panel can show an expiry and a
// rollover in progress rather than leaving a ten-year timer invisible.
func (s *caStore) Status() CAStatus {
	cert, err := s.ServerCertificate()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := CAStatus{
		Fingerprint: ca.Fingerprint(s.signerRoot().Certificate()),
		ExpiresAt:   s.signerRoot().Certificate().NotAfter,
		Rotating:    s.signer != len(s.roots)-1,
		Roots:       len(s.roots),
	}
	if err == nil && cert != nil && s.serverLeaf != nil {
		st.ServerCert = s.serverLeaf.NotAfter
	}
	return st
}
