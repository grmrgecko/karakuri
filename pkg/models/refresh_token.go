package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Session represents an authenticated login session tied to a user.
// Sessions back refresh tokens: a refresh JWT is only valid while its
// session row exists and has not expired.
type Session struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Session identifier."`
	UserID       uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id" description:"Owning user ID."`
	User         User      `gorm:"foreignKey:UserID" json:"-"`
	IPAddress    string    `gorm:"not null" json:"ip_address" description:"Client IP address at login."`
	UserAgent    string    `gorm:"not null" json:"user_agent" description:"Client User-Agent at login."`
	LastActiveAt time.Time `gorm:"not null" json:"last_active_at" description:"Last activity timestamp."`
	// RefreshGeneration is the generation the session's current refresh token
	// carries. Each refresh advances it, so presenting an older token is a
	// replay. RefreshRotatedAt is when it last advanced.
	RefreshGeneration int        `gorm:"not null;default:0" json:"-"`
	RefreshRotatedAt  *time.Time `json:"-"`
	ExpiresAt    time.Time `gorm:"not null;index" json:"expires_at" description:"Session expiration timestamp."`
	CreatedAt    time.Time `json:"created_at" description:"Creation timestamp."`
}

// BeforeCreate auto-generates a UUID primary key if not already set.
func (s *Session) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

// IsExpired reports whether the session has passed its expiration time.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// FindSessionByID looks up a session by primary key.
func FindSessionByID(id uuid.UUID) (*Session, error) {
	var session Session
	if err := db.DB.Where("id = ?", id).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// ListUserSessions returns all sessions for a user ordered by most recently active.
func ListUserSessions(userID uuid.UUID) ([]*Session, error) {
	var sessions []*Session
	err := db.DB.Where("user_id = ?", userID).Order("last_active_at DESC").Find(&sessions).Error
	return sessions, err
}

// DeleteUserSession removes a specific session scoped to the given user.
func DeleteUserSession(userID, sessionID uuid.UUID) error {
	tx := db.DB.Where("id = ? AND user_id = ?", sessionID, userID).Delete(&Session{})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteOtherUserSessions removes all sessions for a user except the current one.
func DeleteOtherUserSessions(userID, currentSessionID uuid.UUID) (int64, error) {
	tx := db.DB.Where("user_id = ? AND id != ?", userID, currentSessionID).Delete(&Session{})
	return tx.RowsAffected, tx.Error
}

// DeleteAllUserSessions removes every session for a user.
func DeleteAllUserSessions(userID uuid.UUID) error {
	return db.DB.Where("user_id = ?", userID).Delete(&Session{}).Error
}

// DeleteExpiredSessions removes all sessions past their expiration time.
func DeleteExpiredSessions() (int64, error) {
	tx := db.DB.Where("expires_at < ?", time.Now()).Delete(&Session{})
	return tx.RowsAffected, tx.Error
}
