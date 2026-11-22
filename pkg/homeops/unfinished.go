package homeops

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
)

// maxUnfinished bounds each list of unfinished work.
const maxUnfinished = 10

// UnfinishedItem is a flow or dashboard whose draft is not what is deployed.
type UnfinishedItem struct {
	ID        string    `json:"id" description:"Flow or dashboard ID."`
	Name      string    `json:"name" description:"Name."`
	UpdatedAt time.Time `json:"updated_at" description:"When it last changed."`
	Deployed  bool      `json:"deployed" description:"Whether any version was ever deployed."`
}

// UnfinishedResult is the caller's unfinished work. A list is absent when the
// caller may not author that kind.
type UnfinishedResult struct {
	Flows      []UnfinishedItem `json:"flows,omitempty" description:"Flows with undeployed draft changes, newest change first."`
	Dashboards []UnfinishedItem `json:"dashboards,omitempty" description:"Dashboards with undeployed draft changes, newest change first."`
}

// Unfinished lists the flows and dashboards the caller may edit whose draft
// differs from the deployed version or that were never deployed.
func Unfinished(ctx context.Context, userID uuid.UUID) (UnfinishedResult, error) {
	var res UnfinishedResult
	if auth.Can(ctx, userID, perm.PermFlowsWrite) {
		flows, err := unfinishedFlows(ctx, userID)
		if err != nil {
			return res, err
		}
		res.Flows = flows
	}
	if auth.Can(ctx, userID, perm.PermDashboardsWrite) {
		dashboards, err := unfinishedDashboards(ctx, userID)
		if err != nil {
			return res, err
		}
		res.Dashboards = dashboards
	}
	return res, nil
}

// unfinishedFlows walks flows newest change first and stops once the list is
// full, so a large installation compares only as many drafts as it needs.
func unfinishedFlows(ctx context.Context, userID uuid.UUID) ([]UnfinishedItem, error) {
	readable, err := flowops.ReadableFlowIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	var flows []*models.Flow
	if err := db.DB.Where("is_subflow = ?", false).Order("updated_at DESC").Find(&flows).Error; err != nil {
		return nil, err
	}
	out := []UnfinishedItem{}
	for _, f := range flows {
		if readable != nil && !readable[f.ID] {
			continue
		}
		if !flowops.Holds(ctx, userID, f, models.FlowAccessEdit) {
			continue
		}
		item, _ := flowops.Item(f)
		if !item.HasDraftChanges {
			continue
		}
		out = append(out, UnfinishedItem{ID: f.ID.String(), Name: f.Name, UpdatedAt: f.UpdatedAt, Deployed: item.Deployed})
		if len(out) == maxUnfinished {
			break
		}
	}
	return out, nil
}

// unfinishedDashboards lists editable dashboards with draft changes. A
// dashboard never deployed counts, since nobody sees it yet.
func unfinishedDashboards(ctx context.Context, userID uuid.UUID) ([]UnfinishedItem, error) {
	groupIDs, err := models.GetUserGroupIDs(userID)
	if err != nil {
		return nil, err
	}
	dashboards, _, err := models.ListAccessibleDashboards(userID, groupIDs, 1, 1000)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(dashboards, func(i, j int) bool { return dashboards[i].UpdatedAt.After(dashboards[j].UpdatedAt) })
	out := []UnfinishedItem{}
	for _, d := range dashboards {
		if !dashops.CanEdit(ctx, userID, d) {
			continue
		}
		deployed := d.DeployedVersion != 0
		if deployed {
			widgets, werr := models.ListDashboardWidgets(d.ID)
			sections, serr := models.ListDashboardSections(d.ID)
			pages, perr := models.ListDashboardPages(d.ID)
			if werr != nil || serr != nil || perr != nil {
				continue
			}
			dashops.AnnotateDraftState(d, widgets, sections, pages)
			if !d.HasDraftChanges {
				continue
			}
		}
		out = append(out, UnfinishedItem{ID: d.ID.String(), Name: d.Name, UpdatedAt: d.UpdatedAt, Deployed: deployed})
		if len(out) == maxUnfinished {
			break
		}
	}
	return out, nil
}
