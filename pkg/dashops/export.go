package dashops

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// ExportDoc is the portable dashboard document format: the draft layout with
// every widget named by its package rather than its local registry ID, so the
// file resolves against whatever a target instance has installed. Owner,
// sharing, and public visibility are deployment facts and stay behind, as do
// widget uploads: the files live outside the layout and their IDs would not
// resolve elsewhere.
type ExportDoc struct {
	KarakuriDashboard int                      `json:"karakuri_dashboard" required:"true" description:"Document format version; currently 1."`
	Name              string                   `json:"name" description:"Dashboard display name."`
	Slug              string                   `json:"slug,omitempty" description:"Preferred slug; a free one is chosen if taken."`
	Description       string                   `json:"description,omitempty" description:"Dashboard summary."`
	Theme             string                   `json:"theme,omitempty" description:"Theme package name, resolved by name on import."`
	Grid              ExportGrid               `json:"grid" description:"Grid geometry."`
	Sections          []ExportSection          `json:"sections,omitempty" description:"Collapsible sections, in display order."`
	Pages             []ExportPage             `json:"pages,omitempty" description:"Pages, in display order."`
	Display           *models.DashboardDisplay `json:"display,omitempty" description:"Page rotation, dimming, burn-in, and variable settings."`
	Widgets           []ExportWidget           `json:"widgets" description:"Widget placements."`
}

// ExportPage is one page in an exported dashboard. Placements point at it by
// position, the same way they point at sections.
type ExportPage struct {
	Title string `json:"title" description:"Page title."`
}

// ExportGrid is the geometry half of an exported dashboard. The spacing
// fields are pointers because zero is a real value, the one an edge-to-edge
// dashboard uses, and has to be told apart from a document that names none.
type ExportGrid struct {
	Layout        string `json:"layout,omitempty" description:"Layout mode: fit, scroll, or regions. Absent reads as fit."`
	Rows          int    `json:"rows" description:"Number of grid rows; fit layout only."`
	Cols          int    `json:"cols" description:"Number of grid columns."`
	RowHeight     int    `json:"row_height,omitempty" description:"Height of one grid row in pixels; scroll layout only."`
	Padding       *int   `json:"padding,omitempty" description:"Outer padding in pixels; absent reads as 8."`
	Gap           *int   `json:"gap,omitempty" description:"Gap between widgets in pixels; absent reads as 8."`
	WidgetPadding *int   `json:"widget_padding,omitempty" description:"Inner widget padding in pixels; absent reads as 4."`
}

// Spacing a new dashboard has when a document names none, matching the
// column defaults.
const (
	defaultPadding       = 8
	defaultGap           = 8
	defaultWidgetPadding = 4
)

// spacingOr returns the spacing a document states, or fallback when it states
// none.
func spacingOr(v *int, fallback int) int {
	if v == nil {
		return fallback
	}
	return *v
}

// ExportSection is one section in an exported dashboard. Placements point at
// it by its position in the list rather than by ID, since the IDs are this
// install's and the importer mints its own.
type ExportSection struct {
	Title     string `json:"title" description:"Section heading."`
	Collapsed bool   `json:"collapsed,omitempty" description:"Whether the section starts collapsed."`
}

// ExportWidget is one placement in an exported dashboard. Package is the
// portable identity; Version records what it was authored against and is
// informational only.
type ExportWidget struct {
	Package string `json:"package" required:"true" description:"Widget package name."`
	Version string `json:"version,omitempty" description:"Widget version at export time."`
	// Section indexes into the document's sections; absent places the widget
	// in the sectionless region above them.
	Section *int   `json:"section,omitempty" description:"Index into the document's sections; absent for the sectionless region."`
	X       int    `json:"x" description:"Grid column position."`
	Y       int    `json:"y" description:"Grid row position, relative to the top of its section."`
	Width   int    `json:"width" description:"Width in grid units."`
	Height  int    `json:"height" description:"Height in grid units."`
	Config  string `json:"config,omitempty" description:"Widget instance configuration JSON."`
	// Page indexes into the document's pages; absent shows the placement on
	// every page.
	Page    *int                  `json:"page,omitempty" description:"Index into the document's pages; absent for every page."`
	Region  string                `json:"region,omitempty" description:"Region name in the region layout."`
	Display *models.WidgetDisplay `json:"display,omitempty" description:"Name, visibility conditions, animations, tile style, and status cues."`
}

// availableSlug returns a slug no dashboard the owner can see uses yet: the
// wanted one, then "-imported" and numbered variants. A dashboard they cannot
// see takes no part, as when creating one, so the suffix confirms nothing
// private.
func availableSlug(ctx context.Context, ownerID uuid.UUID, wanted string) string {
	base := models.Slugify(wanted)
	if base == "" {
		base = "dashboard"
	}
	candidates := []string{base, base + "-imported"}
	for i := 2; i <= 50; i++ {
		candidates = append(candidates, fmt.Sprintf("%s-imported-%d", base, i))
	}
	for _, slug := range candidates {
		if !SlugVisible(ctx, ownerID, slug) {
			return slug
		}
	}
	return fmt.Sprintf("%s-imported-%s", base, uuid.New().String()[:8])
}

// firstNonEmpty returns the first non-empty string, for falling back from a
// document's preferred slug to its name.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Export builds the portable document for a dashboard's draft layout.
// Sections travel in display order, and placements point at them by position:
// the IDs are this install's and mean nothing elsewhere.
func Export(dashboard *models.Dashboard) (*ExportDoc, error) {
	widgets, err := models.ListDashboardWidgets(dashboard.ID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	sections, err := models.ListDashboardSections(dashboard.ID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}

	pages, err := models.ListDashboardPages(dashboard.ID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	docPages := make([]ExportPage, 0, len(pages))
	pageIndex := make(map[uuid.UUID]int, len(pages))
	for i, p := range pages {
		pageIndex[p.ID] = i
		docPages = append(docPages, ExportPage{Title: p.Title})
	}

	bands := make([]ExportSection, 0, len(sections))
	indexByID := make(map[uuid.UUID]int, len(sections))
	for i, s := range sections {
		indexByID[s.ID] = i
		bands = append(bands, ExportSection{Title: s.Title, Collapsed: s.Collapsed})
	}

	// Name each placement by package. A row whose widget row has gone
	// missing has nothing portable to say, so the export refuses rather
	// than writing a placement nothing can resolve.
	placements := make([]ExportWidget, 0, len(widgets))
	for _, w := range widgets {
		if w.Widget.PackageName == "" {
			return nil, apperrors.ErrValidation.
				WithMessage("cannot export: a placed widget is no longer registered")
		}
		var section *int
		if w.SectionID != nil {
			if idx, ok := indexByID[*w.SectionID]; ok {
				section = &idx
			}
		}
		var page *int
		if w.PageID != nil {
			if idx, ok := pageIndex[*w.PageID]; ok {
				page = &idx
			}
		}
		placements = append(placements, ExportWidget{
			Page:    page,
			Region:  w.Region,
			Display: w.Display,
			Package: w.Widget.PackageName,
			Version: w.Widget.Version,
			Section: section,
			X:       w.X,
			Y:       w.Y,
			Width:   w.Width,
			Height:  w.Height,
			Config:  w.Config,
		})
	}

	doc := &ExportDoc{
		KarakuriDashboard: 1,
		Name:              dashboard.Name,
		Slug:              dashboard.Slug,
		Description:       dashboard.Description,
		Grid: ExportGrid{
			Layout:        dashboard.LayoutMode,
			Rows:          dashboard.GridRows,
			Cols:          dashboard.GridCols,
			RowHeight:     dashboard.RowHeight,
			Padding:       &dashboard.Padding,
			Gap:           &dashboard.Gap,
			WidgetPadding: &dashboard.WidgetPadding,
		},
		Sections: bands,
		Pages:    docPages,
		Widgets:  placements,
	}
	if !dashboard.Display.IsZero() {
		doc.Display = dashboard.Display
	}
	if dashboard.ThemeID != nil {
		var theme models.Theme
		if err := db.DB.First(&theme, *dashboard.ThemeID).Error; err == nil {
			doc.Theme = theme.PackageName
		}
	}
	return doc, nil
}

// Import creates a new dashboard from an exported document owned by the given
// user. The layout lands as the draft, private and undeployed: what viewers
// see is the importer's call, not the file's.
func Import(ctx context.Context, doc *ExportDoc, ownerID uuid.UUID) (*models.Dashboard, error) {
	// Validate the document shape.
	if doc.KarakuriDashboard != 1 {
		return nil, apperrors.ErrValidation.WithMessage("unsupported document: karakuri_dashboard must be 1")
	}
	name := strings.TrimSpace(doc.Name)
	if name == "" {
		name = "Imported dashboard"
	}

	// Resolve every widget package before writing anything: a dashboard
	// imported with holes would be worse than one that refused, since the
	// fix is to install the plugin and import again.
	byPackage := make(map[string]models.Widget)
	missing := make(map[string]bool)
	for _, w := range doc.Widgets {
		if _, ok := byPackage[w.Package]; ok {
			continue
		}
		var widget models.Widget
		if err := db.DB.Where("package_name = ?", w.Package).First(&widget).Error; err != nil {
			missing[w.Package] = true
			continue
		}
		byPackage[w.Package] = widget
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, apperrors.ErrValidation.
			WithMessage("cannot import: no widget installed for " + strings.Join(names, ", "))
	}

	// Theme references a package name; an unknown one leaves the site
	// default rather than failing an otherwise importable layout.
	var themeID *uint
	if doc.Theme != "" {
		var theme models.Theme
		if err := db.DB.Where("package_name = ?", doc.Theme).First(&theme).Error; err == nil {
			themeID = &theme.ID
		}
	}

	grid := doc.Grid
	if grid.Rows <= 0 {
		grid.Rows = 12
	}
	if grid.Cols <= 0 {
		grid.Cols = 12
	}
	// A document written before layouts were configurable names none, and
	// what it describes is a fit dashboard.
	if !models.ValidDashboardLayout(grid.Layout) {
		grid.Layout = models.DashboardLayoutFit
	}
	if grid.RowHeight <= 0 {
		grid.RowHeight = 80
	}
	padding := spacingOr(grid.Padding, defaultPadding)
	gap := spacingOr(grid.Gap, defaultGap)
	widgetPadding := spacingOr(grid.WidgetPadding, defaultWidgetPadding)
	// Sections only exist in the scrolling layout, so a document that
	// carries both a fit layout and sections is contradictory; the layout
	// wins and the placements land in the sectionless region.
	docSections := doc.Sections
	if grid.Layout != models.DashboardLayoutScroll {
		docSections = nil
	}

	if err := ValidateDisplay(doc.Display); err != nil {
		return nil, err
	}
	if err := CheckVariableQueries(ctx, ownerID, nil, doc.Display); err != nil {
		return nil, err
	}
	for _, w := range doc.Widgets {
		if err := ValidateWidgetDisplay(w.Display, false); err != nil {
			return nil, err
		}
		if err := validateRegion(w.Region); err != nil {
			return nil, err
		}
		// Decided before the transaction opens: the permission lookup reads
		// the database, and a refusal is the importer's to see as it is.
		if err := checkChartQueries(ctx, ownerID, byPackage[w.Package].PackageName, "", w.Config); err != nil {
			return nil, err
		}
	}
	var display *models.DashboardDisplay
	if !doc.Display.IsZero() {
		display = doc.Display
	}

	dashboard := &models.Dashboard{
		Display:       display,
		ID:            uuid.New(),
		Name:          name,
		Slug:          availableSlug(ctx, ownerID, firstNonEmpty(doc.Slug, name)),
		Description:   doc.Description,
		OwnerID:       ownerID,
		ThemeID:       themeID,
		LayoutMode:    grid.Layout,
		GridRows:      grid.Rows,
		GridCols:      grid.Cols,
		RowHeight:     grid.RowHeight,
		Padding:       padding,
		Gap:           gap,
		WidgetPadding: widgetPadding,
	}

	// Create the dashboard, its sections, and its placements together.
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(dashboard).Error; err != nil {
			return err
		}
		// The spacing columns carry defaults, and an insert leaves a zero to
		// its default, so the document's values are written explicitly.
		if err := tx.Model(&models.Dashboard{}).Where("id = ?", dashboard.ID).UpdateColumns(map[string]any{
			"padding": padding, "gap": gap, "widget_padding": widgetPadding,
		}).Error; err != nil {
			return err
		}
		sectionIDs := make([]uuid.UUID, 0, len(docSections))
		for i, s := range docSections {
			title := strings.TrimSpace(s.Title)
			if title == "" {
				title = fmt.Sprintf("Section %d", i+1)
			}
			row := &models.DashboardSection{
				ID:          uuid.New(),
				DashboardID: dashboard.ID,
				Title:       title,
				SortOrder:   i,
				Collapsed:   s.Collapsed,
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
			sectionIDs = append(sectionIDs, row.ID)
		}
		pageIDs := make([]uuid.UUID, 0, len(doc.Pages))
		for i, p := range doc.Pages {
			title := strings.TrimSpace(p.Title)
			if title == "" {
				title = fmt.Sprintf("Page %d", i+1)
			}
			row := &models.DashboardPage{ID: uuid.New(), DashboardID: dashboard.ID, Title: title, SortOrder: i}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
			pageIDs = append(pageIDs, row.ID)
		}
		for _, w := range doc.Widgets {
			widget := byPackage[w.Package]
			var pageID *uuid.UUID
			if w.Page != nil && *w.Page >= 0 && *w.Page < len(pageIDs) {
				pageID = &pageIDs[*w.Page]
			}
			// An index the document does not have is dropped rather than
			// failing the import: the placement still has a home.
			var sectionID *uuid.UUID
			if w.Section != nil && *w.Section >= 0 && *w.Section < len(sectionIDs) {
				sectionID = &sectionIDs[*w.Section]
			}
			row := &models.DashboardWidget{
				ID:          uuid.New(),
				DashboardID: dashboard.ID,
				WidgetID:    widget.ID,
				SectionID:   sectionID,
				X:           w.X,
				Y:           w.Y,
				Width:       w.Width,
				Height:      w.Height,
				Config:      w.Config,
				PageID:      pageID,
				Region:      w.Region,
				Display:     normalizeWidgetDisplay(w.Display),
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(fmt.Errorf("failed to import dashboard: %w", err))
	}

	// Reload the stored rows so the caller sees what was written.
	imported, err := models.FindDashboardByID(dashboard.ID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	return imported, nil
}
