package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Connection is a stored integration endpoint (e.g. an MQTT broker). The
// driver-specific configuration is encrypted at rest with the application's
// encryption key; use SetConfig/GetConfig to access it.
type Connection struct {
	ID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name    string    `gorm:"uniqueIndex;not null" json:"name" description:"Display name."`
	Kind    string    `gorm:"not null" json:"kind" description:"Driver kind, e.g. mqtt."`
	Config  string    `gorm:"type:text" json:"-"`
	Enabled bool      `gorm:"default:true" json:"enabled" description:"Whether the connection should be running."`
	// OwnerID is the user who created the connection, admin on it. Empty for
	// one restored from before owners existed, which only managers reach.
	OwnerID   *uuid.UUID `gorm:"type:uuid;index" json:"owner_id,omitempty" description:"User who owns the connection."`
	CreatedAt time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (c *Connection) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// encryptionKey resolves the configured AES key for connection secrets.
func encryptionKey() ([]byte, error) {
	if config.C == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return crypto.KeyFromHex(config.C.Security.EncryptionKey)
}

// SetConfig serializes and encrypts a driver configuration into the model.
func (c *Connection) SetConfig(v any) error {
	key, err := encryptionKey()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to serialize connection config: %w", err)
	}
	sealed, err := crypto.Encrypt(key, raw)
	if err != nil {
		return fmt.Errorf("failed to encrypt connection config: %w", err)
	}
	c.Config = sealed
	return nil
}

// GetConfig decrypts and decodes the stored driver configuration.
func (c *Connection) GetConfig(v any) error {
	raw, err := c.ConfigJSON()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("failed to decode connection config: %w", err)
	}
	return nil
}

// ConfigJSON returns the decrypted configuration JSON bytes. An empty stored
// config decrypts to an empty JSON object.
func (c *Connection) ConfigJSON() ([]byte, error) {
	if c.Config == "" {
		return []byte("{}"), nil
	}
	key, err := encryptionKey()
	if err != nil {
		return nil, err
	}
	raw, err := crypto.Decrypt(key, c.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt connection config: %w", err)
	}
	return raw, nil
}

// FindConnectionByID returns a connection looked up by ID.
func FindConnectionByID(id uuid.UUID) (*Connection, error) {
	var conn Connection
	err := db.DB.Where("id = ?", id).First(&conn).Error
	return &conn, err
}

// SaveConnection persists an updated connection model.
func SaveConnection(c *Connection) error { return db.DB.Save(c).Error }

// ListConnections returns all connections ordered by name.
func ListConnections() ([]*Connection, error) {
	var conns []*Connection
	err := db.DB.Order("name ASC").Find(&conns).Error
	return conns, err
}

// ListEnabledConnections returns all enabled connections ordered by name.
func ListEnabledConnections() ([]*Connection, error) {
	var conns []*Connection
	err := db.DB.Where("enabled = ?", true).Order("name ASC").Find(&conns).Error
	return conns, err
}

// CountConnectionDependencies returns how many flows reference the connection.
func CountConnectionDependencies(id uuid.UUID) (int64, error) {
	var count int64
	err := db.DB.Model(&FlowDependency{}).
		Where("kind = ? AND ref_id = ?", "connection", id.String()).
		Count(&count).Error
	return count, err
}

// ListFlowsDependingOnConnection returns the IDs of flows whose graphs
// reference the connection.
func ListFlowsDependingOnConnection(id uuid.UUID) ([]uuid.UUID, error) {
	var flowIDs []uuid.UUID
	err := db.DB.Model(&FlowDependency{}).
		Where("kind = ? AND ref_id = ?", "connection", id.String()).
		Distinct().Pluck("flow_id", &flowIDs).Error
	return flowIDs, err
}
