package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// requirePermission checks the calling user holds one api:* permission,
// reading the user middleware.Auth put on the request context. Tools call it
// once per invocation, never per list item: auth.HasPermission walks the
// role tables on every call.
func requirePermission(ctx context.Context, permission string) error {
	uc := middleware.GetUser(ctx)
	if uc == nil {
		return errors.New("unauthorized: no authenticated user on this request")
	}
	if !auth.Can(ctx, uc.UserID, permission) {
		return errors.New("permission denied: this action requires " + permission)
	}
	return nil
}

// userID returns the calling user's ID, for operations that record ownership.
func userID(ctx context.Context) (uuid.UUID, error) {
	uc := middleware.GetUser(ctx)
	if uc == nil {
		return uuid.Nil, errors.New("unauthorized: no authenticated user on this request")
	}
	return uc.UserID, nil
}

// opErr unwraps an AppError to its code and message so an agent sees the same
// public validation details a REST client sees. Internal error chains never
// enter a tool result.
func opErr(err error) error {
	var app *apperrors.AppError
	if errors.As(err, &app) {
		message := app.Code + ": " + app.Message
		if len(app.Details) > 0 {
			details, _ := json.Marshal(app.Details)
			message += "\ndetails: " + string(details)
		}
		return errors.New(message)
	}
	return err
}

// callerID returns the authenticated caller's user ID, or zero when there is
// none; tools that need one have already refused an anonymous caller.
func callerID(ctx context.Context) uuid.UUID {
	id, _ := userID(ctx)
	return id
}

// loadFlow loads a flow the caller holds at least level on, under the same
// per-flow rules the REST routes apply.
func loadFlow(ctx context.Context, id string, level string) (*models.Flow, error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	f, err := flowops.LoadFor(ctx, uid, id, level)
	if err != nil {
		return nil, opErr(err)
	}
	return f, nil
}

// flowDetail builds a flow result for the caller: their access level, and no
// folder they hold nothing on, as the REST routes report a flow.
func flowDetail(ctx context.Context, f *models.Flow) (*flowops.FlowDetail, error) {
	detail, err := flowops.Detail(f)
	if err != nil {
		return nil, err
	}
	uid := callerID(ctx)
	detail.AccessLevel = flowops.Level(ctx, uid, f)
	flowops.MaskFolder(ctx, uid, &detail.FlowItem)
	return detail, nil
}
