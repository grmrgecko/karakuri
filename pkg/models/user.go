package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Preferences holds a user's client-side UI settings. It is stored as JSON
// text and exposed as an object so the frontend can add a setting without a
// schema change; the server never interprets the keys.
type Preferences map[string]any

// Scan decodes the stored JSON text. A NULL or empty column reads as nil.
func (p *Preferences) Scan(value any) error {
	if value == nil {
		*p = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("preferences: unsupported source type %T", value)
	}
	if len(raw) == 0 {
		*p = nil
		return nil
	}
	return json.Unmarshal(raw, p)
}

// Value encodes the map for storage; a nil map stores as NULL.
func (p Preferences) Value() (driver.Value, error) {
	if p == nil {
		return nil, nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// User represents an authenticated account in the system.
type User struct {
	ID           uuid.UUID   `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Username     string      `gorm:"uniqueIndex;not null" json:"username" description:"Login name."`
	Email        string      `gorm:"uniqueIndex;not null" json:"email" description:"Email address."`
	PasswordHash string      `gorm:"not null" json:"-"`
	Preferences  Preferences `gorm:"type:text" json:"preferences,omitempty" description:"Client UI preferences."`
	CreatedAt    time.Time   `json:"created_at" description:"Creation timestamp."`
	UpdatedAt    time.Time   `json:"updated_at" description:"Last update timestamp."`
}

// UpdateUserPreferences merges a patch into a user's stored preferences and
// returns the result. Keys absent from the patch are left as they were, so
// unrelated clients do not clobber each other's settings.
func UpdateUserPreferences(userID uuid.UUID, patch Preferences) (Preferences, error) {
	var user User
	if err := db.DB.First(&user, "id = ?", userID).Error; err != nil {
		return nil, err
	}

	merged := user.Preferences
	if merged == nil {
		merged = Preferences{}
	}
	for k, v := range patch {
		merged[k] = v
	}

	if err := db.DB.Model(&user).Update("preferences", merged).Error; err != nil {
		return nil, err
	}
	return merged, nil
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

// ListUsers returns a paginated list of users ordered by username.
func ListUsers(page, pageSize int) ([]*User, int64, error) {
	var users []*User
	var count int64

	db.DB.Model(&User{}).Count(&count)

	offset := (page - 1) * pageSize
	err := db.DB.Offset(offset).Limit(pageSize).Order("username ASC").Find(&users).Error
	return users, count, err
}

// containsPattern is the LIKE pattern for names that hold query as written.
// LIKE wildcards in the query are escaped with !, as settingPrefix does,
// because names hold underscores.
func containsPattern(query string) string {
	return "%" + strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`).Replace(query) + "%"
}

// SearchUsers returns users whose username matches the query substring.
func SearchUsers(query string, limit int) ([]*User, error) {
	var users []*User
	err := db.DB.Where("username LIKE ? ESCAPE '!'", containsPattern(query)).Order("username ASC").Limit(limit).Find(&users).Error
	return users, err
}

// UserGroup is a named collection of users with ownership tracked via GroupOwner.
type UserGroup struct {
	ID        uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name      string         `gorm:"not null" json:"name" description:"Group name."`
	CreatedAt time.Time      `json:"created_at" description:"Creation timestamp."`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Create inserts a new group, restoring a soft-deleted one with the same name if found.
func (m *UserGroup) Create() error {
	var existing UserGroup
	err := db.DB.Unscoped().Where("name = ?", m.Name).First(&existing).Error
	if err == nil {
		if !existing.DeletedAt.Valid {
			return gorm.ErrDuplicatedKey
		}
		existing.Name = m.Name
		existing.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&existing).Error; err != nil {
			return err
		}
		*m = existing
		return nil
	}
	return db.DB.Create(m).Error
}

// Delete removes the group with its members, owners, role assignments, and
// the dashboard and flow shares granted to it, in a transaction. SQLite may
// hand a deleted group's ID to the next group created, and Create revives a
// deleted group of the same name under its old ID, so a share or role left
// behind would grant that group the deleted one's access.
func (m *UserGroup) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		for _, rows := range []any{&UserGroupMember{}, &GroupOwner{}, &RoleAssignment{}, &DashboardAccess{}, &DashboardGroupAccess{}, &FlowAccess{}, &FolderAccess{}, &ConnectionAccess{}, &AgentAccess{}} {
			if err := tx.Where("group_id = ?", m.ID).Delete(rows).Error; err != nil {
				return err
			}
		}
		// The group may also own other groups.
		if err := tx.Where("owner_group_id = ?", m.ID).Delete(&GroupOwner{}).Error; err != nil {
			return err
		}
		return tx.Delete(&UserGroup{}, m.ID).Error
	})
}

// ListUserGroups returns a paginated list of groups ordered by name.
func ListUserGroups(page, pageSize int) ([]*UserGroup, int64, error) {
	var groups []*UserGroup
	var count int64

	db.DB.Model(&UserGroup{}).Count(&count)

	offset := (page - 1) * pageSize
	err := db.DB.Offset(offset).Limit(pageSize).Order("name ASC").Find(&groups).Error
	return groups, count, err
}

// GetGroupMembers returns all members of a group with preloaded User.
func GetGroupMembers(groupID uint) ([]UserGroupMember, error) {
	var members []UserGroupMember
	err := db.DB.Preload("User").Where("group_id = ?", groupID).Find(&members).Error
	return members, err
}

// GetUserGroupIDs returns all group IDs that a user belongs to.
func GetUserGroupIDs(userID uuid.UUID) ([]uint, error) {
	var ids []uint
	err := db.DB.Model(&UserGroupMember{}).
		Where("user_id = ?", userID).
		Pluck("group_id", &ids).Error
	return ids, err
}

// ListGroupOwners returns all ownership entries for a group with preloaded relations.
func ListGroupOwners(groupID uint) ([]GroupOwner, error) {
	var owners []GroupOwner
	err := db.DB.Preload("User").Preload("OwnerGroup").Where("group_id = ?", groupID).Find(&owners).Error
	return owners, err
}

// SearchUserGroups returns groups whose name matches the query substring.
func SearchUserGroups(query string, limit int) ([]*UserGroup, error) {
	var groups []*UserGroup
	err := db.DB.Where("name LIKE ? ESCAPE '!'", containsPattern(query)).Order("name ASC").Limit(limit).Find(&groups).Error
	return groups, err
}

// GetMemberUsers returns distinct users who belong to any of the given groups.
func GetMemberUsers(groupIDs []uint) ([]*User, error) {
	var users []*User
	if len(groupIDs) == 0 {
		return users, nil
	}
	err := db.DB.Distinct().
		Joins("JOIN user_group_members ON user_group_members.user_id = users.id").
		Where("user_group_members.group_id IN ?", groupIDs).
		Order("username ASC").
		Find(&users).Error
	return users, err
}

// GroupOwner links a group to either a user or another group as owner.
type GroupOwner struct {
	ID           uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	GroupID      uint           `gorm:"not null" json:"group_id" description:"Owned group ID."`
	Group        UserGroup      `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Owned group."`
	UserID       *uuid.UUID     `gorm:"type:uuid" json:"user_id,omitempty" description:"Owner user ID."`
	User         *User          `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Owner user."`
	OwnerGroupID *uint          `json:"owner_group_id,omitempty" description:"Owner group ID."`
	OwnerGroup   *UserGroup     `gorm:"foreignKey:OwnerGroupID" json:"owner_group,omitempty" description:"Owner group."`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Create inserts a group owner, restoring a soft-deleted duplicate if found.
func (m *GroupOwner) Create() error {
	q := db.DB.Unscoped().Where("group_id = ?", m.GroupID)
	if m.UserID != nil {
		q = q.Where("user_id = ?", *m.UserID)
	} else if m.OwnerGroupID != nil {
		q = q.Where("owner_group_id = ?", *m.OwnerGroupID)
	}
	var existing GroupOwner
	if err := q.First(&existing).Error; err == nil {
		if !existing.DeletedAt.Valid {
			return gorm.ErrDuplicatedKey
		}
		existing.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&existing).Error; err != nil {
			return err
		}
		*m = existing
		return nil
	}
	return db.DB.Create(m).Error
}

// UserGroupMember is the join record between a user and a group.
type UserGroupMember struct {
	GroupID uint      `gorm:"primaryKey" json:"group_id" description:"Group ID."`
	UserID  uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id" description:"User ID."`
	Group   UserGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Group."`
	User    User      `gorm:"foreignKey:UserID" json:"user,omitempty" description:"User."`
}
