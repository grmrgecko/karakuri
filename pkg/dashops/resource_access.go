package dashops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// checkDraftConnections refuses a deploy whose draft reads a connection the
// deployer cannot use: an entity, camera, condition, or variable binding, a
// bound device's connection, or a chart query. Viewers reach those through the
// dashboard, so the deployer vouches for every one.
func checkDraftConnections(ctx context.Context, dashboard *models.Dashboard, userID uuid.UUID) error {
	connections, devices, err := dashscope.DraftConnectionRefs(dashboard)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	for _, raw := range devices {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			continue
		}
		if !access.CanUseConnection(ctx, userID, device.ConnectionID.String()) {
			return apperrors.ErrForbidden.WithMessage(fmt.Sprintf("a widget binds device %q, whose connection is not shared with you", device.Name))
		}
	}
	placements, err := models.ListDashboardWidgets(dashboard.ID)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	for _, p := range placements {
		if p.Widget.PackageName == chartPackage {
			connections = append(connections, chartConnectionIDs(p.Config)...)
		}
	}
	if dashboard.Display != nil {
		for _, v := range dashboard.Display.Variables {
			if v.Query != nil && v.Query.ConnectionID != "" {
				connections = append(connections, v.Query.ConnectionID)
			}
		}
	}
	return checkConnectionIDs(ctx, userID, connections)
}

// checkConnectionIDs refuses the first connection ID the user cannot use. IDs
// that are not UUIDs, such as the energy pseudo-connection, are left alone.
func checkConnectionIDs(ctx context.Context, userID uuid.UUID, ids []string) error {
	checked := map[string]bool{}
	for _, id := range ids {
		if checked[id] {
			continue
		}
		checked[id] = true
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		if !access.CanUseConnection(ctx, userID, id) {
			return apperrors.ErrForbidden.WithMessage(fmt.Sprintf("the dashboard reads connection %s, which is not shared with you", id))
		}
	}
	return nil
}

// chartConnectionIDs returns the connections a chart placement's queries name.
func chartConnectionIDs(config string) []string {
	var c struct {
		Queries []struct {
			ConnectionID string `json:"connectionId"`
		} `json:"queries"`
	}
	if config == "" || json.Unmarshal([]byte(config), &c) != nil {
		return nil
	}
	var ids []string
	for _, q := range c.Queries {
		if q.ConnectionID != "" {
			ids = append(ids, q.ConnectionID)
		}
	}
	return ids
}
