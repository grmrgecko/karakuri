package dashops

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// ResolveWidgetSection turns a section ID from a request into a placement's
// section reference, rejecting one that belongs to another dashboard. An empty
// string means the sectionless region.
func ResolveWidgetSection(dashboardID uuid.UUID, raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid section ID")
	}
	section, err := models.FindDashboardSection(id)
	if err != nil || section.DashboardID != dashboardID {
		return nil, apperrors.ErrValidation.WithMessage("section not found on this dashboard")
	}
	return &id, nil
}

// WidgetSpec is one placement being added to a dashboard.
type WidgetSpec struct {
	WidgetID  uint
	SectionID string
	X, Y      int
	Width     int
	Height    int
	Config    string
	// PageID names the page; empty shows the placement on every page.
	PageID  string
	Region  string
	Display *models.WidgetDisplay
	// UserID is who saves the placement, for permissions a widget's config
	// needs; zero is the server itself.
	UserID uuid.UUID
}

// AddWidget places a widget on a dashboard at the given grid position.
func AddWidget(ctx context.Context, dashboard *models.Dashboard, spec WidgetSpec) (*models.DashboardWidget, error) {
	// Reject early if the referenced widget does not exist.
	var widget models.Widget
	if err := db.DB.First(&widget, spec.WidgetID).Error; err != nil {
		return nil, apperrors.ErrValidation.WithMessage("widget not found")
	}

	// Resolve the target section, if the placement names one.
	sectionID, err := ResolveWidgetSection(dashboard.ID, spec.SectionID)
	if err != nil {
		return nil, err
	}
	pageID, err := ResolveWidgetPage(dashboard.ID, spec.PageID)
	if err != nil {
		return nil, err
	}
	if err := validateRegion(spec.Region); err != nil {
		return nil, err
	}
	if err := ValidateWidgetDisplay(spec.Display, false); err != nil {
		return nil, err
	}
	if err := checkChartQueries(ctx, spec.UserID, widget.PackageName, "", spec.Config); err != nil {
		return nil, err
	}

	// Build and persist the placement record.
	dw := &models.DashboardWidget{
		ID:          uuid.New(),
		DashboardID: dashboard.ID,
		WidgetID:    spec.WidgetID,
		Widget:      widget,
		SectionID:   sectionID,
		X:           spec.X,
		Y:           spec.Y,
		Width:       spec.Width,
		Height:      spec.Height,
		Config:      spec.Config,
		PageID:      pageID,
		Region:      spec.Region,
		Display:     normalizeWidgetDisplay(spec.Display),
	}
	if err := db.DB.Create(dw).Error; err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(dashboard.ID)
	return dw, nil
}

// WidgetPatch holds the optional fields UpdateWidget applies to a placement.
// SectionID moves the placement between regions; an empty string moves it out
// of every section, and nil leaves the region alone.
type WidgetPatch struct {
	SectionID *string
	X         *int
	Y         *int
	Width     *int
	Height    *int
	Config    *string
	// PageID moves the placement to a page; an empty string shows it on
	// every page, and nil leaves the page alone.
	PageID *string
	Region *string
	// Display replaces the display settings; an empty object clears them.
	Display *models.WidgetDisplay
	// ExpectedConfigRevision, with Config, refuses the replacement when the
	// config has been replaced since the caller read this revision.
	ExpectedConfigRevision *int
	// UserID is who saves the change; zero is the server itself.
	UserID uuid.UUID
}

// UpdateWidget patches a widget placement on a dashboard.
func UpdateWidget(ctx context.Context, dashboard *models.Dashboard, widgetID string, p WidgetPatch) (*models.DashboardWidget, error) {
	// Load the placement.
	id, err := uuid.Parse(widgetID)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid widget ID")
	}
	dw, err := models.FindDashboardWidget(id)
	if err != nil || dw.DashboardID != dashboard.ID {
		return nil, apperrors.ErrNotFound.WithMessage("widget placement not found on this dashboard")
	}

	// Apply requested field updates.
	if p.SectionID != nil {
		sectionID, err := ResolveWidgetSection(dashboard.ID, *p.SectionID)
		if err != nil {
			return nil, err
		}
		dw.SectionID = sectionID
	}
	if p.X != nil {
		dw.X = *p.X
	}
	if p.Y != nil {
		dw.Y = *p.Y
	}
	if p.Width != nil {
		dw.Width = *p.Width
	}
	if p.Height != nil {
		dw.Height = *p.Height
	}
	if p.Config != nil {
		if err := checkChartQueries(ctx, p.UserID, dw.Widget.PackageName, dw.Config, *p.Config); err != nil {
			return nil, err
		}
		dw.Config = *p.Config
	}
	if p.PageID != nil {
		pageID, err := ResolveWidgetPage(dashboard.ID, *p.PageID)
		if err != nil {
			return nil, err
		}
		dw.PageID = pageID
	}
	if p.Region != nil {
		if err := validateRegion(*p.Region); err != nil {
			return nil, err
		}
		dw.Region = *p.Region
	}
	if p.Display != nil {
		if err := ValidateWidgetDisplay(p.Display, false); err != nil {
			return nil, err
		}
		dw.Display = normalizeWidgetDisplay(p.Display)
	}

	// Persist changes.
	fields := []string{"updated_at"}
	if p.SectionID != nil {
		fields = append(fields, "section_id")
	}
	if p.X != nil {
		fields = append(fields, "x")
	}
	if p.Y != nil {
		fields = append(fields, "y")
	}
	if p.Width != nil {
		fields = append(fields, "width")
	}
	if p.Height != nil {
		fields = append(fields, "height")
	}
	if p.Config != nil {
		fields = append(fields, "config")
	}
	if p.PageID != nil {
		fields = append(fields, "page_id")
	}
	if p.Region != nil {
		fields = append(fields, "region")
	}
	if p.Display != nil {
		fields = append(fields, "display")
	}
	// A config replacement raises the revision in the same transaction, and
	// one naming the revision it read applies only if nobody replaced the
	// config since, so two writers cannot silently overwrite each other.
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if p.Config != nil {
			bump := tx.Model(&models.DashboardWidget{}).Where("id = ?", dw.ID)
			if p.ExpectedConfigRevision != nil {
				bump = bump.Where("config_revision = ?", *p.ExpectedConfigRevision)
			}
			res := bump.UpdateColumn("config_revision", gorm.Expr("config_revision + 1"))
			if res.Error != nil {
				return apperrors.ErrInternal.WithError(res.Error)
			}
			if res.RowsAffected == 0 && p.ExpectedConfigRevision != nil {
				return apperrors.ErrConflict.WithMessage(fmt.Sprintf(
					"the widget's config changed since revision %d was read; read it again and reapply the change", *p.ExpectedConfigRevision))
			}
			dw.ConfigRevision++
			if p.ExpectedConfigRevision != nil {
				dw.ConfigRevision = *p.ExpectedConfigRevision + 1
			}
		}
		if err := tx.Model(dw).Select(fields).Updates(dw).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	NotifyChanged(dashboard.ID)
	return dw, nil
}

// RemoveWidget deletes a placement belonging to the specified dashboard.
func RemoveWidget(dashboard *models.Dashboard, widgetID string) error {
	id, err := uuid.Parse(widgetID)
	if err != nil {
		return apperrors.ErrValidation.WithMessage("invalid widget ID")
	}
	dw, err := models.FindDashboardWidget(id)
	if err != nil || dw.DashboardID != dashboard.ID {
		return apperrors.ErrNotFound.WithMessage("widget placement not found on this dashboard")
	}
	if err := dw.Delete(); err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(dashboard.ID)
	return nil
}
