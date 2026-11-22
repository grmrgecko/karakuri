package models

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/go-passwd"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	db "github.com/grmrgecko/karakuri/pkg/database"
)

// ErrAPIKeyNotRecoverable marks a hash-only API key created before raw values
// were stored encrypted for local token commands.
var ErrAPIKeyNotRecoverable = errors.New("api key is not recoverable")

// APIKey represents a long-lived authentication token for external API access.
type APIKey struct {
	ID           uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	UserID       uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id" description:"Owner user ID."`
	User         User       `gorm:"foreignKey:UserID" json:"-"`
	Name         string     `gorm:"not null" json:"name" description:"Key display name."`
	KeyHash      string     `gorm:"not null;uniqueIndex" json:"-"`
	EncryptedKey string     `gorm:"type:text" json:"-"`
	Prefix       string     `gorm:"not null" json:"prefix" description:"Key prefix for identification."`
	ExpiresAt    *time.Time `json:"expires_at,omitempty" description:"Expiration timestamp."`
	LastUsed     *time.Time `json:"last_used,omitempty" description:"Last usage timestamp."`
	CreatedAt    time.Time  `json:"created_at" description:"Creation timestamp."`
	// Limited says the key holds only the permissions in Permissions that its
	// user's roles also grant. An unlimited key holds everything its user does,
	// which is what every key created before limits existed is.
	Limited     bool     `gorm:"not null;default:false" json:"limited" description:"Whether the key holds only its listed permissions."`
	Permissions []string `gorm:"serializer:json;type:json" json:"permissions,omitempty" description:"Permission strings a limited key may use, in the role syntax."`
	// DashboardID makes the key a render key: it holds no permissions and
	// opens only that dashboard's render URLs.
	DashboardID *uuid.UUID `gorm:"type:uuid;index" json:"dashboard_id,omitempty" description:"Dashboard a render key opens the render URLs of."`
}

// IsRenderKey reports whether the key opens one dashboard's render URLs and
// nothing else.
func (m *APIKey) IsRenderKey() bool {
	return m.DashboardID != nil
}

// Generate fills the authentication and recoverable forms of a fresh API key
// and returns the raw bearer token.
func (m *APIKey) Generate() (string, error) {
	raw, hash, prefix, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}
	key, err := apiKeyEncryptionKey()
	if err != nil {
		return "", err
	}
	sealed, err := crypto.Encrypt(key, []byte(raw))
	if err != nil {
		return "", fmt.Errorf("failed to encrypt API key: %w", err)
	}
	m.KeyHash = hash
	m.EncryptedKey = sealed
	m.Prefix = prefix
	return raw, nil
}

// Raw opens the recoverable bearer token stored with the API key.
func (m *APIKey) Raw() (string, error) {
	if m.EncryptedKey == "" {
		return "", ErrAPIKeyNotRecoverable
	}
	key, err := apiKeyEncryptionKey()
	if err != nil {
		return "", err
	}
	raw, err := crypto.Decrypt(key, m.EncryptedKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt API key: %w", err)
	}
	return string(raw), nil
}

// apiKeyEncryptionKey resolves the configured key used for recoverable API
// token values.
func apiKeyEncryptionKey() ([]byte, error) {
	if config.C == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return crypto.KeyFromHex(config.C.Security.EncryptionKey)
}

// GenerateAPIKey produces a random 64-character hex key prefixed with "kk_"
// and returns both the raw key and its salted hash for authentication.
func GenerateAPIKey() (rawKey, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	rawKey = "kk_" + hex.EncodeToString(b)
	hash, err = HashToken(rawKey)
	if err != nil {
		return "", "", "", err
	}
	prefix = rawKey[:8]
	return rawKey, hash, prefix, nil
}

// HashToken produces a SHA-256 crypt hash of a raw token string using go-passwd.
func HashToken(raw string) (string, error) {
	h, err := passwd.SHashSHA256Password(raw)
	if err != nil {
		return "", fmt.Errorf("failed to hash token: %w", err)
	}
	return h, nil
}

// CheckTokenHash verifies a raw token against a stored salted hash. It
// recomputes the hash itself rather than calling go-passwd's check, which
// prints both hashes to the standard logger on every mismatch, and every key
// sharing a prefix is tried on each request.
func CheckTokenHash(hash, raw string) bool {
	settings, err := passwd.NewPasswd(hash)
	if err != nil {
		return false
	}
	computed, err := settings.SHashPassword(raw)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash), []byte(computed)) == 1
}

// FindAPIKeyByPrefix returns all API keys matching the given prefix.
func FindAPIKeyByPrefix(prefix string) ([]APIKey, error) {
	var keys []APIKey
	err := db.DB.Where("prefix = ?", prefix).Find(&keys).Error
	return keys, err
}

// UpdateLastUsed sets the last_used timestamp to now.
func (m *APIKey) UpdateLastUsed() error {
	now := time.Now()
	return db.DB.Model(&APIKey{}).Where("id = ?", m.ID).Update("last_used", now).Error
}
