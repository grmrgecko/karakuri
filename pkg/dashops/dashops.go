// Package dashops holds the dashboard operations shared by the REST handlers
// and the MCP tools: resolution, the edit check, layout views, create and
// update with section flattening, widget placement, deploy, and the portable
// export/import document. Callers decide who may act; the one check here,
// CanEdit, answers ownership plus the blanket permission for a user ID so both
// layers ask the same question. Errors are AppErrors so both report the same
// codes.
package dashops

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashlive"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"gorm.io/gorm"
)

// slugFormatRe is the shape a dashboard slug must have: lowercase words
// joined by single hyphens.
var slugFormatRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Resolve looks up a dashboard by UUID, or by a slug exactly one dashboard
// uses. It is for paths acting for no particular user; a request should use
// ResolveFor, so a slug resolves among what its caller can see.
func Resolve(idOrSlug string) (*models.Dashboard, error) {
	return resolve(idOrSlug, nil)
}

// ResolveFor looks up a dashboard by UUID, or by a slug among the dashboards
// the user may view; an anonymous caller sees public dashboards. A slug
// several of them share is ambiguous, and the error lists them by ID so the
// caller can choose. A dashboard the user cannot see never takes part, so a
// slug confirms nothing private.
func ResolveFor(ctx context.Context, userID uuid.UUID, idOrSlug string) (*models.Dashboard, error) {
	return resolve(idOrSlug, func(d *models.Dashboard) bool { return CanView(ctx, userID, d) })
}

// ResolveVisible looks up a dashboard by UUID, or by a slug among the
// dashboards visible accepts, for callers whose idea of visible is more than
// one user's view, such as a request carrying a render token.
func ResolveVisible(idOrSlug string, visible func(*models.Dashboard) bool) (*models.Dashboard, error) {
	return resolve(idOrSlug, visible)
}

func resolve(idOrSlug string, visible func(*models.Dashboard) bool) (*models.Dashboard, error) {
	found, err := models.LookupDashboardVisible(idOrSlug, visible)
	if errors.Is(err, models.ErrSlugAmbiguous) {
		return nil, ambiguousDashboards(idOrSlug, visible)
	}
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	dashboard, err := models.FindDashboardByID(found.ID)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("dashboard not found")
	}
	return dashboard, nil
}

// SlugMatch is one dashboard or flow a slug could mean, as a chooser lists it.
type SlugMatch struct {
	ID    string `json:"id" description:"The item's ID, its canonical address."`
	Name  string `json:"name" description:"Display name."`
	Owner string `json:"owner,omitempty" description:"The owner's username."`
}

// SlugMatches returns the dashboards using a slug that the user may view.
func SlugMatches(ctx context.Context, userID uuid.UUID, slug string) ([]SlugMatch, error) {
	dashboards, err := models.DashboardsWithSlug(slug)
	if err != nil {
		return nil, err
	}
	out := []SlugMatch{}
	for _, d := range dashboards {
		if CanView(ctx, userID, d) {
			out = append(out, SlugMatch{ID: d.ID.String(), Name: d.Name, Owner: d.Owner.Username})
		}
	}
	return out, nil
}

// ambiguousDashboards builds the error for a slug several visible dashboards
// share, listing each as ID to name.
func ambiguousDashboards(slug string, visible func(*models.Dashboard) bool) error {
	dashboards, _ := models.DashboardsWithSlug(slug)
	details := map[string]string{}
	for _, d := range dashboards {
		if visible == nil || visible(d) {
			details[d.ID.String()] = d.Name
		}
	}
	return apperrors.ErrAmbiguous.WithMessage(fmt.Sprintf("%d dashboards use the address %q; open one by its ID", len(details), slug)).WithDetails(details)
}

// SlugVisible reports whether the user can already see a dashboard using a
// slug, which is the clash worth refusing when they pick one.
func SlugVisible(ctx context.Context, userID uuid.UUID, slug string) bool {
	matches, err := SlugMatches(ctx, userID, slug)
	return err == nil && len(matches) > 0
}

// CanView reports whether a user may be served a dashboard's layout: anyone
// when it is public, otherwise its owner, someone it is shared with directly
// or through a user group or its dashboard group, or a holder of the manage
// permission.
func CanView(ctx context.Context, userID uuid.UUID, d *models.Dashboard) bool {
	return d.IsPublic || holdsDashboard(ctx, userID, d, models.DashboardAccessRead)
}

// CanEdit reports whether a user may read and change a dashboard's draft and
// publish it: the owner, a share at edit level or above, or the manage
// permission. Holding api:dashboards:write says a user may author dashboards,
// not that they may write into someone else's.
func CanEdit(ctx context.Context, userID uuid.UUID, d *models.Dashboard) bool {
	return holdsDashboard(ctx, userID, d, models.DashboardAccessEdit)
}

// CanManage reports whether a user may share or delete a dashboard: the
// owner, a share at admin level, or the manage permission.
func CanManage(ctx context.Context, userID uuid.UUID, d *models.Dashboard) bool {
	return holdsDashboard(ctx, userID, d, models.DashboardAccessAdmin)
}

// CanAccess reports whether a user holds an access level on a dashboard, for
// routes that name the level they need. The read level is CanView, which a
// public dashboard grants to anyone.
func CanAccess(ctx context.Context, userID uuid.UUID, d *models.Dashboard, level string) bool {
	if level == models.DashboardAccessRead {
		return CanView(ctx, userID, d)
	}
	return holdsDashboard(ctx, userID, d, level)
}

// holdsDashboard answers one access level for one dashboard. An anonymous
// caller holds nothing; the manage permission holds everything.
func holdsDashboard(ctx context.Context, userID uuid.UUID, d *models.Dashboard, level string) bool {
	if userID == uuid.Nil {
		return false
	}
	if userID == d.OwnerID || auth.Can(ctx, userID, perm.PermDashboardsManage) {
		return true
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ok, err := models.CanAccessDashboard(d.ID, userID, groupIDs, level)
	return err == nil && ok
}

// CanEditGroup reports whether a user may change a dashboard group: the
// owner, a share on it at edit level or above, or the manage permission.
func CanEditGroup(ctx context.Context, userID uuid.UUID, g *models.DashboardGroup) bool {
	return holdsDashboardGroup(ctx, userID, g, models.DashboardAccessEdit)
}

// CanManageGroup reports whether a user may share or delete a dashboard
// group: the owner, a share at admin level, or the manage permission.
func CanManageGroup(ctx context.Context, userID uuid.UUID, g *models.DashboardGroup) bool {
	return holdsDashboardGroup(ctx, userID, g, models.DashboardAccessAdmin)
}

// CheckGroupMove reports whether a user may move a dashboard into target, a
// dashboard group ID where nil or 0 means ungrouped. A dashboard inherits
// the shares of its group, so a move changes who may reach it: it needs admin
// access on the dashboard, and edit access on the target group. Leaving the
// group unchanged needs nothing, since reorder sends every item's group.
func CheckGroupMove(ctx context.Context, userID uuid.UUID, d *models.Dashboard, target *uint) error {
	current := uint(0)
	if d.GroupID != nil {
		current = *d.GroupID
	}
	next := uint(0)
	if target != nil {
		next = *target
	}
	if next == current {
		return nil
	}
	if !CanManage(ctx, userID, d) {
		return apperrors.ErrForbidden.WithMessage("moving a dashboard between groups needs admin access to it")
	}
	return CheckGroupTarget(ctx, userID, target)
}

// CheckGroupTarget reports whether a user may place a dashboard in target, a
// dashboard group ID where nil or 0 means ungrouped: the group must exist
// and the user must be able to edit it.
func CheckGroupTarget(ctx context.Context, userID uuid.UUID, target *uint) error {
	if target == nil || *target == 0 {
		return nil
	}
	group, err := models.FindDashboardGroupByID(*target)
	if err != nil {
		return apperrors.ErrValidation.WithMessage("dashboard group not found")
	}
	if !CanEditGroup(ctx, userID, group) {
		return apperrors.ErrForbidden.WithMessage("adding a dashboard to this group needs edit access to the group")
	}
	return nil
}

// holdsDashboardGroup answers one access level for one dashboard group.
func holdsDashboardGroup(ctx context.Context, userID uuid.UUID, g *models.DashboardGroup, level string) bool {
	if userID == uuid.Nil {
		return false
	}
	if userID == g.OwnerID || auth.Can(ctx, userID, perm.PermDashboardGroupsManage) {
		return true
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	ok, err := models.CanAccessDashboardGroup(g.ID, userID, groupIDs, level)
	return err == nil && ok
}

// ApplyDeployedLayout swaps a dashboard's geometry, sections, and widgets for
// its deployed version. A dashboard that has never been deployed keeps its
// draft: a layout nobody has published yet is better shown than an empty grid.
func ApplyDeployedLayout(d *models.Dashboard) error {
	if d.DeployedVersion == 0 {
		return nil
	}
	// A deployed version that cannot be read is an error, never a fall back
	// to the draft: serving the draft would publish unpublished work.
	version, err := models.FindDashboardVersion(d.ID, d.DeployedVersion)
	if err != nil {
		return fmt.Errorf("reading deployed version %d: %w", d.DeployedVersion, err)
	}
	snapshot, err := models.ParseDashboardSnapshot(version.Snapshot)
	if err != nil {
		return err
	}
	widgets, err := snapshot.WidgetRows(d.ID)
	if err != nil {
		return err
	}
	snapshot.ApplyPresentation(d)
	d.LayoutMode = snapshot.LayoutMode
	d.RowHeight = snapshot.RowHeight
	d.GridRows = snapshot.GridRows
	d.GridCols = snapshot.GridCols
	d.Padding = snapshot.Padding
	d.Gap = snapshot.Gap
	d.WidgetPadding = snapshot.WidgetPadding
	d.Display = snapshot.Display
	d.Sections = snapshot.SectionRows(d.ID)
	d.Pages = snapshot.PageRows(d.ID)
	d.Widgets = widgets
	return nil
}

// AnnotateDraftState fills in HasDraftChanges by comparing the draft layout
// with what was deployed. It is computed rather than stored so a widget writing
// its own config counts the same as the author moving a card.
func AnnotateDraftState(d *models.Dashboard, widgets []models.DashboardWidget, sections []models.DashboardSection, pages []models.DashboardPage) {
	if d.DeployedVersion == 0 {
		d.HasDraftChanges = false
		return
	}
	version, err := models.FindDashboardVersion(d.ID, d.DeployedVersion)
	if err != nil {
		return
	}
	current, err := models.SnapshotDashboard(d, widgets, sections, pages).JSON()
	if err != nil {
		return
	}
	deployed, err := models.ParseDashboardSnapshot(version.Snapshot)
	if err != nil {
		return
	}
	// Older stored snapshots used live presentation settings.
	if deployed.Presentation == nil {
		deployed.Presentation = &models.DashboardPresentation{Name: d.Name, Description: d.Description, ThemeID: d.ThemeID}
	}
	// Re-render the stored snapshot so an older encoding compares by value.
	normalized, err := deployed.JSON()
	if err != nil {
		return
	}
	d.HasDraftChanges = current != normalized
}

// CreateSpec holds the fields a new dashboard is created with.
type CreateSpec struct {
	Name          string
	Slug          string
	Description   string
	IsPublic      bool
	PublicControl bool
	GroupID       *uint
	ThemeID       *uint
	LayoutMode    string
	GridRows      int
	GridCols      int
	RowHeight     int
	Padding       int
	Gap           int
	WidgetPadding int
	Display       *models.DashboardDisplay
}

// setOrNone reads an optional reference where nil and 0 both mean none, which
// the column holds as NULL: 0 names no row, and storing it fails the foreign
// key.
func setOrNone(id *uint) *uint {
	if id == nil || *id == 0 {
		return nil
	}
	return id
}

// checkTheme refuses a theme reference that names no theme.
func checkTheme(id *uint) error {
	if id == nil {
		return nil
	}
	var count int64
	if err := db.DB.Model(&models.Theme{}).Where("id = ?", *id).Count(&count).Error; err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	if count == 0 {
		return apperrors.ErrValidation.WithMessage("theme not found")
	}
	return nil
}

// Create validates a spec and stores a new dashboard owned by the given user.
func Create(ctx context.Context, spec CreateSpec, ownerID uuid.UUID) (*models.Dashboard, error) {
	if spec.Name == "" {
		return nil, apperrors.ErrValidation.WithMessage("name must not be empty")
	}
	spec.GroupID, spec.ThemeID = setOrNone(spec.GroupID), setOrNone(spec.ThemeID)
	if err := checkTheme(spec.ThemeID); err != nil {
		return nil, err
	}

	// Resolve slug.
	slug := spec.Slug
	if slug == "" {
		slug = models.Slugify(spec.Name)
	}
	if slug == "" || len(slug) > 200 || !slugFormatRe.MatchString(slug) {
		return nil, apperrors.ErrValidation.WithMessage("invalid slug format")
	}

	// Refuse a slug the owner can already see in use. One used only by
	// dashboards they cannot see is fine: slugs need not be unique.
	if SlugVisible(ctx, ownerID, slug) {
		return nil, apperrors.ErrConflict.WithMessage("a dashboard with this slug already exists")
	}

	// Resolve layout defaults so a client that sends neither still gets the
	// wall-panel layout it expects.
	layout := spec.LayoutMode
	if !models.ValidDashboardLayout(layout) {
		layout = models.DashboardLayoutFit
	}
	rowHeight := spec.RowHeight
	if rowHeight <= 0 {
		rowHeight = 80
	}

	// Build and persist the dashboard record.
	dashboard := &models.Dashboard{
		ID:            uuid.New(),
		Name:          spec.Name,
		Slug:          slug,
		Description:   spec.Description,
		OwnerID:       ownerID,
		IsPublic:      spec.IsPublic,
		PublicControl: spec.PublicControl,
		GroupID:       spec.GroupID,
		ThemeID:       spec.ThemeID,
		LayoutMode:    layout,
		GridRows:      spec.GridRows,
		GridCols:      spec.GridCols,
		RowHeight:     rowHeight,
		Padding:       spec.Padding,
		Gap:           spec.Gap,
		WidgetPadding: spec.WidgetPadding,
	}
	if !spec.Display.IsZero() {
		if err := ValidateDisplay(spec.Display); err != nil {
			return nil, err
		}
		if err := CheckVariableQueries(ctx, ownerID, nil, spec.Display); err != nil {
			return nil, err
		}
		dashboard.Display = spec.Display
	}
	if err := db.DB.Create(dashboard).Error; err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	return dashboard, nil
}

// Patch holds the optional fields Update applies to a dashboard. GroupID and
// ThemeID set to 0 clear the reference.
type Patch struct {
	// By is the user making the change. A new slug is refused only when a
	// dashboard they can see already uses it.
	By            uuid.UUID
	Name          *string
	Slug          *string
	Description   *string
	IsPublic      *bool
	PublicControl *bool
	GroupID       *uint
	ThemeID       *uint
	SortOrder     *int
	LayoutMode    *string
	GridRows      *int
	GridCols      *int
	RowHeight     *int
	Padding       *int
	Gap           *int
	WidgetPadding *int
	// Display replaces the display settings; an empty object clears them.
	Display *models.DashboardDisplay
	// ExpectedDisplayRevision, with Display, refuses the replacement when the
	// display settings were replaced since the caller read this revision.
	ExpectedDisplayRevision *int
	// UserID is who saves the change; zero is the server itself.
	UserID uuid.UUID
}

// Update applies a patch to a dashboard and persists it. Switching the layout
// to fit merges every section's widgets into the sectionless region.
func Update(ctx context.Context, dashboard *models.Dashboard, p Patch) error {
	// Apply requested field updates.
	if p.Name != nil {
		if *p.Name == "" {
			return apperrors.ErrValidation.WithMessage("name must not be empty")
		}
		dashboard.Name = *p.Name
	}
	if p.Slug != nil && *p.Slug != dashboard.Slug {
		slug := *p.Slug
		if slug == "" || len(slug) > 200 || !slugFormatRe.MatchString(slug) {
			return apperrors.ErrValidation.WithMessage("invalid slug format")
		}
		if SlugVisible(ctx, p.By, slug) {
			return apperrors.ErrConflict.WithMessage("a dashboard with this slug already exists")
		}
		dashboard.Slug = slug
	}
	if p.Description != nil {
		dashboard.Description = *p.Description
	}
	if p.IsPublic != nil {
		dashboard.IsPublic = *p.IsPublic
	}
	if p.PublicControl != nil {
		dashboard.PublicControl = *p.PublicControl
	}
	if p.GroupID != nil {
		if *p.GroupID == 0 {
			dashboard.GroupID = nil
		} else {
			dashboard.GroupID = p.GroupID
		}
		// Clear the preloaded association so GORM doesn't override GroupID during Save.
		dashboard.Group = nil
	}
	if p.ThemeID != nil {
		if *p.ThemeID == 0 {
			dashboard.ThemeID = nil
		} else {
			if err := checkTheme(p.ThemeID); err != nil {
				return err
			}
			dashboard.ThemeID = p.ThemeID
		}
		// Clear the preloaded association so GORM doesn't override ThemeID during Save.
		dashboard.Theme = nil
	}
	if p.SortOrder != nil {
		dashboard.SortOrder = *p.SortOrder
	}
	if p.LayoutMode != nil && !models.ValidDashboardLayout(*p.LayoutMode) {
		return apperrors.ErrValidation.WithMessage("layout_mode must be fit, scroll, or regions")
	}
	if p.GridRows != nil {
		dashboard.GridRows = *p.GridRows
	}
	if p.GridCols != nil {
		dashboard.GridCols = *p.GridCols
	}
	if p.RowHeight != nil {
		if *p.RowHeight <= 0 {
			return apperrors.ErrValidation.WithMessage("row_height must be greater than zero")
		}
		dashboard.RowHeight = *p.RowHeight
	}
	if p.Padding != nil {
		dashboard.Padding = *p.Padding
	}
	if p.Gap != nil {
		dashboard.Gap = *p.Gap
	}
	if p.WidgetPadding != nil {
		dashboard.WidgetPadding = *p.WidgetPadding
	}
	if p.Display != nil {
		if err := ValidateDisplay(p.Display); err != nil {
			return err
		}
		if err := CheckVariableQueries(ctx, p.UserID, dashboard.Display, p.Display); err != nil {
			return err
		}
		dashboard.Display = p.Display
		if p.Display.IsZero() {
			dashboard.Display = nil
		}
	}

	// Leaving the scrolling layout has to take the sections with it: fit
	// has no room below the fold, so their widgets are merged into the
	// sectionless region rather than stacked on top of each other. It runs
	// after the grid fields so it can grow the row count to hold them.
	if p.LayoutMode != nil {
		if *p.LayoutMode == models.DashboardLayoutFit && dashboard.LayoutMode != models.DashboardLayoutFit {
			if err := models.FlattenDashboardSections(dashboard); err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
			// Cleared so the save that follows does not write the deleted
			// sections back through the preloaded association.
			dashboard.Sections = nil
		}
		dashboard.LayoutMode = *p.LayoutMode
	}

	// Persist changes.
	fields := []string{"updated_at"}
	if p.Name != nil {
		fields = append(fields, "name")
	}
	if p.Slug != nil {
		fields = append(fields, "slug")
	}
	if p.Description != nil {
		fields = append(fields, "description")
	}
	if p.IsPublic != nil {
		fields = append(fields, "is_public")
	}
	if p.PublicControl != nil {
		fields = append(fields, "public_control")
	}
	if p.GroupID != nil {
		fields = append(fields, "group_id")
	}
	if p.ThemeID != nil {
		fields = append(fields, "theme_id")
	}
	if p.SortOrder != nil {
		fields = append(fields, "sort_order")
	}
	if p.LayoutMode != nil {
		fields = append(fields, "layout_mode")
	}
	if p.GridRows != nil {
		fields = append(fields, "grid_rows")
	}
	if p.GridCols != nil {
		fields = append(fields, "grid_cols")
	}
	if p.RowHeight != nil {
		fields = append(fields, "row_height")
	}
	if p.Padding != nil {
		fields = append(fields, "padding")
	}
	if p.Gap != nil {
		fields = append(fields, "gap")
	}
	if p.WidgetPadding != nil {
		fields = append(fields, "widget_padding")
	}
	if p.Display != nil {
		fields = append(fields, "display")
	}
	if p.LayoutMode != nil {
		fields = append(fields, "grid_rows")
	}
	// A display replacement raises its revision in the same transaction, and
	// one naming the revision it read applies only if nobody replaced the
	// display since. The variables and screen settings are one value, so a
	// writer adding a variable must not erase another's dim schedule.
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if p.Display != nil {
			bump := tx.Model(&models.Dashboard{}).Where("id = ?", dashboard.ID)
			if p.ExpectedDisplayRevision != nil {
				bump = bump.Where("display_revision = ?", *p.ExpectedDisplayRevision)
			}
			res := bump.UpdateColumn("display_revision", gorm.Expr("display_revision + 1"))
			if res.Error != nil {
				return apperrors.ErrInternal.WithError(res.Error)
			}
			if res.RowsAffected == 0 && p.ExpectedDisplayRevision != nil {
				return apperrors.ErrConflict.WithMessage(fmt.Sprintf(
					"the dashboard's display settings changed since revision %d was read; read them again and reapply the change", *p.ExpectedDisplayRevision))
			}
			dashboard.DisplayRevision++
			if p.ExpectedDisplayRevision != nil {
				dashboard.DisplayRevision = *p.ExpectedDisplayRevision + 1
			}
		}
		if err := tx.Model(dashboard).Select(fields).Updates(dashboard).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	NotifyChanged(dashboard.ID)

	// The response says whether the draft now differs from the deployment;
	// a display or geometry change does, a rename alone does not. The rows
	// are read again because flattening sections rewrote them.
	widgets, werr := models.ListDashboardWidgets(dashboard.ID)
	sections, serr := models.ListDashboardSections(dashboard.ID)
	pages, perr := models.ListDashboardPages(dashboard.ID)
	if werr == nil && serr == nil && perr == nil {
		AnnotateDraftState(dashboard, widgets, sections, pages)
	}
	return nil
}

// Delete removes a dashboard and invalidates its cached viewer scope.
func Delete(dashboard *models.Dashboard) error {
	if err := dashboard.Delete(); err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	dashscope.Invalidate(dashboard.ID)
	dashlive.S.Forget(dashboard.ID)
	return nil
}

// Deploy freezes the draft layout as a numbered version and points viewers at
// it, returning the new version number. The dashboard is updated in place with
// the deployed state so callers can render the now-clean draft.
func Deploy(ctx context.Context, dashboard *models.Dashboard, comment string, userID uuid.UUID) (int, error) {
	// A widget trigger binding lets every viewer press that flow, so the
	// deployer must be someone who may run it.
	if err := checkTriggerFlows(ctx, dashboard, userID); err != nil {
		return 0, err
	}
	// Viewers read and operate what the layout binds, so the deployer must
	// be able to use every connection it reaches.
	if err := checkDraftConnections(ctx, dashboard, userID); err != nil {
		return 0, err
	}

	// Snapshot the draft.
	widgets, err := models.ListDashboardWidgets(dashboard.ID)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	sections, err := models.ListDashboardSections(dashboard.ID)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	pages, err := models.ListDashboardPages(dashboard.ID)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	snapshot, err := models.SnapshotDashboard(dashboard, widgets, sections, pages).JSON()
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}

	// Freeze it under the next version number and publish it.
	next, err := models.NextDashboardVersion(dashboard.ID)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		version := &models.DashboardVersion{
			DashboardID: dashboard.ID,
			Version:     next,
			Snapshot:    snapshot,
			Comment:     comment,
			WidgetCount: len(widgets),
			CreatedByID: &userID,
		}
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		return tx.Model(&models.Dashboard{}).Where("id = ?", dashboard.ID).
			Update("deployed_version", next).Error
	})
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(fmt.Errorf("failed to deploy dashboard: %w", err))
	}

	dashboard.DeployedVersion = next
	dashboard.Widgets = widgets
	dashboard.Sections = sections
	dashboard.Pages = pages
	dashboard.HasDraftChanges = false
	NotifyChanged(dashboard.ID)
	return next, nil
}

// NotifyChanged tells open editors to fetch the committed dashboard draft.
func NotifyChanged(id uuid.UUID) { mq.B.Publish("editor:dashboard:"+id.String(), "changed", nil) }

// RestoreVersion replaces the whole draft with a stored version without deploying it.
// Version zero selects the currently deployed version.
func RestoreVersion(d *models.Dashboard, number int) error {
	if number == 0 {
		number = d.DeployedVersion
	}
	if number < 1 {
		return apperrors.ErrValidation.WithMessage("dashboard has no deployed version to restore")
	}
	version, err := models.FindDashboardVersion(d.ID, number)
	if err != nil {
		return apperrors.ErrNotFound.WithMessage("version not found")
	}
	snapshot, err := models.ParseDashboardSnapshot(version.Snapshot)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	if err := models.RestoreDashboardSnapshot(d, snapshot); err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(d.ID)
	return nil
}

// checkTriggerFlows refuses a deploy whose draft binds a widget trigger on a
// flow the deployer may not run: its owner, a share at edit or above, or the
// flow manage permission. Otherwise a dashboard author could publish a button
// for a flow they were never shared, and its viewers would press it on their
// behalf.
func checkTriggerFlows(ctx context.Context, dashboard *models.Dashboard, userID uuid.UUID) error {
	flowIDs, err := dashscope.DraftTriggerFlows(dashboard)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	if len(flowIDs) == 0 || auth.Can(ctx, userID, perm.PermFlowsManage) {
		return nil
	}
	groupIDs, _ := models.GetUserGroupIDs(userID)
	for _, raw := range flowIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("a widget trigger names an invalid flow ID %q", raw))
		}
		f, err := models.FindFlowByID(id)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("a widget trigger names flow %s, which does not exist", raw))
		}
		rank, err := models.FlowGrantRank(f, userID, groupIDs)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		if rank < models.FlowAccessRank(models.FlowAccessEdit) {
			if rank == 0 {
				return apperrors.ErrValidation.WithMessage(fmt.Sprintf("a widget trigger names flow %s, which does not exist", raw))
			}
			return apperrors.ErrForbidden.WithMessage(fmt.Sprintf("a widget trigger presses %q, and deploying it needs edit access to that flow", f.Name))
		}
	}
	return nil
}
