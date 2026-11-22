package connops

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// ListFor returns the connections a user may use, with masked config, live
// status, and the level they hold on each.
func ListFor(ctx context.Context, userID uuid.UUID) ([]*ConnectionItem, error) {
	allowed, err := access.ConnectionIDs(ctx, userID, access.Use)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	conns, err := models.ListConnections()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	items := make([]*ConnectionItem, 0, len(conns))
	for _, c := range conns {
		if !access.Allowed(allowed, c.ID) {
			continue
		}
		item, err := ItemFor(ctx, userID, c)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// ItemFor is Item with the level the user holds on the connection.
func ItemFor(ctx context.Context, userID uuid.UUID, c *models.Connection) (*ConnectionItem, error) {
	item, err := Item(c)
	if err != nil {
		return nil, err
	}
	item.Access = models.ResourceAccessLevel(access.ConnectionRank(ctx, userID, c))
	return item, nil
}

// CheckConfigConnections refuses a connection config naming another
// connection the user cannot use, such as a calendar connection signing in
// through an OAuth connection.
func CheckConfigConnections(ctx context.Context, userID uuid.UUID, kind string, cfg map[string]any) error {
	def, ok := integrations.KindFor(kind)
	if !ok {
		return nil
	}
	for _, field := range def.ConfigSpec {
		if field.Type != flow.FieldConnection {
			continue
		}
		id, _ := cfg[field.ID].(string)
		if id == "" {
			continue
		}
		if !access.CanUseConnection(ctx, userID, id) {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("%s names connection %s, which is not shared with you", field.ID, id))
		}
	}
	return nil
}
