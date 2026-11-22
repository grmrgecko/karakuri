// Package credstore is the agent's on-disk credential store, the ciphertext
// half of the envelope: it holds account passwords encrypted under a master key
// the agent never keeps, only fetches from the server per use. Stealing the
// store file yields nothing, because the key is not in it and not anywhere else
// on the host.
//
// Each entry is sealed with AES-256-GCM under the 32-byte key K the server
// supplies, its name and user bound in as additional data so an entry cannot be
// lifted into another slot. The store never retains K: every call takes it,
// uses it, and leaves the caller to zero it.
package credstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// KeySize is the length of the master key K, an AES-256 key.
const KeySize = 32

// ErrNotFound reports that the store has no entry by that name.
var ErrNotFound = errors.New("credstore: no credential by that name")

// ErrWrongKey reports that decryption failed: the key does not match the entry,
// or the file was tampered with. The two are indistinguishable by design — a
// modified ciphertext and a wrong key both fail the GCM tag.
var ErrWrongKey = errors.New("credstore: credential did not decrypt (wrong key or tampered store)")

// entry is one sealed credential on disk. User is plaintext so the store can be
// listed without the key; only the password is sealed.
type entry struct {
	User   string `json:"user"`
	Nonce  string `json:"nonce"`  // base64, per-entry random
	Cipher string `json:"cipher"` // base64, AES-256-GCM sealed password
}

// file is the on-disk document.
type file struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

// Store is the credential store rooted at a file path.
type Store struct {
	path string
}

// Meta names a stored credential without revealing its secret.
type Meta struct {
	Name string `json:"name"`
	User string `json:"user"`
}

// Open returns the store at path. It touches nothing until a call reads or
// writes; a missing file reads as an empty store.
func Open(path string) *Store { return &Store{path: path} }

// DefaultPath is where an agent keeps its credential store, beside the rest of
// its identity under the data directory. The CLI that enrolls a credential and
// the node that unlocks with one both resolve it this way.
func DefaultPath(dir string) string { return filepath.Join(dir, "credentials.json") }

// Set seals password for the account user under name, replacing any existing
// entry. K is used only to encrypt and is not retained. The account password
// never leaves this process in the clear.
func (s *Store) Set(k []byte, name, user, password string) error {
	if err := checkKey(k); err != nil {
		return err
	}
	if name == "" || user == "" {
		return fmt.Errorf("credstore: name and user are required")
	}

	gcm, err := newGCM(k)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("credstore: generating a nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(password), aad(name, user))

	doc, err := s.load()
	if err != nil {
		return err
	}
	doc.Entries[name] = entry{
		User:   user,
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Cipher: base64.StdEncoding.EncodeToString(sealed),
	}
	return s.save(doc)
}

// Get opens the entry named name and returns its account and password. K is
// used only to decrypt and is not retained; the caller zeroes the password when
// done with it.
func (s *Store) Get(k []byte, name string) (user, password string, err error) {
	if err := checkKey(k); err != nil {
		return "", "", err
	}
	doc, err := s.load()
	if err != nil {
		return "", "", err
	}
	e, ok := doc.Entries[name]
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	nonce, err := base64.StdEncoding.DecodeString(e.Nonce)
	if err != nil {
		return "", "", fmt.Errorf("credstore: entry %s has a corrupt nonce: %w", name, err)
	}
	sealed, err := base64.StdEncoding.DecodeString(e.Cipher)
	if err != nil {
		return "", "", fmt.Errorf("credstore: entry %s has a corrupt ciphertext: %w", name, err)
	}
	gcm, err := newGCM(k)
	if err != nil {
		return "", "", err
	}
	// GCM panics on a nonce of the wrong length, so a truncated or hand-edited
	// entry must be refused before Open rather than crash every unlock.
	if len(nonce) != gcm.NonceSize() {
		return "", "", ErrWrongKey
	}
	plain, err := gcm.Open(nil, nonce, sealed, aad(name, e.User))
	if err != nil {
		return "", "", ErrWrongKey
	}
	return e.User, string(plain), nil
}

// List reports the stored credentials without their secrets, so a caller can
// show what is enrolled without the key.
func (s *Store) List() ([]Meta, error) {
	doc, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Meta, 0, len(doc.Entries))
	for name, e := range doc.Entries {
		out = append(out, Meta{Name: name, User: e.User})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Remove deletes the named entry, reporting ErrNotFound if it was not there.
func (s *Store) Remove(name string) error {
	doc, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := doc.Entries[name]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	delete(doc.Entries, name)
	return s.save(doc)
}

// load reads the store file, returning an empty document when it does not exist.
func (s *Store) load() (file, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file{Version: 1, Entries: map[string]entry{}}, nil
	}
	if err != nil {
		return file{}, fmt.Errorf("credstore: reading store: %w", err)
	}
	var doc file
	if err := json.Unmarshal(data, &doc); err != nil {
		return file{}, fmt.Errorf("credstore: parsing store: %w", err)
	}
	if doc.Entries == nil {
		doc.Entries = map[string]entry{}
	}
	return doc, nil
}

// save writes the store atomically at 0600, creating the directory 0700. The
// temp-then-rename keeps a crash from leaving a half-written store.
func (s *Store) save(doc file) error {
	doc.Version = 1
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("credstore: encoding store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("credstore: creating store directory: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("credstore: writing store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("credstore: replacing store: %w", err)
	}
	return nil
}

// newGCM builds the AEAD from the master key.
func newGCM(k []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, fmt.Errorf("credstore: building the cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credstore: building GCM: %w", err)
	}
	return gcm, nil
}

// aad binds a sealed password to its slot, so a ciphertext moved to another
// name or re-pointed at another user fails to open.
func aad(name, user string) []byte {
	return []byte("karakuri-credstore\x00" + name + "\x00" + user)
}

// checkKey rejects a key of the wrong size before it reaches the cipher, where
// the error would be less clear.
func checkKey(k []byte) error {
	if len(k) != KeySize {
		return fmt.Errorf("credstore: master key must be %d bytes, got %d", KeySize, len(k))
	}
	return nil
}
