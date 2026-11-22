package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Connection and agent access levels, ordered least to most privileged. A
// grant of one level implies every level below it. Use lists, reads, invokes,
// and references the resource; edit also changes it; admin also shares and
// deletes it.
const (
	ResourceAccessUse   = "use"
	ResourceAccessEdit  = "edit"
	ResourceAccessAdmin = "admin"
)

// resourceAccessRank orders the levels so a comparison decides whether a
// grant satisfies a request.
var resourceAccessRank = map[string]int{
	ResourceAccessUse:   1,
	ResourceAccessEdit:  2,
	ResourceAccessAdmin: 3,
}

// ValidResourceAccess reports whether level names a connection or agent
// access level.
func ValidResourceAccess(level string) bool {
	_, ok := resourceAccessRank[level]
	return ok
}

// ResourceAccessRank returns a level's rank, 0 for an unknown level.
func ResourceAccessRank(level string) int {
	return resourceAccessRank[level]
}

// ResourceAccessLevel returns the level name for a rank, or "" for 0.
func ResourceAccessLevel(rank int) string {
	for level, r := range resourceAccessRank {
		if r == rank {
			return level
		}
	}
	return ""
}

// ConnectionAccess grants one access level on one connection to a user or a
// user group.
type ConnectionAccess struct {
	ID           uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	ConnectionID uuid.UUID  `gorm:"type:uuid;not null;index" json:"connection_id" description:"Connection ID."`
	UserID       *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty" description:"Granted user ID."`
	User         *User      `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Granted user."`
	GroupID      *uint      `gorm:"index" json:"group_id,omitempty" description:"Granted user group ID."`
	Group        *UserGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Granted user group."`
	Permission   string     `gorm:"not null" json:"permission" description:"Access level: use, edit, or admin."`
	CreatedAt    time.Time  `json:"created_at" description:"Creation timestamp."`
}

// AgentAccess grants one access level on one agent to a user or a user group.
type AgentAccess struct {
	ID         uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	AgentID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"agent_id" description:"Agent ID."`
	UserID     *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty" description:"Granted user ID."`
	User       *User      `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Granted user."`
	GroupID    *uint      `gorm:"index" json:"group_id,omitempty" description:"Granted user group ID."`
	Group      *UserGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Granted user group."`
	Permission string     `gorm:"not null" json:"permission" description:"Access level: use, edit, or admin."`
	CreatedAt  time.Time  `json:"created_at" description:"Creation timestamp."`
}

// resourceShareTable describes where one resource kind keeps its owners and
// shares, so connections and agents share one set of queries.
type resourceShareTable struct {
	resource any
	share    any
	column   string
}

var (
	connectionShares = resourceShareTable{resource: &Connection{}, share: &ConnectionAccess{}, column: "connection_id"}
	agentShares      = resourceShareTable{resource: &Agent{}, share: &AgentAccess{}, column: "agent_id"}
)

// grantRank returns the highest rank a user holds on one resource through
// ownership or shares, direct or through any of their groups.
func (t resourceShareTable) grantRank(id uuid.UUID, ownerID *uuid.UUID, userID uuid.UUID, groupIDs []uint) (int, error) {
	if userID == uuid.Nil {
		return 0, nil
	}
	if ownerID != nil && *ownerID == userID {
		return resourceAccessRank[ResourceAccessAdmin], nil
	}
	var levels []string
	q := db.DB.Model(t.share).Where(t.column+" = ?", id).
		Where(db.DB.Where("user_id = ?", userID).Or("group_id IN ?", groupIDsOrNone(groupIDs)))
	if err := q.Pluck("permission", &levels).Error; err != nil {
		return 0, err
	}
	held := 0
	for _, level := range levels {
		held = max(held, resourceAccessRank[level])
	}
	return held, nil
}

// accessibleIDs returns the IDs of every resource of the kind a user owns or
// holds a share of at least minLevel on.
func (t resourceShareTable) accessibleIDs(userID uuid.UUID, groupIDs []uint, minLevel string) ([]uuid.UUID, error) {
	levels := make([]string, 0, len(resourceAccessRank))
	for level, rank := range resourceAccessRank {
		if rank >= resourceAccessRank[minLevel] {
			levels = append(levels, level)
		}
	}
	shared := db.DB.Model(t.share).Select(t.column).
		Where("permission IN ?", levels).
		Where(db.DB.Where("user_id = ?", userID).Or("group_id IN ?", groupIDsOrNone(groupIDs)))
	var ids []uuid.UUID
	err := db.DB.Model(t.resource).Where("owner_id = ? OR id IN (?)", userID, shared).Pluck("id", &ids).Error
	return ids, err
}

// ConnectionGrantRank returns the highest access rank a user holds on a
// connection through ownership or shares, and 0 for none. Blanket permissions
// are the caller's concern.
func ConnectionGrantRank(c *Connection, userID uuid.UUID, groupIDs []uint) (int, error) {
	return connectionShares.grantRank(c.ID, c.OwnerID, userID, groupIDs)
}

// AgentGrantRank returns the highest access rank a user holds on an agent
// through ownership or shares, and 0 for none.
func AgentGrantRank(a *Agent, userID uuid.UUID, groupIDs []uint) (int, error) {
	return agentShares.grantRank(a.ID, a.OwnerID, userID, groupIDs)
}

// AccessibleConnectionIDs returns the IDs of every connection a user owns or
// holds a share of at least minLevel on.
func AccessibleConnectionIDs(userID uuid.UUID, groupIDs []uint, minLevel string) ([]uuid.UUID, error) {
	return connectionShares.accessibleIDs(userID, groupIDs, minLevel)
}

// AccessibleAgentIDs returns the IDs of every agent a user owns or holds a
// share of at least minLevel on.
func AccessibleAgentIDs(userID uuid.UUID, groupIDs []uint, minLevel string) ([]uuid.UUID, error) {
	return agentShares.accessibleIDs(userID, groupIDs, minLevel)
}

// ListConnectionAccess returns a connection's shares with their users and
// groups.
func ListConnectionAccess(connectionID uuid.UUID) ([]ConnectionAccess, error) {
	var entries []ConnectionAccess
	err := db.DB.Preload("User").Preload("Group").Where("connection_id = ?", connectionID).Order("id ASC").Find(&entries).Error
	return entries, err
}

// ListAgentAccess returns an agent's shares with their users and groups.
func ListAgentAccess(agentID uuid.UUID) ([]AgentAccess, error) {
	var entries []AgentAccess
	err := db.DB.Preload("User").Preload("Group").Where("agent_id = ?", agentID).Order("id ASC").Find(&entries).Error
	return entries, err
}

// DeleteConnectionAccess removes every share of a connection, in tx when one
// is given.
func DeleteConnectionAccess(tx *gorm.DB, connectionID uuid.UUID) error {
	if tx == nil {
		tx = db.DB
	}
	return tx.Where("connection_id = ?", connectionID).Delete(&ConnectionAccess{}).Error
}

// DeleteAgentAccess removes every share of an agent, in tx when one is given.
func DeleteAgentAccess(tx *gorm.DB, agentID uuid.UUID) error {
	if tx == nil {
		tx = db.DB
	}
	return tx.Where("agent_id = ?", agentID).Delete(&AgentAccess{}).Error
}
