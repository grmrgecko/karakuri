package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Agent is a remote host enrolled to run node executions for karakuri. Its id
// is baked into the client certificate and into the node types it contributes
// (agent.<id>.<node>), so a rename never touches a stored flow. The certificate
// serial ties the running connection back to this record; revoking the agent
// records that serial and disables the row.
type Agent struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier, baked into the client cert and node types."`
	Name        string    `gorm:"uniqueIndex;not null" json:"name" description:"Display name and palette folder; defaults to the reported hostname."`
	Description string    `json:"description,omitempty" description:"Operator notes."`
	// CertSerial is the serial of the leaf the agent currently authenticates
	// with, replaced on renewal. CertIssuer is the fingerprint of the root that
	// signed it, and CertNotAfter is when the agent has to have renewed by.
	// PresentedIssuer is the root behind the certificate the agent last
	// authenticated with. Issuance can be lost in delivery or local storage,
	// so a CA rollover counts this column, which only the agent's own
	// handshake can move.
	CertSerial      string     `json:"cert_serial,omitempty" description:"Serial of the current client certificate."`
	CertIssuer      string     `json:"cert_issuer,omitempty" description:"Fingerprint of the CA root that signed the current certificate."`
	PresentedIssuer string     `json:"presented_issuer,omitempty" description:"Fingerprint of the CA root behind the certificate the agent last connected with."`
	CertNotAfter    *time.Time `json:"cert_not_after,omitempty" description:"When the current client certificate expires."`
	Hostname        string     `json:"hostname,omitempty" description:"Hostname the agent reported at enrollment."`
	OS              string     `json:"os,omitempty" description:"Operating system reported by the agent."`
	Arch            string     `json:"arch,omitempty" description:"CPU architecture reported by the agent."`
	Version         string     `json:"version,omitempty" description:"Agent binary version, reported on connect."`
	Enabled         bool       `gorm:"default:true" json:"enabled" description:"Whether the agent may connect."`
	// CredentialKey is the agent's credential-store master key, sealed under
	// security.encryption_key. It is the envelope key: the agent keeps its
	// credential store encrypted with this and fetches it on demand, so the key
	// lives here and the ciphertext lives on the agent, and neither is useful
	// without the other. Never serialized to a client.
	CredentialKey string     `json:"-" description:"Sealed credential-store master key for this agent."`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty" description:"When the agent last connected or sent a heartbeat."`
	// OwnerID is the user who minted the token the agent enrolled with, admin
	// on it. Empty for one enrolled before owners existed, which only
	// managers reach.
	OwnerID   *uuid.UUID `gorm:"type:uuid;index" json:"owner_id,omitempty" description:"User who owns the agent."`
	CreatedAt time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (a *Agent) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// FindAgentByID returns an agent by id.
func FindAgentByID(id uuid.UUID) (*Agent, error) {
	var agent Agent
	err := db.DB.Where("id = ?", id).First(&agent).Error
	return &agent, err
}

// FindAgentByName returns an agent by name.
func FindAgentByName(name string) (*Agent, error) {
	var agent Agent
	err := db.DB.Where("name = ?", name).First(&agent).Error
	return &agent, err
}

// ListAgents returns every agent ordered by name.
func ListAgents() ([]*Agent, error) {
	var agents []*Agent
	err := db.DB.Order("name ASC").Find(&agents).Error
	return agents, err
}

// SaveAgent writes an agent row.
func SaveAgent(a *Agent) error { return db.DB.Save(a).Error }

// RecordAgentCert stores what an agent was just issued, at enrollment and at
// every renewal. It writes the three certificate columns and nothing else, so a
// renewal arriving while the panel is editing a name cannot undo the edit.
func RecordAgentCert(id uuid.UUID, serial, issuer string, notAfter time.Time) error {
	return db.DB.Model(&Agent{}).Where("id = ?", id).Updates(map[string]any{
		"cert_serial":    serial,
		"cert_issuer":    issuer,
		"cert_not_after": notAfter,
	}).Error
}

// RecordPresentedIssuer stores the root behind the certificate an agent just
// authenticated with and reports whether it changed.
func RecordPresentedIssuer(id uuid.UUID, issuer string) (bool, error) {
	res := db.DB.Model(&Agent{}).Where("id = ? AND (presented_issuer IS NULL OR presented_issuer <> ?)", id, issuer).
		Update("presented_issuer", issuer)
	return res.RowsAffected > 0, res.Error
}

// DeleteAgent removes an agent by id.
func DeleteAgent(id uuid.UUID) error {
	return db.DB.Where("id = ?", id).Delete(&Agent{}).Error
}

// TouchAgentSeen records that an agent connected or sent a heartbeat now.
func TouchAgentSeen(id uuid.UUID, at time.Time) error {
	return db.DB.Model(&Agent{}).Where("id = ?", id).Update("last_seen_at", at).Error
}

// credentialKeySize is the length of an agent's credential-store master key, an
// AES-256 key matching what the agent's store seals with.
const credentialKeySize = 32

// AgentCredentialKey returns the agent's credential-store master key, minting
// and sealing one on first request. The key is stored under
// security.encryption_key, so it is only ever at rest encrypted; this returns
// the plaintext for the caller to hand to the enrolled agent and then forget.
// Generating it lazily means an agent that never uses credentialed unlock never
// gets a key.
func AgentCredentialKey(id uuid.UUID) ([]byte, error) {
	key, err := encryptionKey()
	if err != nil {
		return nil, err
	}
	agent, err := FindAgentByID(id)
	if err != nil {
		return nil, err
	}

	if agent.CredentialKey != "" {
		k, err := crypto.Decrypt(key, agent.CredentialKey)
		if err != nil {
			return nil, fmt.Errorf("opening the agent credential key: %w", err)
		}
		return k, nil
	}

	k := make([]byte, credentialKeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("generating an agent credential key: %w", err)
	}
	sealed, err := crypto.Encrypt(key, k)
	if err != nil {
		return nil, fmt.Errorf("sealing the agent credential key: %w", err)
	}
	// Only an agent with no key takes this one. Two first requests arriving
	// together, an operator's credential set beside a scheduled unlock, would
	// otherwise each mint a key and the loser's ciphertext would never open.
	result := db.DB.Model(&Agent{}).
		Where("id = ? AND (credential_key = '' OR credential_key IS NULL)", id).
		Update("credential_key", sealed)
	if result.Error != nil {
		return nil, fmt.Errorf("storing the agent credential key: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return AgentCredentialKey(id)
	}
	return k, nil
}

// AgentCredentialRekey is one agent's credential-store key re-sealed under a
// new encryption key.
type AgentCredentialRekey struct {
	ID     uuid.UUID
	Name   string
	Sealed string
}

// StageAgentCredentialRekey opens every agent's credential key with oldKey and
// seals it with newKey. A key that does not open reports crypto.ErrDecrypt
// naming the agent.
func StageAgentCredentialRekey(oldKey, newKey []byte) ([]AgentCredentialRekey, error) {
	var agents []Agent
	if err := db.DB.Where("credential_key <> ''").Order("name ASC").Find(&agents).Error; err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	out := make([]AgentCredentialRekey, 0, len(agents))
	for _, agent := range agents {
		plain, err := crypto.Decrypt(oldKey, agent.CredentialKey)
		if err != nil {
			return nil, fmt.Errorf("agent %s credential key: %w", agent.Name, err)
		}
		sealed, err := crypto.Encrypt(newKey, plain)
		if err != nil {
			return nil, fmt.Errorf("re-encrypting the credential key of agent %s: %w", agent.Name, err)
		}
		out = append(out, AgentCredentialRekey{ID: agent.ID, Name: agent.Name, Sealed: sealed})
	}
	return out, nil
}

// Apply writes the re-sealed key inside tx.
func (r AgentCredentialRekey) Apply(tx *gorm.DB) error {
	return tx.Model(&Agent{}).Where("id = ?", r.ID).Update("credential_key", r.Sealed).Error
}

// SetAgentVersion records the version an agent reported. It writes that one
// column, so a renewal arriving while the operator revokes or renames the
// agent cannot write the row it read back over their change.
func SetAgentVersion(id uuid.UUID, version string) error {
	return db.DB.Model(&Agent{}).Where("id = ?", id).Update("version", version).Error
}

// UpdateAgentDetails writes the fields the panel edits and nothing else, so a
// certificate renewal recorded in between keeps its serial.
func UpdateAgentDetails(id uuid.UUID, name, description string, enabled bool) error {
	return db.DB.Model(&Agent{}).Where("id = ?", id).Updates(map[string]any{
		"name":        name,
		"description": description,
		"enabled":     enabled,
	}).Error
}

// AgentEnrollmentToken is a single-use secret that lets an unknown host enroll.
// The raw token is shown to the operator once; only its hash is stored. It
// carries the CA pin so an agent can verify the host on first contact, and the
// name and description the resulting agent inherits.
type AgentEnrollmentToken struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	TokenHash   string     `gorm:"uniqueIndex;not null" json:"-"`
	Name        string     `json:"name,omitempty" description:"Name the enrolled agent inherits; falls back to its hostname."`
	Description string     `json:"description,omitempty" description:"Description the enrolled agent inherits."`
	ExpiresAt   time.Time  `json:"expires_at" description:"When the token stops being accepted."`
	UsedAt      *time.Time `json:"used_at,omitempty" description:"When the token was consumed; nil while unused."`
	CreatedBy   *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty" description:"User who minted the token."`
	CreatedAt   time.Time  `json:"created_at" description:"Creation timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (t *AgentEnrollmentToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

// HashEnrollmentToken hashes a raw token for storage and lookup. The raw value
// is high-entropy, so a fast hash is the right tool: it is not a password.
func HashEnrollmentToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewEnrollmentToken mints a token, storing only its hash and returning the raw
// value for the operator to hand the agent once. ttl bounds its life.
func NewEnrollmentToken(name, description string, createdBy *uuid.UUID, ttl time.Duration) (raw string, tok *AgentEnrollmentToken, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("drawing enrollment token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	tok = &AgentEnrollmentToken{
		TokenHash:   HashEnrollmentToken(raw),
		Name:        name,
		Description: description,
		ExpiresAt:   time.Now().Add(ttl),
		CreatedBy:   createdBy,
	}
	if err := db.DB.Create(tok).Error; err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

// ConsumeEnrollmentToken validates a raw token and marks it used in one atomic
// update, so two agents racing the same token cannot both enroll. It returns
// the token record on success.
func ConsumeEnrollmentToken(raw string, at time.Time) (*AgentEnrollmentToken, error) {
	hash := HashEnrollmentToken(raw)
	var tok AgentEnrollmentToken
	if err := db.DB.Where("token_hash = ?", hash).First(&tok).Error; err != nil {
		return nil, fmt.Errorf("unknown enrollment token")
	}
	if tok.UsedAt != nil {
		return nil, fmt.Errorf("enrollment token already used")
	}
	if at.After(tok.ExpiresAt) {
		return nil, fmt.Errorf("enrollment token expired")
	}
	// Claim it: the WHERE guards against a concurrent claim, and rows-affected
	// tells us who won.
	res := db.DB.Model(&AgentEnrollmentToken{}).
		Where("id = ? AND used_at IS NULL", tok.ID).
		Update("used_at", at)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("enrollment token already used")
	}
	tok.UsedAt = &at
	return &tok, nil
}

// ListEnrollmentTokens returns tokens ordered newest first, for the panel.
func ListEnrollmentTokens() ([]*AgentEnrollmentToken, error) {
	var toks []*AgentEnrollmentToken
	err := db.DB.Order("created_at DESC").Find(&toks).Error
	return toks, err
}

// DeleteEnrollmentToken removes a token by id, which is how an operator revokes
// one before it is used. It returns gorm.ErrRecordNotFound when no token has
// that id.
func DeleteEnrollmentToken(id uuid.UUID) error {
	res := db.DB.Where("id = ?", id).Delete(&AgentEnrollmentToken{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RevokedAgentCert records a certificate serial the operator has revoked. The
// agent listener refuses any handshake presenting a listed serial, even one
// that still chains to the CA and has not expired.
type RevokedAgentCert struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Serial    string    `gorm:"uniqueIndex;not null" json:"serial" description:"Revoked certificate serial, lowercase hex."`
	AgentID   uuid.UUID `gorm:"type:uuid" json:"agent_id" description:"Agent the certificate belonged to."`
	Reason    string    `json:"reason,omitempty" description:"Why it was revoked."`
	CreatedAt time.Time `json:"created_at" description:"When it was revoked."`
}

// RevokeAgentSerial records a serial as revoked. A repeat is not an error.
func RevokeAgentSerial(serial string, agentID uuid.UUID, reason string) error {
	if serial == "" {
		return nil
	}
	rec := RevokedAgentCert{Serial: serial, AgentID: agentID, Reason: reason}
	return db.DB.Where("serial = ?", serial).FirstOrCreate(&rec).Error
}

// IsAgentSerialRevoked reports whether a serial has been revoked.
func IsAgentSerialRevoked(serial string) (bool, error) {
	var count int64
	err := db.DB.Model(&RevokedAgentCert{}).Where("serial = ?", serial).Count(&count).Error
	return count > 0, err
}
