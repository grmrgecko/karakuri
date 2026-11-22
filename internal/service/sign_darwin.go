//go:build darwin

package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// On macOS the GUI and desktop nodes need the agent trusted for Accessibility,
// and that grant is keyed to the binary's code-signing designated requirement.
// An unsigned or ad-hoc build changes that requirement on every rebuild, so the
// grant is lost on each update and the operator has to grant it again. Signing
// every build with one persistent identity keeps the requirement — an
// identifier plus the certificate's own hash — the same, so the grant, once
// given, survives updates. The identity is self-signed and created once on this
// host, then reused; it is a code-signing key with no other authority, so it
// needs no administrator and is not a trust anchor for anything else.

const (
	signIdentityName = "Karakuri Agent Self-Signed"
	// signIdentifier fixes the designated requirement's identifier so it does
	// not follow the binary's filename or path.
	signIdentifier = "karakuri-agent"
)

// EnsureStableSignature signs the binary with the host's persistent identity,
// creating that identity the first time. It returns a short status for the
// operator and never a fatal error: signing is what makes the Accessibility
// grant persist, not what makes the agent run, so a host that cannot sign still
// installs and works — it just asks for the grant again after the next update.
func EnsureStableSignature(binaryPath string) string {
	security, err := exec.LookPath("security")
	if err != nil {
		return ""
	}
	codesign, err := exec.LookPath("codesign")
	if err != nil {
		return ""
	}

	// A release binary arrives signed with the project's Developer ID and
	// notarized. Its designated requirement (identifier plus team) already
	// holds across updates, and re-signing would void the notarization.
	if developerIDSigned(codesign, binaryPath) {
		return "signed with the release's Developer ID, so the Accessibility grant persists across updates"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	keychain := filepath.Join(home, "Library", "Keychains", "karakuri-signing.keychain-db")
	passFile := filepath.Join(home, ".config", "karakuri", "signing-keychain.pass")

	// Create the identity once.
	if identityHash(security, keychain) == "" {
		if err := createSigningIdentity(security, keychain, passFile); err != nil {
			return fmt.Sprintf("could not create a signing identity, so the macOS Accessibility grant will not survive updates: %v", err)
		}
	}
	hash := identityHash(security, keychain)
	if hash == "" {
		return "could not find the signing identity after creating it, so the agent was not signed"
	}

	pass, err := os.ReadFile(passFile)
	if err != nil {
		return fmt.Sprintf("signing-keychain password is unreadable, so the agent was not signed: %v", err)
	}
	_ = runQuiet(security, "unlock-keychain", "-p", string(pass), keychain)
	// codesign reaches the key only through a keychain on the search list, so
	// the signing keychain is kept on it. This is idempotent and also repairs a
	// list left short by an interrupted first run.
	_ = addToSearchList(security, keychain)

	// Sign by the identity's fingerprint rather than its name, which is
	// unambiguous even if another certificate on the host shares the name.
	if err := runQuiet(codesign, "--force", "--identifier", signIdentifier,
		"-s", hash, "--keychain", keychain, binaryPath); err != nil {
		return fmt.Sprintf("could not sign the agent (stop the service first if it is running), so the Accessibility grant may not survive updates: %v", err)
	}
	return "signed with this host's persistent identity, so the Accessibility grant persists across updates"
}

// developerIDSigned reports whether the binary carries a Developer ID
// Application signature. codesign -d writes the signing chain to stderr, one
// Authority line per certificate.
func developerIDSigned(codesign, binaryPath string) bool {
	out, err := exec.Command(codesign, "-d", "--verbose=2", binaryPath).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Authority=Developer ID Application:")
}

// identityHash returns the SHA-1 fingerprint of the signing identity in the
// keychain, or empty when it is not there. find-identity lists the identity
// even when it is untrusted for the trust policy, which is all codesign needs.
func identityHash(security, keychain string) string {
	out, err := exec.Command(security, "find-identity", "-p", "codesigning", keychain).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, signIdentityName) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if len(field) == 40 && isHex(field) {
				return field
			}
		}
	}
	return ""
}

// isHex reports whether every character is a hexadecimal digit.
func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// createSigningIdentity makes a dedicated keychain and a self-signed
// code-signing certificate in it, and records the keychain password so a later
// update can unlock it without a prompt. The key and certificate are minted in
// Go and imported as PEM — the keychain pairs them into an identity by public
// key on its own, so nothing outside security and codesign is involved.
func createSigningIdentity(security, keychain, passFile string) error {
	passBytes := make([]byte, 16)
	if _, err := rand.Read(passBytes); err != nil {
		return err
	}
	pass := hex.EncodeToString(passBytes)
	if err := os.MkdirAll(filepath.Dir(passFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(passFile, []byte(pass), 0o600); err != nil {
		return err
	}

	// A leftover from a broken earlier run would keep the new one from creating
	// the keychain, so it is cleared first.
	_ = runQuiet(security, "delete-keychain", keychain)
	if err := runQuiet(security, "create-keychain", "-p", pass, keychain); err != nil {
		return err
	}
	// No auto-lock timeout, so an update months later can still reach the key.
	if err := runQuiet(security, "set-keychain-settings", keychain); err != nil {
		return err
	}
	if err := runQuiet(security, "unlock-keychain", "-p", pass, keychain); err != nil {
		return err
	}

	keyPEM, certPEM, err := mintSigningCertificate()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "karakuri-sign")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	key := filepath.Join(tmp, "key.pem")
	crt := filepath.Join(tmp, "cert.pem")
	if err := os.WriteFile(key, keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(crt, certPEM, 0o600); err != nil {
		return err
	}

	if err := runQuiet(security, "import", key, "-k", keychain, "-T", "/usr/bin/codesign", "-A"); err != nil {
		return err
	}
	if err := runQuiet(security, "import", crt, "-k", keychain); err != nil {
		return err
	}
	// Let codesign use the key without a keychain-access prompt.
	if err := runQuiet(security, "set-key-partition-list",
		"-S", "apple-tool:,apple:,codesign:", "-s", "-k", pass, keychain); err != nil {
		return err
	}
	return nil
}

// mintSigningCertificate generates the self-signed code-signing certificate
// and its key, returned as PEM. Ten years matches what the openssl invocation
// it replaced minted; the extended key usage is what codesign requires of an
// identity before it will sign with it.
func mintSigningCertificate() (keyPEM, certPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: signIdentityName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return keyPEM, certPEM, nil
}

// addToSearchList puts the keychain at the front of the user's search list,
// keeping every keychain already on it.
func addToSearchList(security, keychain string) error {
	out, err := exec.Command(security, "list-keychains", "-d", "user").Output()
	if err != nil {
		return fmt.Errorf("reading the keychain search list: %w", err)
	}
	args := []string{"list-keychains", "-d", "user", "-s", keychain}
	for _, line := range strings.Split(string(out), "\n") {
		path := strings.Trim(strings.TrimSpace(line), "\"")
		if path != "" && path != keychain {
			args = append(args, path)
		}
	}
	return runQuiet(security, args...)
}

// runQuiet runs a command and folds its output into any error.
func runQuiet(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("%s: %s", filepath.Base(name), firstLine(detail))
		}
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}

// firstLine trims a tool's output to its first line for an error.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
