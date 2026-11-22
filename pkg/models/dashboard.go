package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
)

// DashboardGroup organizes dashboards into a nested tree with optional sharing.
// A nil ParentID places the group at the root beside ungrouped dashboards; the
// sharing rows hang off the group itself, so a subgroup is shared by sharing it
// rather than by inheriting from its parent.
type DashboardGroup struct {
	ID          uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name        string    `gorm:"not null" json:"name" description:"Display name."`
	Description string    `json:"description" description:"Optional summary."`
	ParentID    *uint     `gorm:"index" json:"parent_id,omitempty" description:"Parent group ID; absent at the root."`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order" description:"Position for manual ordering."`
	OwnerID     uuid.UUID `gorm:"type:uuid;not null" json:"owner_id" description:"Owner user ID."`
	Owner       User      `gorm:"foreignKey:OwnerID" json:"owner,omitempty" description:"Owner user."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}

// Delete removes a dashboard group, unsets group_id on child dashboards, lifts
// subgroups to the deleted group's parent so none are orphaned, and drops the
// access entries.
func (m *DashboardGroup) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		tx.Model(&Dashboard{}).Where("group_id = ?", m.ID).Update("group_id", nil)
		tx.Model(&DashboardGroup{}).Where("parent_id = ?", m.ID).Update("parent_id", m.ParentID)
		tx.Where("dashboard_group_id = ?", m.ID).Delete(&DashboardGroupAccess{})
		return tx.Delete(&DashboardGroup{}, m.ID).Error
	})
}

// DashboardGroupIsDescendantOf reports whether candidate is the group itself or
// sits anywhere beneath it. Move operations use this to reject cycles.
func DashboardGroupIsDescendantOf(candidateID, groupID uint) (bool, error) {
	current := candidateID
	for i := 0; i < maxFolderDepth; i++ {
		if current == groupID {
			return true, nil
		}
		g, err := FindDashboardGroupByID(current)
		if err != nil {
			return false, err
		}
		if g.ParentID == nil {
			return false, nil
		}
		current = *g.ParentID
	}
	return true, nil
}

// FindDashboardGroupByID returns a dashboard group with its owner preloaded.
func FindDashboardGroupByID(id uint) (*DashboardGroup, error) {
	var group DashboardGroup
	err := db.DB.Preload("Owner").First(&group, id).Error
	return &group, err
}

// ListDashboardGroupsByUser returns groups owned by the user or shared with them via direct or group access.
func ListDashboardGroupsByUser(ownerID uuid.UUID, userGroupIDs []uint) ([]*DashboardGroup, error) {
	var groups []*DashboardGroup

	sub := db.DB.Model(&DashboardGroupAccess{}).Select("dashboard_group_id").Where("user_id = ?", ownerID)
	if len(userGroupIDs) > 0 {
		sub = sub.Or("group_id IN ?", userGroupIDs)
	}

	query := db.DB.Model(&DashboardGroup{}).Where(
		"owner_id = ? OR id IN (?)",
		ownerID,
		sub,
	)

	err := query.Preload("Owner").Order("sort_order ASC, name ASC").Find(&groups).Error
	return groups, err
}

// ListDashboardGroupAccess returns all access entries for a group with preloaded relations.
func ListDashboardGroupAccess(groupID uint) ([]DashboardGroupAccess, error) {
	var entries []DashboardGroupAccess
	err := db.DB.Preload("User").Preload("Group").Where("dashboard_group_id = ?", groupID).Find(&entries).Error
	return entries, err
}

// DashboardGroupAccess grants a user or user group access to all dashboards within a dashboard group.
type DashboardGroupAccess struct {
	ID               uint           `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	DashboardGroupID uint           `gorm:"not null;index" json:"dashboard_group_id" description:"Parent dashboard group ID."`
	DashboardGroup   DashboardGroup `gorm:"foreignKey:DashboardGroupID" json:"-"`
	UserID           *uuid.UUID     `gorm:"type:uuid" json:"user_id,omitempty" description:"Granted user ID."`
	User             *User          `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Granted user."`
	GroupID          *uint          `json:"group_id,omitempty" description:"Granted user group ID."`
	Group            *UserGroup     `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Granted user group."`
	Permission       string         `gorm:"not null" json:"permission" description:"Permission level: read (look only), view (look and operate), edit, or admin."`
	CreatedAt        time.Time      `json:"created_at" description:"Creation timestamp."`
}

// Dashboard layout modes. Fit is the wall-panel layout: rows and columns both
// scale to the viewport and everything is on screen at once. Scroll is the
// long-page layout: columns scale to the width, a row is a fixed number of
// pixels tall, and the grid keeps going below the fold.
const (
	DashboardLayoutFit    = "fit"
	DashboardLayoutScroll = "scroll"
)

// defaultRowHeight is the pixel height one scrolling-layout row takes when a
// dashboard names none, matching the column default's size on a wide screen.
const defaultRowHeight = 80

// ValidDashboardLayout reports whether a layout mode is one this build knows.
func ValidDashboardLayout(mode string) bool {
	return mode == DashboardLayoutFit || mode == DashboardLayoutScroll || mode == DashboardLayoutRegions
}

// Dashboard represents a user-owned layout containing positioned widgets.
type Dashboard struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name        string    `gorm:"not null" json:"name" description:"Display name."`
	// Slug is a readable address. It need not be unique: a slug resolves to the
	// one dashboard the caller can see with it, and the ID is canonical.
	Slug        string    `gorm:"index;not null" json:"slug" description:"Readable address; not unique. The ID is canonical."`
	Description string    `json:"description" description:"Optional summary."`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order" description:"Position for manual ordering."`
	OwnerID     uuid.UUID `gorm:"type:uuid;not null" json:"owner_id" description:"Owner user ID."`
	Owner       User      `gorm:"foreignKey:OwnerID" json:"owner,omitempty" description:"Owner user."`
	IsPublic    bool      `gorm:"default:false" json:"is_public" description:"Whether publicly accessible."`
	// PublicControl decides whether an anonymous viewer of a public dashboard
	// may operate what it shows or only watch it. Reading is what publishing a
	// dashboard means; switching the lights from a URL anyone can guess is a
	// separate decision, so it is off until the author makes it.
	PublicControl bool            `gorm:"default:false" json:"public_control" description:"Whether anonymous viewers of a public dashboard may operate its controls."`
	GroupID       *uint           `json:"group_id,omitempty" description:"Dashboard group ID."`
	Group         *DashboardGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Dashboard group."`
	ThemeID       *uint           `json:"theme_id" description:"Applied theme ID."`
	Theme         *Theme          `gorm:"foreignKey:ThemeID" json:"theme,omitempty" description:"Applied theme."`
	// LayoutMode decides how the grid meets the screen: fit scales rows and
	// columns to the viewport so the whole dashboard is always visible, scroll
	// scales only the columns and gives every row a fixed height, letting the
	// dashboard run past the fold.
	LayoutMode    string `gorm:"not null;default:fit" json:"layout_mode" description:"Layout mode: fit, scroll, or regions."` // Valid values: fit, scroll, regions.
	GridRows      int    `gorm:"not null;default:12" json:"grid_rows" description:"Number of grid rows; fit layout only."`
	GridCols      int    `gorm:"not null;default:12" json:"grid_cols" description:"Number of grid columns."`
	RowHeight     int    `gorm:"not null;default:80" json:"row_height" description:"Height of one grid row in pixels; scroll and region layouts."`
	Padding       int    `gorm:"not null;default:8" json:"padding" description:"Outer padding in pixels."`
	Gap           int    `gorm:"not null;default:8" json:"gap" description:"Gap between widgets in pixels."`
	WidgetPadding int    `gorm:"not null;default:4" json:"widget_padding" description:"Inner widget padding in pixels."`
	// Display holds the always-on screen settings: page rotation, dimming,
	// burn-in shift, and variables. Nil when none are set.
	Display *DashboardDisplay `gorm:"serializer:json;type:json" json:"display,omitempty" description:"Page rotation, dimming, burn-in, and variable settings."`
	// DisplayRevision rises each time the display settings are replaced.
	DisplayRevision int `gorm:"not null;default:0" json:"display_revision" description:"Rises with each display replacement; pass it back as expected_display_revision."`
	// DeployedVersion is the version viewers are served; 0 means the dashboard
	// has never been deployed and viewers fall back to the draft.
	DeployedVersion int       `gorm:"not null;default:0" json:"deployed_version" description:"Deployed version number; 0 when never deployed."`
	CreatedAt       time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt       time.Time `json:"updated_at" description:"Last update timestamp."`

	Widgets  []DashboardWidget  `gorm:"foreignKey:DashboardID" json:"widgets,omitempty" description:"Placed widgets."`
	Sections []DashboardSection `gorm:"foreignKey:DashboardID" json:"sections,omitempty" description:"Collapsible sections; scroll layout only."`
	Pages    []DashboardPage    `gorm:"foreignKey:DashboardID" json:"pages,omitempty" description:"Pages placements are shown on, in display order."`
	// HasDraftChanges is computed, not stored, and only filled in on
	// single-dashboard responses where the widgets are loaded anyway.
	HasDraftChanges bool `gorm:"-" json:"has_draft_changes" description:"Whether the draft layout differs from the deployed version."`
	// CanOperate is computed, not stored, on single-dashboard responses. It
	// tells the viewer whether to draw controls or only readings; the server
	// enforces the same decision on every write.
	CanOperate bool `gorm:"-" json:"can_operate" description:"Whether the caller may operate what the dashboard shows."`
}

// BeforeCreate assigns a UUID and generates a slug from the name if not set.
func (d *Dashboard) BeforeCreate(tx *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if d.Slug == "" {
		d.Slug = Slugify(d.Name)
	}
	return nil
}

// Delete removes a dashboard and its widgets, uploads, and access entries in a transaction.
func (m *Dashboard) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		var widgets []DashboardWidget
		tx.Where("dashboard_id = ?", m.ID).Find(&widgets)
		for _, w := range widgets {
			tx.Where("dashboard_widget_id = ?", w.ID).Delete(&WidgetUpload{})
		}
		tx.Where("dashboard_id = ?", m.ID).Delete(&DashboardWidget{})
		tx.Where("dashboard_id = ?", m.ID).Delete(&DashboardSection{})
		tx.Where("dashboard_id = ?", m.ID).Delete(&DashboardPage{})
		tx.Where("dashboard_id = ?", m.ID).Delete(&DashboardAccess{})
		tx.Where("dashboard_id = ?", m.ID).Delete(&DashboardVersion{})
		// Render rows point at a dashboard that is about to stop existing. The
		// files the jobs left behind are swept by the render sweeper, which
		// reads the directory rather than the table.
		tx.Where("dashboard_id = ?", m.ID).Delete(&RenderSchedule{})
		tx.Where("dashboard_id = ?", m.ID).Delete(&RenderJob{})
		// A render key names this dashboard and opens nothing else, so it
		// goes with it.
		tx.Where("dashboard_id = ?", m.ID).Delete(&APIKey{})
		return tx.Delete(&Dashboard{}, "id = ?", m.ID).Error
	})
}

// FindDashboardByID returns a dashboard with all standard preloads.
func FindDashboardByID(id uuid.UUID) (*Dashboard, error) {
	var dashboard Dashboard
	err := db.DB.Preload("Widgets").Preload("Widgets.Widget").
		Preload("Sections", func(tx *gorm.DB) *gorm.DB { return tx.Order("sort_order ASC, created_at ASC") }).
		Preload("Pages", func(tx *gorm.DB) *gorm.DB { return tx.Order("sort_order ASC, created_at ASC") }).
		Preload("Owner").Preload("Theme").
		Preload("Group").Preload("Group.Owner").
		Where("id = ?", id).First(&dashboard).Error
	if err != nil {
		return nil, err
	}
	return &dashboard, nil
}

// ErrSlugAmbiguous reports a slug that names more than one dashboard or flow
// among those the lookup may consider.
var ErrSlugAmbiguous = errors.New("several items use this slug")

// DashboardsWithSlug returns every dashboard using a slug, with its owner, for
// callers that filter them by what a user may see.
func DashboardsWithSlug(slug string) ([]*Dashboard, error) {
	var out []*Dashboard
	err := db.DB.Preload("Owner").Where("slug = ?", slug).Order("name ASC").Find(&out).Error
	return out, err
}

// LookupDashboardVisible resolves a dashboard by ID, or by a slug among the
// dashboards visible accepts; a nil visible accepts every dashboard. A slug
// no accepted dashboard uses is not found, and one several use is
// ErrSlugAmbiguous, so a slug a caller cannot see never decides the answer.
func LookupDashboardVisible(ref string, visible func(*Dashboard) bool) (*Dashboard, error) {
	if id, err := uuid.Parse(ref); err == nil {
		var dashboard Dashboard
		if err := db.DB.Where("id = ?", id).First(&dashboard).Error; err != nil {
			return nil, err
		}
		return &dashboard, nil
	}
	matches, err := DashboardsWithSlug(ref)
	if err != nil {
		return nil, err
	}
	var found *Dashboard
	for _, d := range matches {
		if visible != nil && !visible(d) {
			continue
		}
		if found != nil {
			return nil, ErrSlugAmbiguous
		}
		found = d
	}
	if found == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return found, nil
}

// LookupDashboard resolves a dashboard by ID or slug without any preloads. It
// is for the paths that need the row's own fields and nothing hanging off it —
// authorization asks whether the dashboard is public and which version is
// deployed, and loading its widgets, owner, and theme to answer that would put
// four joins on every request a wall panel makes.
//
// A slug is matched among every dashboard, so it resolves only when exactly
// one uses it. Paths acting for a user should prefer LookupDashboardVisible.
func LookupDashboard(ref string) (*Dashboard, error) {
	return LookupDashboardVisible(ref, nil)
}

// ListDashboards returns a paginated list of dashboards with owner and group preloads.
func ListDashboards(page, pageSize int) ([]*Dashboard, int64, error) {
	var dashboards []*Dashboard
	var count int64

	db.DB.Model(&Dashboard{}).Count(&count)

	offset := (page - 1) * pageSize
	err := db.DB.Preload("Owner").Preload("Group").Preload("Group.Owner").
		Offset(offset).Limit(pageSize).Order("sort_order ASC, name ASC").Find(&dashboards).Error
	return dashboards, count, err
}

// ListPublicDashboards returns only dashboards marked public, for anonymous listings.
func ListPublicDashboards(page, pageSize int) ([]*Dashboard, int64, error) {
	var dashboards []*Dashboard
	var count int64

	query := db.DB.Model(&Dashboard{}).Where("is_public = ?", true)
	query.Count(&count)

	offset := (page - 1) * pageSize
	err := query.Preload("Owner").Preload("Group").Preload("Group.Owner").
		Offset(offset).Limit(pageSize).Order("sort_order ASC, name ASC").Find(&dashboards).Error
	return dashboards, count, err
}

// Dashboard access levels, ordered least to most privileged. A grant of one
// level implies every level below it. Read shows the dashboard without
// operating it; view also operates what it shows.
const (
	DashboardAccessRead  = "read"
	DashboardAccessView  = "view"
	DashboardAccessEdit  = "edit"
	DashboardAccessAdmin = "admin"
)

// dashboardAccessRank orders the levels so a comparison decides whether a
// grant satisfies a request.
var dashboardAccessRank = map[string]int{
	DashboardAccessRead:  1,
	DashboardAccessView:  2,
	DashboardAccessEdit:  3,
	DashboardAccessAdmin: 4,
}

// ValidDashboardAccess reports whether level names a dashboard access level.
// A share stored with any other string would grant nothing while still
// looking like a share.
func ValidDashboardAccess(level string) bool {
	_, ok := dashboardAccessRank[level]
	return ok
}

// CanAccessDashboard reports whether a user may act on a dashboard at the
// requested level. Access is owner-first, then explicit grants (direct or
// through a user group, on the dashboard or its dashboard group); a public
// dashboard is viewable by anyone, including anonymous callers, but never
// editable by them.
func CanAccessDashboard(dashboardID uuid.UUID, userID uuid.UUID, userGroupIDs []uint, level string) (bool, error) {
	want, ok := dashboardAccessRank[level]
	if !ok {
		return false, nil
	}

	var dashboard Dashboard
	if err := db.DB.Where("id = ?", dashboardID).First(&dashboard).Error; err != nil {
		return false, err
	}
	if dashboard.IsPublic && want == dashboardAccessRank[DashboardAccessRead] {
		return true, nil
	}
	held, err := dashboardGrantRank(&dashboard, userID, userGroupIDs)
	if err != nil {
		return false, err
	}
	return held >= want, nil
}

// HasDashboardOperateGrant reports whether a user was deliberately given a
// dashboard to operate: as its owner, through a share at view level or above
// on it or on its dashboard group, or as that group's owner. A public
// dashboard grants nothing here, because being able to see it is not the same
// as having been shared it, and neither does a read share.
func HasDashboardOperateGrant(dashboardID uuid.UUID, userID uuid.UUID, userGroupIDs []uint) (bool, error) {
	var dashboard Dashboard
	if err := db.DB.Where("id = ?", dashboardID).First(&dashboard).Error; err != nil {
		return false, err
	}
	held, err := dashboardGrantRank(&dashboard, userID, userGroupIDs)
	return held >= dashboardAccessRank[DashboardAccessView], err
}

// CanAccessDashboardGroup reports whether a user may act on a dashboard group
// at the requested level: its owner, or a grant on it held directly or
// through a user group.
func CanAccessDashboardGroup(groupID uint, userID uuid.UUID, userGroupIDs []uint, level string) (bool, error) {
	want, ok := dashboardAccessRank[level]
	if !ok {
		return false, nil
	}
	held, err := dashboardGroupGrantRank(groupID, userID, userGroupIDs)
	if err != nil {
		return false, err
	}
	return held >= want, nil
}

// dashboardGrantRank returns the highest access rank a user holds on a
// dashboard through ownership or grants, and 0 when they hold none. An owner
// ranks as admin.
func dashboardGrantRank(dashboard *Dashboard, userID uuid.UUID, userGroupIDs []uint) (int, error) {
	if userID == uuid.Nil {
		return 0, nil
	}
	if dashboard.OwnerID == userID {
		return dashboardAccessRank[DashboardAccessAdmin], nil
	}
	held := 0
	entries, err := grantsFor(dashboard.ID, userID, userGroupIDs)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		held = max(held, dashboardAccessRank[e])
	}
	if dashboard.GroupID == nil {
		return held, nil
	}
	inherited, err := dashboardGroupGrantRank(*dashboard.GroupID, userID, userGroupIDs)
	if err != nil {
		return 0, err
	}
	return max(held, inherited), nil
}

// dashboardGroupGrantRank returns the highest access rank a user holds on a
// dashboard group through ownership or grants, and 0 when they hold none. A
// group that no longer exists grants nothing rather than failing the caller.
func dashboardGroupGrantRank(groupID uint, userID uuid.UUID, userGroupIDs []uint) (int, error) {
	if userID == uuid.Nil {
		return 0, nil
	}
	var group DashboardGroup
	if err := db.DB.First(&group, groupID).Error; err == nil && group.OwnerID == userID {
		return dashboardAccessRank[DashboardAccessAdmin], nil
	}
	var grants []DashboardGroupAccess
	q := db.DB.Where("dashboard_group_id = ? AND user_id = ?", groupID, userID)
	if len(userGroupIDs) > 0 {
		q = q.Or("dashboard_group_id = ? AND group_id IN ?", groupID, userGroupIDs)
	}
	if err := q.Find(&grants).Error; err != nil {
		return 0, err
	}
	held := 0
	for _, g := range grants {
		held = max(held, dashboardAccessRank[g.Permission])
	}
	return held, nil
}

// grantsFor returns the permission levels a user holds on one dashboard,
// directly or through any of their user groups.
func grantsFor(dashboardID, userID uuid.UUID, userGroupIDs []uint) ([]string, error) {
	var entries []DashboardAccess
	q := db.DB.Where("dashboard_id = ? AND user_id = ?", dashboardID, userID)
	if len(userGroupIDs) > 0 {
		q = q.Or("dashboard_id = ? AND group_id IN ?", dashboardID, userGroupIDs)
	}
	if err := q.Find(&entries).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Permission)
	}
	return out, nil
}

// ListAccessibleDashboards returns dashboards visible to a user via ownership, public flag, direct/group sharing, or dashboard group ownership or sharing.
func ListAccessibleDashboards(userID uuid.UUID, userGroupIDs []uint, page, pageSize int) ([]*Dashboard, int64, error) {
	var dashboards []*Dashboard
	var count int64

	directAccess := db.DB.Model(&DashboardAccess{}).Select("dashboard_id").Where("user_id = ?", userID)
	groupAccess := db.DB.Model(&DashboardAccess{}).Select("dashboard_id").Where("group_id IN ?", userGroupIDs)
	dgAccessUser := db.DB.Model(&DashboardGroupAccess{}).Select("dashboard_group_id").Where("user_id = ?", userID)
	dgAccessGroup := db.DB.Model(&DashboardGroupAccess{}).Select("dashboard_group_id").Where("group_id IN ?", userGroupIDs)
	// A dashboard group's owner holds admin on what sits in it, as
	// dashboardGroupGrantRank decides, so the list has to hold it too.
	dgOwned := db.DB.Model(&DashboardGroup{}).Select("id").Where("owner_id = ?", userID)

	query := db.DB.Model(&Dashboard{}).Where(
		"owner_id = ? OR is_public = ? OR id IN (?) OR id IN (?) OR group_id IN (?) OR group_id IN (?) OR group_id IN (?)",
		userID, true,
		directAccess, groupAccess,
		dgAccessUser, dgAccessGroup, dgOwned,
	)

	query.Count(&count)

	offset := (page - 1) * pageSize
	err := query.Preload("Owner").Preload("Group").Preload("Group.Owner").
		Offset(offset).Limit(pageSize).Order("sort_order ASC, name ASC").Find(&dashboards).Error
	return dashboards, count, err
}

// FindDashboardWidget returns a dashboard widget with its Widget preloaded.
func FindDashboardWidget(id uuid.UUID) (*DashboardWidget, error) {
	var dw DashboardWidget
	err := db.DB.Preload("Widget").Where("id = ?", id).First(&dw).Error
	return &dw, err
}

// ListDashboardWidgets returns all widgets for a dashboard with Widget preloaded.
func ListDashboardWidgets(dashboardID uuid.UUID) ([]DashboardWidget, error) {
	var widgets []DashboardWidget
	err := db.DB.Preload("Widget").Where("dashboard_id = ?", dashboardID).Find(&widgets).Error
	return widgets, err
}

// ListDashboardAccess returns all access entries for a dashboard with User and Group preloaded.
func ListDashboardAccess(dashboardID uuid.UUID) ([]DashboardAccess, error) {
	var entries []DashboardAccess
	err := db.DB.Preload("User").Preload("Group").Where("dashboard_id = ?", dashboardID).Find(&entries).Error
	return entries, err
}

// DashboardWidget places a widget instance at a specific position and size within a dashboard grid.
type DashboardWidget struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	DashboardID uuid.UUID `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Parent dashboard ID."`
	Dashboard   Dashboard `gorm:"foreignKey:DashboardID" json:"-"`
	WidgetID    uint      `gorm:"not null" json:"widget_id" description:"Widget type ID."`
	Widget      Widget    `gorm:"foreignKey:WidgetID" json:"widget,omitempty" description:"Widget definition."`
	// SectionID names the section the placement sits in; nil places it in the
	// sectionless region above the first section, which is where every
	// placement of a fit-layout dashboard lives.
	SectionID *uuid.UUID `gorm:"type:uuid;index" json:"section_id,omitempty" description:"Owning section ID; absent when placed outside every section."`
	X         int        `gorm:"not null" json:"x" description:"Grid column position."`
	Y         int        `gorm:"not null" json:"y" description:"Grid row position, relative to the top of its section."`
	Width     int        `gorm:"not null" json:"width" description:"Width in grid units."`
	Height    int        `gorm:"not null" json:"height" description:"Height in grid units."`
	Config    string     `gorm:"type:json" json:"config" description:"Widget instance configuration JSON."`
	// PageID names the page the placement is shown on; nil shows it on
	// every page.
	PageID *uuid.UUID `gorm:"type:uuid;index" json:"page_id,omitempty" description:"Page the placement is shown on; absent for every page."`
	// Region is the named screen region of the region layout. Other layouts
	// ignore it and keep using X and Y, so switching layouts loses nothing.
	Region  string         `json:"region,omitempty" description:"Region name in the region layout."`
	Display *WidgetDisplay `gorm:"serializer:json;type:json" json:"display,omitempty" description:"Name, visibility conditions, animations, tile style, and status cues."`
	// ConfigRevision rises each time the config is replaced, so a writer that
	// read it can refuse to overwrite a change made since.
	ConfigRevision int       `gorm:"not null;default:0" json:"config_revision" description:"Rises with each config replacement; pass it back as expected_config_revision."`
	CreatedAt      time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt      time.Time `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID if one has not been set.
func (dw *DashboardWidget) BeforeCreate(tx *gorm.DB) error {
	if dw.ID == uuid.Nil {
		dw.ID = uuid.New()
	}
	return nil
}

// Delete removes a dashboard widget and its uploads in a transaction.
func (m *DashboardWidget) Delete() error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		tx.Where("dashboard_widget_id = ?", m.ID).Delete(&WidgetUpload{})
		return tx.Delete(&DashboardWidget{}, "id = ?", m.ID).Error
	})
}

// DashboardAccess grants a user or group a specific permission level on a dashboard.
type DashboardAccess struct {
	ID          uint       `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	DashboardID uuid.UUID  `gorm:"type:uuid;not null;index" json:"dashboard_id" description:"Dashboard ID."`
	Dashboard   Dashboard  `gorm:"foreignKey:DashboardID" json:"-"`
	UserID      *uuid.UUID `gorm:"type:uuid" json:"user_id,omitempty" description:"Granted user ID."`
	User        *User      `gorm:"foreignKey:UserID" json:"user,omitempty" description:"Granted user."`
	GroupID     *uint      `json:"group_id,omitempty" description:"Granted user group ID."`
	Group       *UserGroup `gorm:"foreignKey:GroupID" json:"group,omitempty" description:"Granted user group."`
	Permission  string     `gorm:"not null" json:"permission" description:"Permission level: read (look only), view (look and operate), edit, or admin."`
	CreatedAt   time.Time  `json:"created_at" description:"Creation timestamp."`
}
