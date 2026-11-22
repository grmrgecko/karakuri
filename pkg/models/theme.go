package models

import (
	"time"
)

// Theme defines a visual style that can be applied to dashboards.
type Theme struct {
	ID          uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name        string    `gorm:"not null" json:"name" description:"Display name."`
	Description string    `json:"description" description:"Theme description."`
	PackageName string    `gorm:"uniqueIndex;not null" json:"package_name" description:"Package identifier."`
	Version     string    `gorm:"not null" json:"version" description:"Semantic version."`
	Author      string    `json:"author" description:"Author name."`
	Copyright   string    `json:"copyright" description:"Copyright notice."`
	License     string    `json:"license" description:"License identifier."`
	Variables   string    `gorm:"type:json" json:"variables" description:"Theme CSS variables JSON."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}
