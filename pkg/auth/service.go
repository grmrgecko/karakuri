package auth

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// TokenPair carries both short-lived access and long-lived refresh JWTs back to the caller.
type TokenPair struct {
	AccessToken  string `json:"access_token" description:"JWT access token."`
	RefreshToken string `json:"refresh_token" description:"Refresh token for renewal."`
	ExpiresIn    int    `json:"expires_in" description:"Token lifetime in seconds."`
}

// Claims is embedded in access JWTs to identify the user without a database lookup.
type Claims struct {
	UserID    uuid.UUID `json:"user_id"`
	Username  string    `json:"username"`
	SessionID uuid.UUID `json:"session_id"`
	jwt.RegisteredClaims
}

// RefreshClaims is embedded in refresh JWTs; carries only the user and session IDs since a full user lookup happens on refresh.
// Generation must match the session's RefreshGeneration for the token to be used.
type RefreshClaims struct {
	Generation int `json:"gen,omitempty"`
	UserID    uuid.UUID `json:"user_id"`
	SessionID uuid.UUID `json:"session_id"`
	jwt.RegisteredClaims
}

// Token audiences tell an access token and a refresh token apart, so neither
// is accepted where the other is expected: a leaked access token cannot be
// exchanged for a fresh pair, and a refresh token is no bearer credential.
const (
	audienceAccess  = "access"
	audienceRefresh = "refresh"
)

// unknownUserHash is a bcrypt hash compared against the password of a
// username that does not exist, so a failed login costs the same time either
// way and response timing does not confirm which usernames are real.
var unknownUserHash struct {
	once sync.Once
	hash []byte
}

func unknownUserPasswordHash() []byte {
	unknownUserHash.once.Do(func() {
		unknownUserHash.hash, _ = bcrypt.GenerateFromPassword([]byte("no such user"), bcryptCost())
	})
	return unknownUserHash.hash
}

// bcryptCost returns the configured bcrypt cost, falling back to the library default.
func bcryptCost() int {
	if config.C != nil && config.C.Security.BcryptCost >= bcrypt.MinCost && config.C.Security.BcryptCost <= bcrypt.MaxCost {
		return config.C.Security.BcryptCost
	}
	return bcrypt.DefaultCost
}

// HashPassword produces a bcrypt hash of a password with the configured cost.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost())
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword performs a constant-time comparison of a plaintext password against a hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Authenticate verifies credentials and returns the user on success.
// Both lookup failure and password mismatch return the same generic error to avoid leaking whether a username exists.
func Authenticate(username, password string) (*models.User, error) {
	var user models.User
	if err := db.DB.Where("username = ?", username).First(&user).Error; err != nil {
		_ = bcrypt.CompareHashAndPassword(unknownUserPasswordHash(), []byte(password))
		return nil, fmt.Errorf("invalid credentials")
	}

	if !CheckPassword(user.PasswordHash, password) {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &user, nil
}

// GenerateTokens issues an access/refresh token pair for the given user and
// session, with the refresh token at the given generation.
func GenerateTokens(user *models.User, sessionID uuid.UUID, generation int) (*TokenPair, error) {
	now := time.Now()

	accessClaims := Claims{
		UserID:    user.ID,
		Username:  user.Username,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(config.C.Security.JWTExpiry) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   user.ID.String(),
			Audience:  jwt.ClaimStrings{audienceAccess},
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessStr, err := accessToken.SignedString([]byte(config.C.Security.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshClaims := RefreshClaims{
		Generation: generation,
		UserID:     user.ID,
		SessionID:  sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(config.C.Security.RefreshExpiry) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   user.ID.String(),
			Audience:  jwt.ClaimStrings{audienceRefresh},
		},
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshStr, err := refreshToken.SignedString([]byte(config.C.Security.JWTSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessStr,
		RefreshToken: refreshStr,
		ExpiresIn:    config.C.Security.JWTExpiry,
	}, nil
}

// CreateSession creates a database-backed session for the user and returns a fresh token pair.
func CreateSession(user *models.User, ipAddress, userAgent string) (*TokenPair, error) {
	now := time.Now()
	session := &models.Session{
		UserID:       user.ID,
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
		LastActiveAt: now,
		ExpiresAt:    now.Add(time.Duration(config.C.Security.RefreshExpiry) * time.Second),
	}

	if err := db.DB.Create(session).Error; err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return GenerateTokens(user, session.ID, 0)
}

// ValidateSession looks up a session by ID and rejects it if expired.
func ValidateSession(sessionID uuid.UUID) (*models.Session, error) {
	session, err := models.FindSessionByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}

	if session.IsExpired() {
		db.DB.Delete(session)
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// ValidateToken parses and validates an access JWT, then verifies the associated session is still active.
// Returns both the JWT claims and the validated session.
func ValidateToken(tokenStr string) (*Claims, *models.Session, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, hmacKey, jwt.WithAudience(audienceAccess))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, nil, fmt.Errorf("invalid token claims")
	}

	// Every JWT must carry a valid session ID.
	if claims.SessionID == uuid.Nil {
		return nil, nil, fmt.Errorf("token missing session")
	}

	session, err := ValidateSession(claims.SessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid session: %w", err)
	}
	if session.UserID != claims.UserID {
		return nil, nil, fmt.Errorf("token names another user's session")
	}

	return claims, session, nil
}

// hmacKey is the key lookup for every token this service issues: HS256 with
// the configured secret, and nothing else, so a token that names another
// algorithm is refused before its signature is looked at.
func hmacKey(token *jwt.Token) (interface{}, error) {
	if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	}
	return []byte(config.C.Security.JWTSecret), nil
}

// ErrRefreshReused reports a refresh token that was already exchanged. The
// session it names has been revoked.
var ErrRefreshReused = errors.New("refresh token was already used; the session has been revoked")

// RefreshReuseGrace is how long the token a refresh just replaced keeps
// working. Browser tabs share one stored refresh token, and two that refresh
// at the same moment would otherwise read as a replay and sign the user out.
var RefreshReuseGrace = 30 * time.Second

// RefreshToken validates a refresh JWT, verifies the session, extends its
// lifetime, and issues a fresh token pair. Each refresh token works once: the
// session's generation advances, and presenting an older token after the
// grace period revokes the session, since only a copied token can still be
// presented after its holder moved on.
func RefreshToken(refreshTokenStr string) (*TokenPair, error) {
	token, err := jwt.ParseWithClaims(refreshTokenStr, &RefreshClaims{}, hmacKey, jwt.WithAudience(audienceRefresh))
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid refresh token claims")
	}

	// Every refresh token must carry a valid session.
	if claims.SessionID == uuid.Nil {
		return nil, fmt.Errorf("refresh token missing session")
	}

	session, err := ValidateSession(claims.SessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}
	// The session row decides whose tokens come out of a refresh, so a forged
	// user id over a real session yields nothing.
	if session.UserID != claims.UserID {
		return nil, fmt.Errorf("refresh token names another user's session")
	}

	var user models.User
	if err := db.DB.Where("id = ?", claims.UserID).First(&user).Error; err != nil {
		return nil, fmt.Errorf("user not found")
	}

	// Advance the generation only from the one the token carries, so two
	// exchanges of the same token cannot both advance it.
	now := time.Now()
	res := db.DB.Model(&models.Session{}).
		Where("id = ? AND refresh_generation = ?", session.ID, claims.Generation).
		Updates(map[string]any{
			"refresh_generation": gorm.Expr("refresh_generation + 1"),
			"refresh_rotated_at": now,
			"last_active_at":     now,
			"expires_at":         now.Add(time.Duration(config.C.Security.RefreshExpiry) * time.Second),
		})
	if res.Error != nil {
		return nil, fmt.Errorf("rotating refresh token: %w", res.Error)
	}
	if res.RowsAffected == 1 {
		return GenerateTokens(&user, session.ID, claims.Generation+1)
	}

	// Someone exchanged this token first. Within the grace period that is a
	// concurrent refresh, which gets the current generation; anything else is
	// a replay.
	current, err := models.FindSessionByID(session.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}
	if claims.Generation == current.RefreshGeneration-1 && current.RefreshRotatedAt != nil &&
		now.Sub(*current.RefreshRotatedAt) < RefreshReuseGrace {
		return GenerateTokens(&user, session.ID, current.RefreshGeneration)
	}
	if err := DeleteSession(session.ID); err != nil {
		return nil, fmt.Errorf("revoking replayed session: %w", err)
	}
	return nil, ErrRefreshReused
}

// DeleteSession removes a session by ID.
func DeleteSession(sessionID uuid.UUID) error {
	return db.DB.Where("id = ?", sessionID).Delete(&models.Session{}).Error
}

// CleanupExpiredSessions removes all sessions past their expiration time.
func CleanupExpiredSessions() {
	_, _ = models.DeleteExpiredSessions()
}

// ResetPassword replaces a user's password hash after validating the user
// exists, and ends the user's sessions: a reset is how an account somebody
// else signed in to is recovered, and their session would otherwise keep
// refreshing itself.
func ResetPassword(username, newPassword string) error {
	var user models.User
	if err := db.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	user.PasswordHash = hash
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", user.ID).Delete(&models.Session{}).Error
	})
}

// GetEffectivePermissions collects deduplicated permission strings across all roles
// assigned to the user directly and through group membership.
func GetEffectivePermissions(userID uuid.UUID) ([]string, error) {
	// Roles assigned directly to the user.
	directRoleIDs, err := models.GetUserRoleIDs(userID)
	if err != nil {
		return nil, err
	}

	// Groups the user belongs to.
	groupIDs, err := models.GetUserGroupIDs(userID)
	if err != nil {
		return nil, err
	}

	// Roles assigned through groups.
	groupRoleIDs, err := models.GetGroupRoleIDs(groupIDs)
	if err != nil {
		return nil, err
	}

	// Merge and deduplicate role IDs.
	seen := make(map[uint]bool)
	var allRoleIDs []uint
	for _, id := range directRoleIDs {
		if !seen[id] {
			seen[id] = true
			allRoleIDs = append(allRoleIDs, id)
		}
	}
	for _, id := range groupRoleIDs {
		if !seen[id] {
			seen[id] = true
			allRoleIDs = append(allRoleIDs, id)
		}
	}

	// Load permissions for all roles.
	rolePerms, err := models.GetRolePermissions(allRoleIDs)
	if err != nil {
		return nil, err
	}

	// Deduplicate permission strings.
	permSet := make(map[string]bool)
	var result []string
	for _, rp := range rolePerms {
		key := rp.Permission
		if !permSet[key] {
			permSet[key] = true
			result = append(result, key)
		}
	}

	return result, nil
}

// HasPermission reports whether the user holds a permission that matches the required string, using wildcard matching.
func HasPermission(userID uuid.UUID, required string) bool {
	perms, err := GetEffectivePermissions(userID)
	if err != nil {
		return false
	}

	for _, held := range perms {
		if permissions.MatchPermission(held, required) {
			return true
		}
	}
	return false
}

// GetUserByID looks up a user by primary key, distinguishing not-found from other database errors.
func GetUserByID(userID uuid.UUID) (*models.User, error) {
	var user models.User
	if err := db.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return &user, nil
}
