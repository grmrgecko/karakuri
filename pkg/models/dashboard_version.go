package models

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// DashboardVersion is one frozen layout of a dashboard. The dashboard's own
// rows stay the editable draft — every edit still persists immediately — and a
// deploy copies them here under a rising version number, which is the layout
// viewers are served.
type DashboardVersion struct {
	ID          uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	DashboardID uuid.UUID  `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Parent dashboard ID."`
	Version     int        `gorm:"not null" json:"version" description:"Version number, rising from 1."`
	Snapshot    string     `gorm:"type:text" json:"-"`
	Comment     string     `json:"comment" description:"Deploy comment."`
	WidgetCount int        `gorm:"not null;default:0" json:"widget_count" description:"Number of widget placements in this version."`
	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty" description:"User who deployed this version."`
	CreatedBy   *User      `gorm:"foreignKey:CreatedByID" json:"created_by,omitempty" description:"User who deployed this version."`
	CreatedAt   time.Time  `json:"created_at" description:"Creation timestamp."`
}

// DashboardSnapshotWidget is one widget placement inside a version. The
// placement ID is preserved, so uploads and plugin-side per-widget config keyed
// by it still resolve after a deploy or a restore.
type DashboardSnapshotWidget struct {
	ID        uuid.UUID  `json:"id"`
	WidgetID  uint       `json:"widget_id"`
	SectionID *uuid.UUID `json:"section_id,omitempty"`
	X         int        `json:"x"`
	Y         int        `json:"y"`
	Width     int        `json:"width"`
	Height    int        `json:"height"`
	Config    string     `json:"config"`
	// The fields below are omitted when unset, so a placement that uses none
	// of them serializes exactly as it did before they existed.
	PageID  *uuid.UUID     `json:"page_id,omitempty"`
	Region  string         `json:"region,omitempty"`
	Display *WidgetDisplay `json:"display,omitempty"`
}

// DashboardSnapshotSection is one section inside a version. Its ID is preserved
// for the same reason a placement's is: the widgets in the version point at it.
type DashboardSnapshotSection struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	SortOrder int       `json:"sort_order"`
	Collapsed bool      `json:"collapsed"`
}

// DashboardSnapshotPage is one page inside a version, kept under its ID
// because the version's placements point at it.
type DashboardSnapshotPage struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	SortOrder int       `json:"sort_order"`
}

// DashboardSnapshot is the deployable state of a dashboard: layout geometry,
// sections, widget placements, and presentation. Sharing and URL identity
// remain dashboard properties.
type DashboardSnapshot struct {
	Presentation  *DashboardPresentation     `json:"presentation,omitempty"`
	LayoutMode    string                     `json:"layout_mode"`
	GridRows      int                        `json:"grid_rows"`
	GridCols      int                        `json:"grid_cols"`
	RowHeight     int                        `json:"row_height"`
	Padding       int                        `json:"padding"`
	Gap           int                        `json:"gap"`
	WidgetPadding int                        `json:"widget_padding"`
	Sections      []DashboardSnapshotSection `json:"sections,omitempty"`
	Widgets       []DashboardSnapshotWidget  `json:"widgets"`
	Display       *DashboardDisplay          `json:"display,omitempty"`
	Pages         []DashboardSnapshotPage    `json:"pages,omitempty"`
}

// SnapshotDashboard builds a snapshot from a dashboard and its draft sections,
// pages, and placements. Every list is sorted by ID so two snapshots of the
// same layout serialize identically and can be compared as strings.
func SnapshotDashboard(d *Dashboard, widgets []DashboardWidget, sections []DashboardSection, pages []DashboardPage) *DashboardSnapshot {
	snap := &DashboardSnapshot{
		Presentation:  &DashboardPresentation{Name: d.Name, Description: d.Description, ThemeID: d.ThemeID},
		LayoutMode:    d.LayoutMode,
		GridRows:      d.GridRows,
		GridCols:      d.GridCols,
		RowHeight:     d.RowHeight,
		Padding:       d.Padding,
		Gap:           d.Gap,
		WidgetPadding: d.WidgetPadding,
		Sections:      make([]DashboardSnapshotSection, 0, len(sections)),
		Widgets:       make([]DashboardSnapshotWidget, 0, len(widgets)),
	}
	if !d.Display.IsZero() {
		snap.Display = d.Display
	}
	for _, p := range pages {
		snap.Pages = append(snap.Pages, DashboardSnapshotPage{ID: p.ID, Title: p.Title, SortOrder: p.SortOrder})
	}
	for _, s := range sections {
		snap.Sections = append(snap.Sections, DashboardSnapshotSection{
			ID:        s.ID,
			Title:     s.Title,
			SortOrder: s.SortOrder,
			Collapsed: s.Collapsed,
		})
	}
	for _, w := range widgets {
		snap.Widgets = append(snap.Widgets, DashboardSnapshotWidget{
			ID:        w.ID,
			WidgetID:  w.WidgetID,
			SectionID: w.SectionID,
			X:         w.X,
			Y:         w.Y,
			Width:     w.Width,
			Height:    w.Height,
			Config:    w.Config,
			PageID:    w.PageID,
			Region:    w.Region,
			Display:   normalWidgetDisplay(w.Display),
		})
	}
	sort.Slice(snap.Pages, func(i, j int) bool {
		return snap.Pages[i].ID.String() < snap.Pages[j].ID.String()
	})
	sort.Slice(snap.Sections, func(i, j int) bool {
		return snap.Sections[i].ID.String() < snap.Sections[j].ID.String()
	})
	sort.Slice(snap.Widgets, func(i, j int) bool {
		return snap.Widgets[i].ID.String() < snap.Widgets[j].ID.String()
	})
	snap.Normalize()
	return snap
}

// SectionRows rebuilds section rows from a snapshot in display order, so a
// deployed view renders its bands without reading the draft rows.
func (s *DashboardSnapshot) SectionRows(dashboardID uuid.UUID) []DashboardSection {
	rows := make([]DashboardSection, 0, len(s.Sections))
	for _, sec := range s.Sections {
		rows = append(rows, DashboardSection{
			ID:          sec.ID,
			DashboardID: dashboardID,
			Title:       sec.Title,
			SortOrder:   sec.SortOrder,
			Collapsed:   sec.Collapsed,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].SortOrder < rows[j].SortOrder })
	return rows
}

// jsonColumn encodes a nullable JSON column for a map update, which writes
// values as given rather than through the field's serializer.
func jsonColumn[T any](v *T) any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(data)
}

// normalWidgetDisplay stores unset placement display settings as nil, so an
// empty object written by a client compares equal to none at all.
func normalWidgetDisplay(d *WidgetDisplay) *WidgetDisplay {
	if d.IsZero() {
		return nil
	}
	if d.Style.IsZero() && d.Style != nil {
		copied := *d
		copied.Style = nil
		return &copied
	}
	return d
}

// PageRows rebuilds page rows from a snapshot in display order.
func (s *DashboardSnapshot) PageRows(dashboardID uuid.UUID) []DashboardPage {
	rows := make([]DashboardPage, 0, len(s.Pages))
	for _, p := range s.Pages {
		rows = append(rows, DashboardPage{ID: p.ID, DashboardID: dashboardID, Title: p.Title, SortOrder: p.SortOrder})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].SortOrder < rows[j].SortOrder })
	return rows
}

// JSON renders a snapshot for storage. The stable widget order set by
// SnapshotDashboard makes the output comparable, which is how a draft is told
// apart from what was deployed.
func (s *DashboardSnapshot) JSON() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ParseDashboardSnapshot decodes a stored snapshot.
func ParseDashboardSnapshot(data string) (*DashboardSnapshot, error) {
	var snap DashboardSnapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		return nil, err
	}
	snap.Normalize()
	return &snap, nil
}

// Normalize fills in fields a snapshot taken by an older build does not carry.
// A version deployed before layouts were configurable describes a fit
// dashboard, and saying so is what keeps it comparing equal to an unchanged
// draft — otherwise every dashboard would report draft changes after an
// upgrade.
func (s *DashboardSnapshot) Normalize() {
	if !ValidDashboardLayout(s.LayoutMode) {
		s.LayoutMode = DashboardLayoutFit
	}
	if s.RowHeight <= 0 {
		s.RowHeight = defaultRowHeight
	}
}

// WidgetRows rebuilds placement rows from a snapshot with their widget
// definitions preloaded, so a deployed view renders without reading the draft
// rows. A definition whose plugin has since been unloaded comes back zeroed,
// exactly as a stale draft placement would.
func (s *DashboardSnapshot) WidgetRows(dashboardID uuid.UUID) ([]DashboardWidget, error) {
	rows := make([]DashboardWidget, 0, len(s.Widgets))
	if len(s.Widgets) == 0 {
		return rows, nil
	}

	// Load every referenced definition in one query.
	ids := make([]uint, 0, len(s.Widgets))
	for _, w := range s.Widgets {
		ids = append(ids, w.WidgetID)
	}
	var defs []Widget
	if err := db.DB.Where("id IN ?", ids).Find(&defs).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]Widget, len(defs))
	for _, def := range defs {
		byID[def.ID] = def
	}

	for _, w := range s.Widgets {
		rows = append(rows, DashboardWidget{
			ID:          w.ID,
			DashboardID: dashboardID,
			WidgetID:    w.WidgetID,
			Widget:      byID[w.WidgetID],
			SectionID:   w.SectionID,
			X:           w.X,
			Y:           w.Y,
			Width:       w.Width,
			Height:      w.Height,
			Config:      w.Config,
			PageID:      w.PageID,
			Region:      w.Region,
			Display:     w.Display,
		})
	}
	return rows, nil
}

// RestoreDashboardSnapshot copies a version back over the draft: geometry is
// applied to the dashboard, placements are recreated under their original IDs,
// and placements added since the version are removed. Uploads attached to a
// removed placement go with it, so a widget that was deleted and restored comes
// back without the files it once held.
func RestoreDashboardSnapshot(d *Dashboard, s *DashboardSnapshot) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		// Apply geometry. Parsing normalizes a snapshot older than the layout
		// modes, so the values here are always ones this build understands.
		updates := map[string]interface{}{
			"layout_mode":    s.LayoutMode,
			"grid_rows":      s.GridRows,
			"grid_cols":      s.GridCols,
			"row_height":     s.RowHeight,
			"padding":        s.Padding,
			"gap":            s.Gap,
			"widget_padding": s.WidgetPadding,
			"display":        jsonColumn(s.Display),
			// A restore replaces the display, so a writer holding the old
			// revision must not overwrite it unawares.
			"display_revision": gorm.Expr("display_revision + 1"),
		}
		if s.Presentation != nil {
			updates["name"] = s.Presentation.Name
			updates["description"] = s.Presentation.Description
			updates["theme_id"] = s.Presentation.ThemeID
		}
		if err := tx.Model(&Dashboard{}).Where("id = ?", d.ID).Updates(updates).Error; err != nil {
			return err
		}

		// Write the version's sections back under their original IDs, dropping
		// any added since. Placements reference them, so they land first.
		sections := s.SectionRows(d.ID)
		keepSections := make(map[uuid.UUID]bool, len(sections))
		for _, sec := range sections {
			keepSections[sec.ID] = true
		}
		var existingSections []DashboardSection
		if err := tx.Where("dashboard_id = ?", d.ID).Find(&existingSections).Error; err != nil {
			return err
		}
		presentSection := make(map[uuid.UUID]bool, len(existingSections))
		for _, row := range existingSections {
			presentSection[row.ID] = true
			if keepSections[row.ID] {
				continue
			}
			// Widgets are rewritten below, so the section goes on its own
			// rather than through Delete, which would take them with it.
			if err := tx.Delete(&DashboardSection{}, "id = ?", row.ID).Error; err != nil {
				return err
			}
		}
		for _, sec := range sections {
			if presentSection[sec.ID] {
				if err := tx.Model(&DashboardSection{}).Where("id = ?", sec.ID).Updates(map[string]interface{}{
					"title":      sec.Title,
					"sort_order": sec.SortOrder,
					"collapsed":  sec.Collapsed,
				}).Error; err != nil {
					return err
				}
				continue
			}
			row := sec
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}

		// Pages go back the same way sections do, before the placements
		// that point at them.
		pages := s.PageRows(d.ID)
		keepPages := make(map[uuid.UUID]bool, len(pages))
		for _, p := range pages {
			keepPages[p.ID] = true
		}
		var existingPages []DashboardPage
		if err := tx.Where("dashboard_id = ?", d.ID).Find(&existingPages).Error; err != nil {
			return err
		}
		presentPage := make(map[uuid.UUID]bool, len(existingPages))
		for _, row := range existingPages {
			presentPage[row.ID] = true
			if keepPages[row.ID] {
				continue
			}
			if err := tx.Delete(&DashboardPage{}, "id = ?", row.ID).Error; err != nil {
				return err
			}
		}
		for _, p := range pages {
			if presentPage[p.ID] {
				if err := tx.Model(&DashboardPage{}).Where("id = ?", p.ID).Updates(map[string]interface{}{
					"title":      p.Title,
					"sort_order": p.SortOrder,
				}).Error; err != nil {
					return err
				}
				continue
			}
			row := p
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}

		// Drop placements the version does not know about.
		keep := make(map[uuid.UUID]bool, len(s.Widgets))
		for _, w := range s.Widgets {
			keep[w.ID] = true
		}
		var existing []DashboardWidget
		if err := tx.Where("dashboard_id = ?", d.ID).Find(&existing).Error; err != nil {
			return err
		}
		present := make(map[uuid.UUID]bool, len(existing))
		for _, row := range existing {
			present[row.ID] = true
			if keep[row.ID] {
				continue
			}
			if err := tx.Where("dashboard_widget_id = ?", row.ID).Delete(&WidgetUpload{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&DashboardWidget{}, "id = ?", row.ID).Error; err != nil {
				return err
			}
		}

		// Write the version's placements back, recreating any that were deleted.
		for _, w := range s.Widgets {
			row := DashboardWidget{
				ID:          w.ID,
				DashboardID: d.ID,
				WidgetID:    w.WidgetID,
				SectionID:   w.SectionID,
				X:           w.X,
				Y:           w.Y,
				Width:       w.Width,
				Height:      w.Height,
				Config:      w.Config,
				PageID:      w.PageID,
				Region:      w.Region,
				Display:     w.Display,
			}
			if present[w.ID] {
				if err := tx.Model(&DashboardWidget{}).Where("id = ?", w.ID).Updates(map[string]interface{}{
					"widget_id":  w.WidgetID,
					"section_id": w.SectionID,
					"x":          w.X,
					"y":          w.Y,
					"width":      w.Width,
					"height":     w.Height,
					"config":     w.Config,
					"page_id":    w.PageID,
					"region":     w.Region,
					"display":    jsonColumn(row.Display),
					// The config was replaced by the version's.
					"config_revision": gorm.Expr("config_revision + 1"),
				}).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}

		// Keep the in-memory dashboard consistent with what was written.
		s.ApplyPresentation(d)
		d.LayoutMode = s.LayoutMode
		d.GridRows = s.GridRows
		d.GridCols = s.GridCols
		d.RowHeight = s.RowHeight
		d.Padding = s.Padding
		d.Gap = s.Gap
		d.WidgetPadding = s.WidgetPadding
		d.Display = s.Display
		return nil
	})
}

// FindDashboardVersion returns one numbered version of a dashboard.
func FindDashboardVersion(dashboardID uuid.UUID, version int) (*DashboardVersion, error) {
	var v DashboardVersion
	err := db.DB.Where("dashboard_id = ? AND version = ?", dashboardID, version).First(&v).Error
	return &v, err
}

// ListDashboardVersions returns a dashboard's versions, newest first.
func ListDashboardVersions(dashboardID uuid.UUID) ([]*DashboardVersion, error) {
	var versions []*DashboardVersion
	err := db.DB.Preload("CreatedBy").
		Where("dashboard_id = ?", dashboardID).
		Order("version DESC").Find(&versions).Error
	return versions, err
}

// NextDashboardVersion returns the version number the next deploy takes.
func NextDashboardVersion(dashboardID uuid.UUID) (int, error) {
	var maxVersion int
	err := db.DB.Model(&DashboardVersion{}).
		Where("dashboard_id = ?", dashboardID).
		Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error
	return maxVersion + 1, err
}

// DashboardPresentation holds the authoring settings frozen with a layout.
type DashboardPresentation struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ThemeID     *uint  `json:"theme_id"`
}

// ApplyPresentation selects the deployed title and theme for a viewer.
func (s *DashboardSnapshot) ApplyPresentation(d *Dashboard) {
	if s.Presentation == nil {
		return
	}
	d.Name, d.Description, d.ThemeID = s.Presentation.Name, s.Presentation.Description, s.Presentation.ThemeID
	d.Theme = nil
}
