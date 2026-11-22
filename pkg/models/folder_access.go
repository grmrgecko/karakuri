package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
)

// FolderAccess grants one flow access level on a folder to a user or a user
// group. The grant reaches the folder's subfolders and every flow inside
// them, as a dashboard group's shares reach its dashboards.
type FolderAccess struct {
	ID         uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	FolderID   uint       `gorm:"not null;index" json:"folder_id" description:"Folder ID."`
	UserID     *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty" description:"Granted user ID."`
	User       *User      `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Granted user."`
	GroupID    *uint      `gorm:"index" json:"group_id,omitempty" description:"Granted user group ID."`
	Group      *UserGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Granted user group."`
	Permission string     `gorm:"not null" json:"permission" description:"Access level: faceplate, view, edit, or admin."`
	CreatedAt  time.Time  `json:"created_at" description:"Creation timestamp."`
}

// FolderRanks returns the access rank a user holds on every folder they hold
// anything on: admin for folders they own, the highest share held directly or
// through a group, and whatever the folder's ancestors grant, since a folder's
// owner and shares reach everything beneath it. Folders absent from the map
// rank 0. Folders are few, so the whole tree is read in two queries.
func FolderRanks(userID uuid.UUID, userGroupIDs []uint) (map[uint]int, error) {
	out := map[uint]int{}
	if userID == uuid.Nil {
		return out, nil
	}
	var folders []Folder
	if err := db.DB.Select("id", "parent_id", "owner_id").Find(&folders).Error; err != nil {
		return nil, err
	}
	var grants []FolderAccess
	q := db.DB.Where("user_id = ?", userID)
	if len(userGroupIDs) > 0 {
		q = q.Or("group_id IN ?", userGroupIDs)
	}
	if err := q.Find(&grants).Error; err != nil {
		return nil, err
	}

	direct := map[uint]int{}
	parent := map[uint]*uint{}
	for _, f := range folders {
		parent[f.ID] = f.ParentID
		if f.OwnerID == userID {
			direct[f.ID] = flowAccessRank[FlowAccessAdmin]
		}
	}
	for _, g := range grants {
		direct[g.FolderID] = max(direct[g.FolderID], flowAccessRank[g.Permission])
	}

	// A folder's rank is the highest along its path to the root. The walk is
	// bounded so a corrupt parent cycle cannot spin.
	for _, f := range folders {
		rank := 0
		current := &f.ID
		for depth := 0; current != nil && depth < maxFolderDepth; depth++ {
			rank = max(rank, direct[*current])
			current = parent[*current]
		}
		if rank > 0 {
			out[f.ID] = rank
		}
	}
	return out, nil
}

// foldersAtLeast returns the IDs of folders ranked at least minLevel.
func foldersAtLeast(ranks map[uint]int, minLevel string) []uint {
	ids := make([]uint, 0, len(ranks))
	for id, rank := range ranks {
		if rank >= flowAccessRank[minLevel] {
			ids = append(ids, id)
		}
	}
	return ids
}

// ListFolderAccess returns a folder's shares with their users and groups.
func ListFolderAccess(folderID uint) ([]FolderAccess, error) {
	var entries []FolderAccess
	err := db.DB.Preload("User").Preload("Group").Where("folder_id = ?", folderID).Order("id ASC").Find(&entries).Error
	return entries, err
}
