package tsdbops

import (
	"context"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// forwardUsable reports whether the user may use every connection a forward
// reads from and writes to.
func forwardUsable(allowed map[uuid.UUID]bool, f *models.HistoryForward) bool {
	for _, id := range access.ForwardConnectionIDs(f) {
		if !access.AllowedString(allowed, id) {
			return false
		}
	}
	return true
}

// ListForwardsFor lists the forwards whose connections the user may all use.
func ListForwardsFor(ctx context.Context, userID uuid.UUID, source string) ([]ForwardItem, error) {
	items, err := ListForwards(source)
	if err != nil {
		return nil, err
	}
	allowed, err := access.ConnectionIDs(ctx, userID, access.Use)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	out := make([]ForwardItem, 0, len(items))
	for _, item := range items {
		if forwardUsable(allowed, &item.HistoryForward) {
			out = append(out, item)
		}
	}
	return out, nil
}

// StatusesFor reports the history writers of the connections the user may use.
func StatusesFor(ctx context.Context, userID uuid.UUID) ([]WriterStatus, error) {
	items, err := Statuses()
	if err != nil {
		return nil, err
	}
	allowed, err := access.ConnectionIDs(ctx, userID, access.Use)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	out := make([]WriterStatus, 0, len(items))
	for _, st := range items {
		if access.Allowed(allowed, st.ConnectionID) {
			out = append(out, st)
		}
	}
	return out, nil
}

// SaveForwardFor saves a forward when the user may use every connection it
// names, and, when it replaces one, every connection the old one named.
func SaveForwardFor(ctx context.Context, userID uuid.UUID, f *models.HistoryForward) (ForwardItem, error) {
	if f.ID != uuid.Nil {
		if existing, err := models.FindHistoryForward(f.ID); err == nil {
			if err := access.UseConnections(ctx, userID, access.ForwardConnectionIDs(existing)...); err != nil {
				return ForwardItem{}, apperrors.ErrNotFound.WithMessage("history forward not found")
			}
		}
	}
	if err := access.UseConnections(ctx, userID, access.ForwardConnectionIDs(f)...); err != nil {
		return ForwardItem{}, err
	}
	return SaveForward(f)
}

// DeleteForwardFor deletes a forward when the user may use its connections.
func DeleteForwardFor(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	existing, err := models.FindHistoryForward(id)
	if err != nil {
		return apperrors.ErrNotFound.WithMessage("history forward not found")
	}
	if err := access.UseConnections(ctx, userID, access.ForwardConnectionIDs(existing)...); err != nil {
		return apperrors.ErrNotFound.WithMessage("history forward not found")
	}
	return DeleteForward(id)
}
