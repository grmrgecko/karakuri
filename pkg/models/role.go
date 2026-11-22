package models

import (
	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Role defines a named set of permissions that can be assigned to users or groups.
type Role struct {
	ID          uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name        string         `gorm:"uniqueIndex;not null" json:"name" description:"Role name."`
	Description string         `gorm:"not null" json:"description" description:"Role description."`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// Create inserts a new role, restoring a soft-deleted one with the same name if found.
func (m *Role) Create() error {
	var existing Role
	err := db.DB.Unscoped().Where("name = ?", m.Name).First(&existing).Error
	if err == nil {
		if !existing.DeletedAt.Valid {
			return gorm.ErrDuplicatedKey
		}
		existing.Description = m.Description
		existing.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&existing).Error; err != nil {
			return err
		}
		*m = existing
		return nil
	}
	return db.DB.Create(m).Error
}

// Delete removes a role and its permissions and assignments in a transaction.
func (m *Role) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", m.ID).Delete(&RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("role_id = ?", m.ID).Delete(&RoleAssignment{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Role{}, m.ID).Error
	})
}

// RolePermission grants a permission to a role.
type RolePermission struct {
	ID         uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	RoleID     uint           `gorm:"not null" json:"role_id" description:"Parent role ID."`
	Role       Role           `gorm:"foreignKey:RoleID" json:"role,omitempty" description:"Parent role."`
	Permission string         `gorm:"not null" json:"permission" description:"Permission string (e.g. api:flows:read)."`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// Create inserts a role permission, restoring a soft-deleted duplicate if found.
func (m *RolePermission) Create() error {
	var existing RolePermission
	err := db.DB.Unscoped().Where("role_id = ? AND permission = ?",
		m.RoleID, m.Permission).First(&existing).Error
	if err == nil {
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

// RoleAssignment links a role to either a user or a group.
type RoleAssignment struct {
	ID        uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	RoleID    uint           `gorm:"not null" json:"role_id" description:"Assigned role ID."`
	Role      Role           `gorm:"foreignKey:RoleID" json:"role,omitempty" description:"Assigned role."`
	UserID    *uuid.UUID     `gorm:"type:uuid" json:"user_id,omitempty" description:"Assigned user ID."`
	User      *User          `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Assigned user."`
	GroupID   *uint          `json:"group_id,omitempty" description:"Assigned group ID."`
	Group     *UserGroup     `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Assigned group."`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Create inserts a role assignment, restoring a soft-deleted duplicate if found.
func (m *RoleAssignment) Create() error {
	q := db.DB.Unscoped().Where("role_id = ?", m.RoleID)
	if m.UserID != nil {
		q = q.Where("user_id = ?", *m.UserID)
	} else if m.GroupID != nil {
		q = q.Where("group_id = ?", *m.GroupID)
	}
	var existing RoleAssignment
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

// ListRoleAssignments returns all role assignments with preloaded relations.
func ListRoleAssignments() ([]RoleAssignment, error) {
	var assignments []RoleAssignment
	err := db.DB.Preload("Role").Preload("User").Preload("Group").Find(&assignments).Error
	return assignments, err
}

// SeedRolePermission creates a role permission only if no record (active or soft-deleted) exists.
func SeedRolePermission(roleID uint, permission string) error {
	var count int64
	db.DB.Unscoped().Model(&RolePermission{}).
		Where("role_id = ? AND permission = ?", roleID, permission).
		Count(&count)
	if count > 0 {
		return nil
	}
	return db.DB.Create(&RolePermission{
		RoleID: roleID, Permission: permission,
	}).Error
}

// GetUserRoleIDs returns all role IDs directly assigned to a user.
func GetUserRoleIDs(userID uuid.UUID) ([]uint, error) {
	var ids []uint
	err := db.DB.Model(&RoleAssignment{}).
		Where("user_id = ?", userID).
		Pluck("role_id", &ids).Error
	return ids, err
}

// GetGroupRoleIDs returns all role IDs assigned to any of the given groups.
func GetGroupRoleIDs(groupIDs []uint) ([]uint, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	var ids []uint
	err := db.DB.Model(&RoleAssignment{}).
		Where("group_id IN ?", groupIDs).
		Pluck("role_id", &ids).Error
	return ids, err
}

// GetRolePermissions returns all permission records for the given role IDs.
func GetRolePermissions(roleIDs []uint) ([]RolePermission, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	var perms []RolePermission
	err := db.DB.Where("role_id IN ?", roleIDs).Find(&perms).Error
	return perms, err
}
