package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// DashboardSection is one collapsible band of a scrolling dashboard.
// Sections stack in sort order below the widgets
// that belong to no section, and a widget's Y is relative to the top of the
// section holding it, so collapsing one moves everything below it up without
// rewriting a single placement.
type DashboardSection struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	DashboardID uuid.UUID `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Parent dashboard ID."`
	Dashboard   Dashboard `gorm:"foreignKey:DashboardID" json:"-"`
	Title       string    `gorm:"not null" json:"title" description:"Section heading."`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order" description:"Position among the dashboard's sections."`
	// Collapsed is the authored default. A viewer may fold a section open or
	// shut for themselves, but that choice is theirs and is not stored.
	Collapsed bool      `gorm:"not null;default:false" json:"collapsed" description:"Whether the section starts collapsed."`
	CreatedAt time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID if one has not been set.
func (s *DashboardSection) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

// Delete removes a section together with the widgets placed in it. The
// placements have no meaning outside their section — their Y is measured from
// its top — so leaving them behind would scatter them across the grid.
func (s *DashboardSection) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		var widgets []DashboardWidget
		if err := tx.Where("section_id = ?", s.ID).Find(&widgets).Error; err != nil {
			return err
		}
		for _, w := range widgets {
			if err := tx.Where("dashboard_widget_id = ?", w.ID).Delete(&WidgetUpload{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("section_id = ?", s.ID).Delete(&DashboardWidget{}).Error; err != nil {
			return err
		}
		return tx.Delete(&DashboardSection{}, "id = ?", s.ID).Error
	})
}

// FindDashboardSection returns one section by ID.
func FindDashboardSection(id uuid.UUID) (*DashboardSection, error) {
	var section DashboardSection
	err := db.DB.Where("id = ?", id).First(&section).Error
	return &section, err
}

// ListDashboardSections returns a dashboard's sections in display order.
func ListDashboardSections(dashboardID uuid.UUID) ([]DashboardSection, error) {
	var sections []DashboardSection
	err := db.DB.Where("dashboard_id = ?", dashboardID).
		Order("sort_order ASC, created_at ASC").Find(&sections).Error
	return sections, err
}

// NextDashboardSectionOrder returns the sort position a new section takes,
// which is the bottom of the dashboard.
func NextDashboardSectionOrder(dashboardID uuid.UUID) int {
	var maxOrder *int
	db.DB.Model(&DashboardSection{}).Where("dashboard_id = ?", dashboardID).
		Select("MAX(sort_order)").Scan(&maxOrder)
	if maxOrder == nil {
		return 0
	}
	return *maxOrder + 1
}

// FlattenDashboardSections merges every section's widgets into the sectionless
// region and drops the sections. Switching a dashboard back to the fit layout
// calls this: fit has no room below the fold to put a section in, and silently
// stacking every section's placements at the same Y would bury them in each
// other. Grid rows grow to hold the result so nothing lands out of bounds.
func FlattenDashboardSections(d *Dashboard) error {
	sections, err := ListDashboardSections(d.ID)
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		return nil
	}
	widgets, err := ListDashboardWidgets(d.ID)
	if err != nil {
		return err
	}

	// Measure the sectionless region, then each section in turn, offsetting
	// every placement by the rows already consumed above it.
	bySection := make(map[uuid.UUID][]DashboardWidget, len(sections))
	offset := 0
	for _, w := range widgets {
		if w.SectionID == nil {
			if w.Y+w.Height > offset {
				offset = w.Y + w.Height
			}
			continue
		}
		bySection[*w.SectionID] = append(bySection[*w.SectionID], w)
	}

	moves := make(map[uuid.UUID]int, len(widgets))
	for _, section := range sections {
		rows := 0
		for _, w := range bySection[section.ID] {
			moves[w.ID] = w.Y + offset
			if w.Y+w.Height > rows {
				rows = w.Y + w.Height
			}
		}
		offset += rows
	}

	return db.DB.Transaction(func(tx *gorm.DB) error {
		for id, y := range moves {
			if err := tx.Model(&DashboardWidget{}).Where("id = ?", id).
				Updates(map[string]interface{}{"y": y, "section_id": nil}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("dashboard_id = ?", d.ID).Delete(&DashboardSection{}).Error; err != nil {
			return err
		}
		if offset > d.GridRows {
			d.GridRows = offset
			if err := tx.Model(&Dashboard{}).Where("id = ?", d.ID).
				Update("grid_rows", offset).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
