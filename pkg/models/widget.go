package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// Widget describes a registered visualization component available for placement on dashboards.
type Widget struct {
	ID              uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name            string    `gorm:"not null" json:"name" description:"Display name."`
	PluginName      string    `gorm:"index" json:"plugin_name" description:"Plugin contributing this widget."`
	Description     string    `json:"description" description:"Widget description."`
	PackageName     string    `gorm:"uniqueIndex;not null" json:"package_name" description:"Package identifier."`
	TagName         string    `gorm:"not null" json:"tag_name" description:"HTML custom element tag."`
	ConfigTagName   string    `json:"config_tag_name" description:"Config editor custom element tag."`
	Version         string    `gorm:"not null" json:"version" description:"Semantic version."`
	Author          string    `json:"author" description:"Author name."`
	ConfigSchema    string    `gorm:"type:json" json:"config_schema,omitempty" description:"JSON schema for configuration."`
	DefaultConfig   string    `gorm:"type:json" json:"default_config,omitempty" description:"Default configuration JSON."`
	Tags            string    `gorm:"type:json" json:"tags" description:"Categorization tags JSON array."`
	EntryPoint      string    `json:"entry_point" description:"JavaScript entry file name."`
	DefaultProvider string    `json:"default_provider,omitempty" description:"Default provider name for this widget."`
	CreatedAt       time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt       time.Time `json:"updated_at" description:"Last update timestamp."`
}

// ListWidgets returns a paginated list of the widgets available to place:
// those whose contributing plugin is loaded. Filtering in the query rather
// than after it keeps the pagination total honest.
func ListWidgets(page, pageSize int, available []string) ([]*Widget, int64, error) {
	var widgets []*Widget
	var count int64

	query := db.DB.Model(&Widget{}).Where("package_name IN ?", available)
	query.Count(&count)

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("name ASC").Find(&widgets).Error
	return widgets, count, err
}

// MaxUploadPosition returns the highest position value among uploads for the given widget, or -1 if none exist.
func MaxUploadPosition(dashboardWidgetID uuid.UUID) (int, error) {
	var maxPos *int
	err := db.DB.Model(&WidgetUpload{}).
		Where("dashboard_widget_id = ?", dashboardWidgetID).
		Select("COALESCE(MAX(position), -1)").
		Scan(&maxPos).Error
	if err != nil {
		return -1, err
	}
	return *maxPos, nil
}

// ReorderUploads sets the position of each upload to its index in the provided slice.
func ReorderUploads(dashboardWidgetID uuid.UUID, uploadIDs []uuid.UUID) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		for i, id := range uploadIDs {
			result := tx.Model(&WidgetUpload{}).
				Where("id = ? AND dashboard_widget_id = ?", id, dashboardWidgetID).
				Update("position", i)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}
		return nil
	})
}

// WidgetUpload tracks a file uploaded as part of a dashboard widget's content.
type WidgetUpload struct {
	ID                uuid.UUID       `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	DashboardWidgetID uuid.UUID       `gorm:"type:uuid;not null;index" json:"dashboard_widget_id" description:"Parent dashboard widget ID."`
	DashboardWidget   DashboardWidget `gorm:"foreignKey:DashboardWidgetID" json:"-"`
	FileName          string          `gorm:"not null" json:"file_name" description:"Original file name."`
	StoragePath       string          `gorm:"not null" json:"storage_path" description:"Server-side storage path."`
	ContentType       string          `gorm:"not null" json:"content_type" description:"MIME content type."`
	Size              int64           `gorm:"not null" json:"size" description:"File size in bytes."`
	Position          int             `gorm:"not null;default:0" json:"position" description:"Display order position."`
	CreatedAt         time.Time       `json:"created_at" description:"Creation timestamp."`
	UpdatedAt         time.Time       `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID if one has not been set.
func (wu *WidgetUpload) BeforeCreate(tx *gorm.DB) error {
	if wu.ID == uuid.Nil {
		wu.ID = uuid.New()
	}
	return nil
}
