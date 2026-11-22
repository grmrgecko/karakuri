package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// ResourceShare is one grant on a connection or an agent, with the grantee
// reduced to what a share dialog names them by.
type ResourceShare struct {
	ID         uint              `json:"id" description:"Share entry ID."`
	UserID     *uuid.UUID        `json:"user_id,omitempty" description:"Granted user ID."`
	User       *models.User      `json:"user,omitempty" description:"Granted user."`
	GroupID    *uint             `json:"group_id,omitempty" description:"Granted user group ID."`
	Group      *models.UserGroup `json:"group,omitempty" description:"Granted user group."`
	Permission string            `json:"permission" description:"Access level: use, edit, or admin."`
	CreatedAt  time.Time         `json:"created_at" description:"Creation timestamp."`
}

// ResourceSharesInput identifies the connection or agent whose shares are read.
type ResourceSharesInput struct {
	ID string `path:"id" format:"uuid" description:"Connection or agent UUID."`
}

// ResourceAddShareInput names the resource, the grantee, and the level.
type ResourceAddShareInput struct {
	ID         string `path:"id" format:"uuid" description:"Connection or agent UUID."`
	Username   string `json:"username,omitempty" description:"Username to share with."`
	GroupName  string `json:"group_name,omitempty" description:"User group name to share with."`
	Permission string `json:"permission" enum:"use,edit,admin" default:"use" description:"Access level: use lists, invokes, and references it; edit also changes it; admin also shares and deletes it."`
}

// ResourceRemoveShareInput identifies one share to remove.
type ResourceRemoveShareInput struct {
	ID      string `path:"id" format:"uuid" description:"Connection or agent UUID."`
	ShareID uint   `path:"shareId" description:"Share entry ID."`
}

// shareTarget resolves a share request's grantee and level, and a label for
// the event log.
func shareTarget(in ResourceAddShareInput) (userID *uuid.UUID, groupID *uint, permission, grantee string, err error) {
	if (in.Username == "") == (in.GroupName == "") {
		return nil, nil, "", "", apperrors.ErrValidation.WithMessage("give either username or group_name")
	}
	permission = in.Permission
	if permission == "" {
		permission = access.Use
	}
	if !models.ValidResourceAccess(permission) {
		return nil, nil, "", "", apperrors.ErrValidation.WithMessage("permission must be use, edit, or admin")
	}
	if in.Username != "" {
		var user models.User
		if err := db.DB.Where("username = ?", in.Username).First(&user).Error; err != nil {
			return nil, nil, "", "", apperrors.ErrValidation.WithMessage("user not found")
		}
		return &user.ID, nil, permission, user.Username, nil
	}
	var group models.UserGroup
	if err := db.DB.Where("name = ?", in.GroupName).First(&group).Error; err != nil {
		return nil, nil, "", "", apperrors.ErrValidation.WithMessage("group not found")
	}
	return nil, &group.ID, permission, "group " + group.Name, nil
}

// granteeLabel names a removed share's grantee for the event log.
func granteeLabel(user *models.User, group *models.UserGroup) string {
	switch {
	case user != nil:
		return user.Username
	case group != nil:
		return "group " + group.Name
	}
	return "a removed account"
}

// recordResourceShare records a change to who a connection or agent is shared
// with, under the resource's own event category and subject.
func recordResourceShare(ctx context.Context, category, kind string, id uuid.UUID, name, message string, details map[string]any) {
	events.Record(events.Entry{
		Category: category,
		Subject:  events.Subject{Kind: kind, ID: id.String(), Name: name},
		Message:  message,
		Details:  details,
		Actor:    events.ActorFrom(ctx),
	})
}

// ConnectionSharesHandler lists who a connection is shared with.
func ConnectionSharesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ResourceSharesInput, out *apiutil.Success[[]ResourceShare]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		entries, err := models.ListConnectionAccess(c.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		shares := make([]ResourceShare, 0, len(entries))
		for _, e := range entries {
			shares = append(shares, ResourceShare{ID: e.ID, UserID: e.UserID, User: shareGrantee(e.User), GroupID: e.GroupID, Group: e.Group, Permission: e.Permission, CreatedAt: e.CreatedAt})
		}
		out.Data = shares
		return nil
	})
	u.SetTitle("List Connection Shares")
	u.SetDescription("Returns the users and groups a connection is shared with and at which level. Needs admin on the connection.")
	u.SetTags("Connections")
	return u
}

// ConnectionAddShareHandler shares a connection with a user or group.
func ConnectionAddShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ResourceAddShareInput, out *apiutil.Success[ResourceShare]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		userID, groupID, permission, grantee, err := shareTarget(in)
		if err != nil {
			return err
		}
		entry := &models.ConnectionAccess{ConnectionID: c.ID, UserID: userID, GroupID: groupID, Permission: permission}
		if err := db.DB.Create(entry).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		recordResourceShare(ctx, models.EventConnection, "connection", c.ID, c.Name,
			fmt.Sprintf("Shared connection %s with %s at %s", c.Name, grantee, permission),
			map[string]any{"grantee": grantee, "permission": permission})
		out.Data = ResourceShare{ID: entry.ID, UserID: userID, GroupID: groupID, Permission: permission, CreatedAt: entry.CreatedAt}
		return nil
	})
	u.SetTitle("Share Connection")
	u.SetDescription("Shares a connection with a user or group by name at use, edit, or admin level. Needs admin on the connection.")
	u.SetTags("Connections")
	return u
}

// ConnectionRemoveShareHandler removes one share from a connection.
func ConnectionRemoveShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ResourceRemoveShareInput, out *apiutil.Success[any]) error {
		c, err := access.Connection(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		// The share must belong to this connection, so a share ID cannot reach
		// a resource the caller does not administer.
		var entry models.ConnectionAccess
		if err := db.DB.Preload("User").Preload("Group").Where("connection_id = ? AND id = ?", c.ID, in.ShareID).First(&entry).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("share not found")
		}
		if err := db.DB.Delete(&models.ConnectionAccess{}, entry.ID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		grantee := granteeLabel(entry.User, entry.Group)
		recordResourceShare(ctx, models.EventConnection, "connection", c.ID, c.Name,
			fmt.Sprintf("Stopped sharing connection %s with %s", c.Name, grantee),
			map[string]any{"grantee": grantee, "permission": entry.Permission})
		out.Message = "Share removed"
		return nil
	})
	u.SetTitle("Remove Connection Share")
	u.SetDescription("Removes one share from a connection. Needs admin on the connection.")
	u.SetTags("Connections")
	return u
}

// AgentSharesHandler lists who an agent is shared with.
func AgentSharesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ResourceSharesInput, out *apiutil.Success[[]ResourceShare]) error {
		a, err := access.Agent(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		entries, err := models.ListAgentAccess(a.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		shares := make([]ResourceShare, 0, len(entries))
		for _, e := range entries {
			shares = append(shares, ResourceShare{ID: e.ID, UserID: e.UserID, User: shareGrantee(e.User), GroupID: e.GroupID, Group: e.Group, Permission: e.Permission, CreatedAt: e.CreatedAt})
		}
		out.Data = shares
		return nil
	})
	u.SetTitle("List Agent Shares")
	u.SetDescription("Returns the users and groups an agent is shared with and at which level. Needs admin on the agent.")
	u.SetTags("Agents")
	return u
}

// AgentAddShareHandler shares an agent with a user or group.
func AgentAddShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ResourceAddShareInput, out *apiutil.Success[ResourceShare]) error {
		a, err := access.Agent(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		userID, groupID, permission, grantee, err := shareTarget(in)
		if err != nil {
			return err
		}
		entry := &models.AgentAccess{AgentID: a.ID, UserID: userID, GroupID: groupID, Permission: permission}
		if err := db.DB.Create(entry).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		recordResourceShare(ctx, models.EventAgent, "agent", a.ID, a.Name,
			fmt.Sprintf("Shared agent %s with %s at %s", a.Name, grantee, permission),
			map[string]any{"grantee": grantee, "permission": permission})
		out.Data = ResourceShare{ID: entry.ID, UserID: userID, GroupID: groupID, Permission: permission, CreatedAt: entry.CreatedAt}
		return nil
	})
	u.SetTitle("Share Agent")
	u.SetDescription("Shares an agent with a user or group by name at use, edit, or admin level. Needs admin on the agent.")
	u.SetTags("Agents")
	return u
}

// AgentRemoveShareHandler removes one share from an agent.
func AgentRemoveShareHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ResourceRemoveShareInput, out *apiutil.Success[any]) error {
		a, err := access.Agent(ctx, callerID(ctx), in.ID, access.Admin)
		if err != nil {
			return err
		}
		var entry models.AgentAccess
		if err := db.DB.Preload("User").Preload("Group").Where("agent_id = ? AND id = ?", a.ID, in.ShareID).First(&entry).Error; err != nil {
			return apperrors.ErrNotFound.WithMessage("share not found")
		}
		if err := db.DB.Delete(&models.AgentAccess{}, entry.ID).Error; err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		grantee := granteeLabel(entry.User, entry.Group)
		recordResourceShare(ctx, models.EventAgent, "agent", a.ID, a.Name,
			fmt.Sprintf("Stopped sharing agent %s with %s", a.Name, grantee),
			map[string]any{"grantee": grantee, "permission": entry.Permission})
		out.Message = "Share removed"
		return nil
	})
	u.SetTitle("Remove Agent Share")
	u.SetDescription("Removes one share from an agent. Needs admin on the agent.")
	u.SetTags("Agents")
	return u
}

// RegisterResourceShareRoutes registers the connection and agent sharing
// endpoints. They need no global permission beyond signing in: admin on the
// connection or agent is the check.
func RegisterResourceShareRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/shares", nethttp.NewHandler(ConnectionSharesHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/connections/{id}/shares", nethttp.NewHandler(ConnectionAddShareHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodDelete, "/connections/{id}/shares/{shareId}", nethttp.NewHandler(ConnectionRemoveShareHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/agents/{id}/shares", nethttp.NewHandler(AgentSharesHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/agents/{id}/shares", nethttp.NewHandler(AgentAddShareHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodDelete, "/agents/{id}/shares/{shareId}", nethttp.NewHandler(AgentRemoveShareHandler()))
}
