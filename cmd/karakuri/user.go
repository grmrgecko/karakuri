package main

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// UserCmd groups subcommands for user account management.
type UserCmd struct {
	Create      UserCreateCmd      `cmd:"" help:"Create a new user."`
	Passwd      UserPasswdCmd      `cmd:"" help:"Reset a user's password."`
	EnsureAdmin UserEnsureAdminCmd `cmd:"" help:"Ensure the admin role and group exist (recovery)."`
}

// UserCreateCmd creates a new user account and optionally assigns the admin group.
type UserCreateCmd struct {
	Username string `arg:"" help:"Username for the new user."`
	Email    string `arg:"" help:"Email for the new user."`
	Password string `arg:"" help:"Password for the new user."`
	Admin    bool   `help:"Add user to admin group." default:"false"`
}

func (c *UserCreateCmd) Run() error {
	err := initDatabase()
	if err != nil {
		return err
	}

	// Hash password.
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Create user record.
	user := &models.User{
		ID:           uuid.New(),
		Username:     c.Username,
		Email:        c.Email,
		PasswordHash: hash,
	}
	if err := db.DB.Create(user).Error; err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Determine which groups to join.
	groupNames := []string{"User"}
	if c.Admin {
		groupNames = append(groupNames, "Admin")
	}

	// Create group memberships.
	for _, name := range groupNames {
		var group models.UserGroup
		if err := db.DB.Where("name = ?", name).First(&group).Error; err != nil {
			return fmt.Errorf("%s group not found (run migrations first): %w", name, err)
		}
		member := &models.UserGroupMember{
			GroupID: group.ID,
			UserID:  user.ID,
		}
		if err := db.DB.Create(member).Error; err != nil {
			return fmt.Errorf("failed to add user to %s group: %w", name, err)
		}
	}

	fmt.Printf("User created successfully: %s (ID: %s)\n", user.Username, user.ID)
	return nil
}

// UserEnsureAdminCmd restores the admin role and group if they were soft-deleted
// or missing, and verifies the permission, ownership, and role assignment records.
type UserEnsureAdminCmd struct{}

func (c *UserEnsureAdminCmd) Run() error {
	err := initDatabase()
	if err != nil {
		return err
	}

	// Ensure admin role exists, restoring if soft-deleted.
	var role models.Role
	if err := db.DB.Unscoped().Where("name = ?", "Admin").First(&role).Error; err != nil {
		role = models.Role{Name: "Admin", Description: "Full system access"}
		if err := db.DB.Create(&role).Error; err != nil {
			return fmt.Errorf("failed to create admin role: %w", err)
		}
		fmt.Println("Created admin role.")
	} else if role.DeletedAt.Valid {
		role.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&role).Error; err != nil {
			return fmt.Errorf("failed to restore admin role: %w", err)
		}
		fmt.Println("Restored soft-deleted admin role.")
	} else {
		fmt.Println("Admin role OK.")
	}

	// Ensure api:* permission on the admin role.
	var perm models.RolePermission
	if err := db.DB.Unscoped().Where("role_id = ? AND permission = ?", role.ID, "api:*").First(&perm).Error; err != nil {
		perm = models.RolePermission{RoleID: role.ID, Permission: "api:*"}
		if err := db.DB.Create(&perm).Error; err != nil {
			return fmt.Errorf("failed to create admin permission: %w", err)
		}
		fmt.Println("Created api:* permission on admin role.")
	} else if perm.DeletedAt.Valid {
		perm.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&perm).Error; err != nil {
			return fmt.Errorf("failed to restore admin permission: %w", err)
		}
		fmt.Println("Restored soft-deleted api:* permission on admin role.")
	} else {
		fmt.Println("Admin role api:* permission OK.")
	}

	// Ensure Admin group exists, restoring if soft-deleted.
	var group models.UserGroup
	if err := db.DB.Unscoped().Where("name = ?", "Admin").First(&group).Error; err != nil {
		group = models.UserGroup{Name: "Admin"}
		if err := db.DB.Create(&group).Error; err != nil {
			return fmt.Errorf("failed to create Admin group: %w", err)
		}
		fmt.Println("Created Admin group.")
	} else if group.DeletedAt.Valid {
		group.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&group).Error; err != nil {
			return fmt.Errorf("failed to restore Admin group: %w", err)
		}
		fmt.Println("Restored soft-deleted Admin group.")
	} else {
		fmt.Println("Admin group OK.")
	}

	// Ensure Admin group is self-owned.
	var owner models.GroupOwner
	if err := db.DB.Unscoped().Where("group_id = ? AND owner_group_id = ?", group.ID, group.ID).First(&owner).Error; err != nil {
		owner = models.GroupOwner{GroupID: group.ID, OwnerGroupID: &group.ID}
		if err := db.DB.Create(&owner).Error; err != nil {
			return fmt.Errorf("failed to create Admin group ownership: %w", err)
		}
		fmt.Println("Created Admin group self-ownership.")
	} else if owner.DeletedAt.Valid {
		owner.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&owner).Error; err != nil {
			return fmt.Errorf("failed to restore Admin group ownership: %w", err)
		}
		fmt.Println("Restored soft-deleted Admin group self-ownership.")
	} else {
		fmt.Println("Admin group ownership OK.")
	}

	// Ensure admin role is assigned to the Admin group.
	var assignment models.RoleAssignment
	if err := db.DB.Unscoped().Where("role_id = ? AND group_id = ?", role.ID, group.ID).First(&assignment).Error; err != nil {
		assignment = models.RoleAssignment{RoleID: role.ID, GroupID: &group.ID}
		if err := db.DB.Create(&assignment).Error; err != nil {
			return fmt.Errorf("failed to create admin role assignment: %w", err)
		}
		fmt.Println("Assigned admin role to Admin group.")
	} else if assignment.DeletedAt.Valid {
		assignment.DeletedAt = gorm.DeletedAt{}
		if err := db.DB.Unscoped().Save(&assignment).Error; err != nil {
			return fmt.Errorf("failed to restore admin role assignment: %w", err)
		}
		fmt.Println("Restored soft-deleted admin role assignment.")
	} else {
		fmt.Println("Admin role assignment OK.")
	}

	fmt.Printf("\nAdmin role (ID: %d) and group (ID: %d) verified.\n", role.ID, group.ID)
	return nil
}

// UserPasswdCmd replaces a user's password hash.
type UserPasswdCmd struct {
	Username string `arg:"" help:"Username of the user."`
	Password string `arg:"" help:"New password."`
}

func (c *UserPasswdCmd) Run() error {
	err := initDatabase()
	if err != nil {
		return err
	}

	if err := auth.ResetPassword(c.Username, c.Password); err != nil {
		return fmt.Errorf("failed to reset password: %w", err)
	}

	fmt.Printf("Password reset successfully for user: %s\n", c.Username)
	return nil
}
