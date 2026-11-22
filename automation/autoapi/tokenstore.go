package autoapi

import (
	"os"
	"path/filepath"
	"strings"
)

// TokenStore persists the small opaque tokens a backend keeps between runs, the
// RemoteDesktop portal's restore_token being the one that matters: with it a
// second session restores without prompting the user again. The caller decides
// where these live; the automation module never reaches into an agent's
// identity directory itself.
type TokenStore interface {
	Load(key string) (token string, ok bool)
	Save(key, token string) error
}

// fileTokenStore keeps each token in its own file under a directory, named for
// the key. It is the store an agent points at its own data directory.
type fileTokenStore struct {
	dir string
}

// FileTokenStore returns a TokenStore backed by files under dir. The directory
// is created lazily on the first Save.
func FileTokenStore(dir string) TokenStore {
	return fileTokenStore{dir: dir}
}

// tokenPath maps a key to its file, keeping the name filesystem-safe.
func (s fileTokenStore) tokenPath(key string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, key)
	return filepath.Join(s.dir, safe+".token")
}

// Load reads the stored token for key, reporting whether one was present.
func (s fileTokenStore) Load(key string) (string, bool) {
	data, err := os.ReadFile(s.tokenPath(key))
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(string(data))
	return token, token != ""
}

// Save writes the token for key, creating the directory 0700 on first use.
func (s fileTokenStore) Save(key, token string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.tokenPath(key), []byte(token), 0o600)
}
