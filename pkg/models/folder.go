package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// maxFolderDepth bounds ancestor walks so corrupt parent links cannot spin
// forever. Real hierarchies are far shallower than this.
const maxFolderDepth = 64

// Folder groups flows and subflows into a nested tree. A nil ParentID places
// the folder at the root alongside uncategorized flows.
type Folder struct {
	ID        uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name      string         `gorm:"not null" json:"name" description:"Folder name."`
	ParentID  *uint          `gorm:"index" json:"parent_id,omitempty" description:"Parent folder ID; absent at the root."`
	OwnerID   uuid.UUID      `gorm:"type:uuid;not null" json:"owner_id" description:"Owner user ID."`
	CreatedAt time.Time      `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time      `json:"updated_at" description:"Last update timestamp."`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	// AccessLevel is the caller's level on the folder, filled in by callers
	// that know who is asking.
	AccessLevel string `gorm:"-" json:"access_level,omitempty" description:"The caller's access level: faceplate, view, edit, or admin."`
}

// FindFolderByID returns a folder looked up by primary key.
func FindFolderByID(id uint) (*Folder, error) {
	var folder Folder
	err := db.DB.First(&folder, id).Error
	return &folder, err
}

// ListFolders returns every folder ordered by name. The tree is assembled by
// the caller, so one flat query serves the whole hierarchy.
func ListFolders() ([]*Folder, error) {
	var folders []*Folder
	err := db.DB.Order("name ASC").Find(&folders).Error
	return folders, err
}

// FolderIsDescendantOf reports whether candidate is the folder itself or sits
// anywhere beneath it. Move operations use this to reject cycles.
func FolderIsDescendantOf(candidateID, folderID uint) (bool, error) {
	current := candidateID
	for i := 0; i < maxFolderDepth; i++ {
		if current == folderID {
			return true, nil
		}
		f, err := FindFolderByID(current)
		if err != nil {
			return false, err
		}
		if f.ParentID == nil {
			return false, nil
		}
		current = *f.ParentID
	}
	return true, nil
}

// FolderChildCounts returns how many subfolders and flows a folder directly
// holds, so deletion can refuse to discard contents.
func FolderChildCounts(id uint) (folders int64, flows int64, err error) {
	if err = db.DB.Model(&Folder{}).Where("parent_id = ?", id).Count(&folders).Error; err != nil {
		return 0, 0, err
	}
	if err = db.DB.Model(&Flow{}).Where("folder_id = ?", id).Count(&flows).Error; err != nil {
		return 0, 0, err
	}
	return folders, flows, nil
}
