package credstore

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// key returns a random valid master key for a test.
func key(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

// TestRoundTripAcrossReopen locks the one invariant that matters: a password
// sealed under K comes back byte-for-byte after the store is written to disk and
// reopened from it, which is what proves the on-disk format and the AEAD agree.
// It re-reads from a fresh Store rather than trusting in-memory state, because
// the store's whole job is to survive on disk.
func TestRoundTripAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	k := key(t)

	require.NoError(t, Open(path).Set(k, "alice-desktop", "alice", "s3cr3t-éñ-你好"))

	user, pw, err := Open(path).Get(k, "alice-desktop")
	require.NoError(t, err)
	assert.Equal(t, "alice", user)
	assert.Equal(t, "s3cr3t-éñ-你好", pw, "a non-ASCII password must survive the round trip exactly")

	// The file on disk must not contain the plaintext password anywhere.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "s3cr3t", "the password must not be readable in the store file")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the store file must be 0600")
}

// TestWrongKeyFails pins that a different key cannot open an entry and that the
// failure is the wrong-key error, not a panic or a garbage plaintext. This is
// the property the whole envelope rests on: the ciphertext on the agent is inert
// without the server's key.
func TestWrongKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	require.NoError(t, Open(path).Set(key(t), "alice", "alice", "password"))

	_, _, err := Open(path).Get(key(t), "alice")
	assert.ErrorIs(t, err, ErrWrongKey, "a different key must not decrypt the entry")
}

// TestTamperIsDetected pins that flipping a byte of the stored ciphertext makes
// the entry fail to open rather than returning a corrupted password, since GCM
// authenticates the ciphertext. A silent corruption here could feed a wrong
// string to a login attempt.
func TestTamperIsDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	k := key(t)
	require.NoError(t, Open(path).Set(k, "alice", "alice", "password"))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	// Flip a character inside the base64 ciphertext field.
	i := len(raw) / 2
	mutated := append([]byte{}, raw...)
	if mutated[i] == 'A' {
		mutated[i] = 'B'
	} else {
		mutated[i] = 'A'
	}
	require.NoError(t, os.WriteFile(path, mutated, 0o600))

	_, _, err = Open(path).Get(k, "alice")
	assert.Error(t, err, "a tampered store must not open to a plausible password")
}

// TestAADBindsEntryToItsSlot pins that a ciphertext cannot be lifted from one
// name into another: the name and user are authenticated as additional data, so
// swapping a sealed value into a different slot fails to open even with the
// right key.
func TestAADBindsEntryToItsSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	k := key(t)
	s := Open(path)
	require.NoError(t, s.Set(k, "low-priv", "guest", "guest-pw"))
	require.NoError(t, s.Set(k, "high-priv", "root", "root-pw"))

	// Move guest's sealed value into root's slot by editing the file, keeping
	// root's user. It must not open, because the AAD binds guest's name+user.
	doc, err := Open(path).load()
	require.NoError(t, err)
	doc.Entries["high-priv"] = entry{
		User:   "root",
		Nonce:  doc.Entries["low-priv"].Nonce,
		Cipher: doc.Entries["low-priv"].Cipher,
	}
	require.NoError(t, Open(path).save(doc))

	_, _, err = Open(path).Get(k, "high-priv")
	assert.ErrorIs(t, err, ErrWrongKey, "a ciphertext moved to another slot must not open")
}

// TestListAndRemove covers the management surface: List reports names and users
// without needing the key, and Remove deletes exactly one entry and reports a
// missing one.
func TestListAndRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	k := key(t)
	s := Open(path)
	require.NoError(t, s.Set(k, "b", "bob", "x"))
	require.NoError(t, s.Set(k, "a", "alice", "y"))

	metas, err := s.List()
	require.NoError(t, err)
	require.Len(t, metas, 2)
	assert.Equal(t, "a", metas[0].Name, "List is sorted by name")
	assert.Equal(t, "alice", metas[0].User)

	require.NoError(t, s.Remove("a"))
	metas, err = s.List()
	require.NoError(t, err)
	assert.Len(t, metas, 1)
	assert.ErrorIs(t, s.Remove("a"), ErrNotFound, "removing a gone entry reports not found")
}

// TestRejectsBadKeySize pins that a key of the wrong length is refused before
// the cipher, since the server delivering a short key is a bug worth naming.
func TestRejectsBadKeySize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	err := Open(path).Set(make([]byte, 16), "a", "alice", "pw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")
}
