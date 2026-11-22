package api

import (
	"context"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// parseControlID reads a control UUID from a path parameter.
func parseControlID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("invalid control ID")
	}
	return id, nil
}

// canReadControls reports whether the caller may see every control rather than
// only the public ones.
func canReadControls(ctx context.Context) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && auth.Can(ctx, uc.UserID, perm.PermControlsRead)
}

// ControlKindsHandler returns the interactor that lists the control kinds and
// their settings schemas. The settings panel renders the schema rather than
// hard-coding a form per kind, so a new kind needs no frontend change.
func ControlKindsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]controls.Kind]) error {
		out.Data = controls.Kinds()
		return nil
	})
	u.SetTitle("List Control Kinds")
	u.SetDescription("Returns every control kind with its settings schema and value type.")
	u.SetTags("Controls")
	return u
}

// ControlListHandler returns the interactor that lists every control with its
// current value.
func ControlListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]controls.Snapshot]) error {
		list := controls.S.List()

		// A caller without read access reached this through the public path —
		// an anonymous dashboard viewer, or a signed-in user whose role does
		// not include controls. It sees the controls marked public and the
		// ones the dashboard it named binds to, rather than an error where a
		// row would be or the whole catalog to get at one of them.
		if !canReadControls(ctx) {
			scope := dashscope.FromContext(ctx)
			visible := make([]controls.Snapshot, 0, len(list))
			for _, c := range list {
				if c.AllowPublic || scope.Allows(dashscope.Capability{Kind: dashscope.CapControl, Ref: c.ID.String()}) {
					visible = append(visible, c)
				}
			}
			list = visible
		}

		out.Data = list
		return nil
	})
	u.SetTitle("List Controls")
	u.SetDescription("Returns every control and its current value. Anonymous callers see only public controls.")
	u.SetTags("Controls")
	return u
}

// ControlGetInput identifies one control.
type ControlGetInput struct {
	ID string `path:"id" format:"uuid" description:"Control UUID."`
}

// ControlGetHandler returns the interactor that reads one control.
func ControlGetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ControlGetInput, out *apiutil.Success[controls.Snapshot]) error {
		id, err := parseControlID(in.ID)
		if err != nil {
			return err
		}
		snap, ok := controls.S.Get(id)
		if !ok {
			return apperrors.ErrNotFound.WithMessage("control not found")
		}
		if !snap.AllowPublic && !canReadControls(ctx) &&
			!dashscope.Allows(ctx, dashscope.Capability{Kind: dashscope.CapControl, Ref: id.String()}) {
			return unauthorizedOrForbidden(ctx)
		}
		out.Data = snap
		return nil
	})
	u.SetTitle("Get Control")
	u.SetDescription("Returns one control and its current value.")
	u.SetTags("Controls")
	return u
}

// ControlCreateInput defines a new control.
type ControlCreateInput struct {
	Name        string         `json:"name" required:"true" minLength:"1" description:"Display name, unique across controls."`
	Kind        string         `json:"kind" required:"true" description:"Control kind."`
	Icon        string         `json:"icon,omitempty" description:"Line-awesome icon class."`
	Options     map[string]any `json:"options,omitempty" description:"Kind-specific settings."`
	AllowPublic bool           `json:"allow_public,omitempty" description:"Whether anonymous viewers of a public dashboard may read and set it."`
}

// ControlCreateHandler returns the interactor that defines a control.
func ControlCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ControlCreateInput, out *apiutil.Success[controls.Snapshot]) error {
		snap, err := controls.S.Create(in.Name, in.Kind, in.Icon, in.Options, in.AllowPublic)
		if err != nil {
			return controlWriteError(err)
		}
		out.Data = snap
		out.Message = "Control created"
		return nil
	})
	u.SetTitle("Create Control")
	u.SetDescription("Defines a control, seeded with its kind's initial value.")
	u.SetTags("Controls")
	return u
}

// ControlUpdateInput rewrites a control's definition.
type ControlUpdateInput struct {
	ID          string         `path:"id" format:"uuid" description:"Control UUID."`
	Name        string         `json:"name" required:"true" minLength:"1" description:"Display name."`
	Kind        string         `json:"kind" required:"true" description:"Control kind."`
	Icon        string         `json:"icon,omitempty" description:"Line-awesome icon class."`
	Options     map[string]any `json:"options,omitempty" description:"Kind-specific settings."`
	AllowPublic bool           `json:"allow_public,omitempty" description:"Whether anonymous viewers of a public dashboard may read and set it."`
}

// ControlUpdateHandler returns the interactor that rewrites a control.
func ControlUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ControlUpdateInput, out *apiutil.Success[controls.Snapshot]) error {
		id, err := parseControlID(in.ID)
		if err != nil {
			return err
		}
		snap, err := controls.S.Update(id, in.Name, in.Kind, in.Icon, in.Options, in.AllowPublic)
		if err != nil {
			return controlWriteError(err)
		}
		out.Data = snap
		out.Message = "Control updated"
		return nil
	})
	u.SetTitle("Update Control")
	u.SetDescription("Rewrites a control's definition. Changing its kind re-seeds the value.")
	u.SetTags("Controls")
	return u
}

// ControlDeleteHandler returns the interactor that removes a control.
func ControlDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ControlGetInput, out *apiutil.Success[struct{}]) error {
		id, err := parseControlID(in.ID)
		if err != nil {
			return err
		}
		if _, ok := controls.S.Get(id); !ok {
			return apperrors.ErrNotFound.WithMessage("control not found")
		}
		if err := controls.S.Delete(id); err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		tsdbops.DeleteOwned(id)
		out.Message = "Control deleted"
		return nil
	})
	u.SetTitle("Delete Control")
	u.SetDescription("Removes a control. Dashboard rows pointing at it stop resolving rather than erroring.")
	u.SetTags("Controls")
	return u
}

// ControlSetInput carries the new value for a control.
type ControlSetInput struct {
	ID string `path:"id" format:"uuid" description:"Control UUID."`
	// Attribute names which part of a composite control is being written, such
	// as a light's brightness. Empty writes the whole value, and a scalar sent
	// to a composite control lands on its primary attribute.
	Attribute string `json:"attribute,omitempty" description:"Attribute of the control's kind being set; empty writes the whole value."`
	Value     any    `json:"value" description:"New value, in the shape the control's kind holds."`
}

// ControlSetHandler returns the interactor that operates a control. This is
// the dashboard's write path, so it is authorized per control rather than
// per user: a control marked public may be set by anonymous viewers of a
// public dashboard, and everything else needs api:controls:set.
func ControlSetHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ControlSetInput, out *apiutil.Success[controls.Snapshot]) error {
		id, err := parseControlID(in.ID)
		if err != nil {
			return err
		}
		snap, ok := controls.S.Get(id)
		if !ok {
			return apperrors.ErrNotFound.WithMessage("control not found")
		}

		// Check the write is allowed before anything moves: the control is
		// marked public, the caller holds the permission outright, or a
		// dashboard they may view puts this control on the page and permits
		// operating it.
		uc := middleware.GetUser(ctx)
		if !snap.AllowPublic &&
			!(uc != nil && auth.Can(ctx, uc.UserID, perm.PermControlsSet)) &&
			!dashscope.Allows(ctx, dashscope.Capability{Kind: dashscope.CapControl, Ref: id.String(), Write: true}) {
			return unauthorizedOrForbidden(ctx)
		}

		w := controls.By(controls.SourceDashboard)
		w.Actor = events.ActorFrom(ctx)
		next, err := controls.S.SetAttribute(id, in.Attribute, in.Value, w)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		out.Data = next
		return nil
	})
	u.SetTitle("Set Control")
	u.SetDescription("Writes a control's value, coercing it to the control's kind and refusing what the kind cannot hold.")
	u.SetTags("Controls")
	return u
}

// controlWriteError maps a store failure onto the right status: a duplicate
// name is a conflict, a bad kind or missing name is a validation error, and
// anything else is ours.
func controlWriteError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "unique"):
		return apperrors.ErrConflict.WithMessage("a control with this name already exists")
	case strings.Contains(msg, "record not found"):
		return apperrors.ErrNotFound.WithMessage("control not found")
	case strings.Contains(msg, "unknown control kind"), strings.Contains(msg, "name is required"):
		return apperrors.ErrValidation.WithMessage(msg)
	}
	return apperrors.ErrInternal.WithError(err)
}

// RegisterControlRoutes registers the control endpoints. Reading and setting
// are optionally authenticated so a public dashboard can render and operate
// the controls marked public; the handlers enforce the rest.
func RegisterControlRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// The kind catalog is a description of the software, not of the house: it
	// carries no control, no value, and no name anyone chose. A dashboard
	// viewer holding no permission still has to draw a light with the
	// attributes a light has, and the MQTT device panel picks from the same
	// list without needing control access, so it is gated no harder than the
	// guides are.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/controls/kinds",
			nethttp.NewHandler(ControlKindsHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/controls",
			nethttp.NewHandler(ControlListHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/controls/{id}",
			nethttp.NewHandler(ControlGetHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermControlsWrite), bearerDoc).
		Method(http.MethodPost, "/controls",
			nethttp.NewHandler(ControlCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermControlsWrite), bearerDoc).
		Method(http.MethodPut, "/controls/{id}",
			nethttp.NewHandler(ControlUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermControlsWrite), bearerDoc).
		Method(http.MethodDelete, "/controls/{id}",
			nethttp.NewHandler(ControlDeleteHandler()))
	s.With(middleware.OptionalAuth()).
		Method(http.MethodPost, "/controls/{id}/set",
			nethttp.NewHandler(ControlSetHandler()))
}
