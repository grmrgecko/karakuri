package energyops

import (
	"context"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/energy"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// CandidatesFor lists the energy candidates read through connections the user
// may use, and every control candidate.
func CandidatesFor(ctx context.Context, userID uuid.UUID) ([]energy.Candidate, error) {
	list, err := energy.Candidates(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := access.ConnectionIDs(ctx, userID, access.Use)
	if err != nil {
		return nil, err
	}
	out := make([]energy.Candidate, 0, len(list))
	for _, c := range list {
		if conn := access.SourceConnectionID(c.SourceKind, c.SourceID); conn != "" && !access.AllowedString(allowed, conn) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// SaveSourceFor saves a source when the user may use the connection it reads
// through, and, when it replaces one, the old source's.
func SaveSourceFor(ctx context.Context, userID uuid.UUID, id uuid.UUID, in SourceInput) (*models.EnergySource, error) {
	if id != uuid.Nil {
		if err := checkSource(ctx, userID, id); err != nil {
			return nil, err
		}
	}
	if err := access.UseConnections(ctx, userID, access.SourceConnectionID(in.SourceKind, in.SourceID)); err != nil {
		return nil, err
	}
	return SaveSource(id, in)
}

// ImportFor starts a history import when the user may use the source's
// connection.
func ImportFor(ctx context.Context, userID uuid.UUID, id uuid.UUID, days int) error {
	if err := checkSource(ctx, userID, id); err != nil {
		return err
	}
	return Import(id, days)
}

// checkSource refuses a stored source read through a connection the user
// cannot use.
func checkSource(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	var src models.EnergySource
	if err := db.DB.First(&src, "id = ?", id).Error; err != nil {
		return nil
	}
	if err := access.UseConnections(ctx, userID, access.SourceConnectionID(src.SourceKind, src.SourceID)); err != nil {
		return apperrors.ErrNotFound.WithMessage("energy source not found")
	}
	return nil
}
