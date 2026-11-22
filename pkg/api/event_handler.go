package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	db "github.com/grmrgecko/karakuri/pkg/database"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// EventListInput filters the event log.
type EventListInput struct {
	Category []string `query:"category" description:"Categories to include; repeatable."`
	Level    []string `query:"level" description:"Levels to include; repeatable."`
	Subject  []string `query:"subject" description:"Subjects as kind:id, such as control:<uuid>; repeatable."`
	Actor    string   `query:"actor" description:"Actor as kind or kind:id."`
	Q        string   `query:"q" description:"Text in the message, subject, or actor name."`
	From     string   `query:"from" description:"Earliest time: RFC 3339 or a relative form such as now-24h."`
	To       string   `query:"to" description:"Latest time: RFC 3339 or a relative form."`
	Before   uint     `query:"before" description:"Return entries older than this ID; the next_before of the previous page."`
	Limit    int      `query:"limit" minimum:"1" maximum:"500" description:"Entries per page; default 100."`
}

// EventFilter turns list input into a query filter for ctx's caller. A
// signed-in reader sees every category but security, and security too with
// the audit permission. A dashboard viewer without the read permission sees
// only the subjects the dashboard shows, never security.
func EventFilter(ctx context.Context, in EventListInput) (events.Filter, error) {
	f := events.Filter{Categories: in.Category, Levels: in.Level, Text: in.Q, Before: in.Before, Limit: in.Limit}
	for _, raw := range in.Subject {
		s, ok := events.ParseSubject(raw)
		if !ok {
			return f, apperrors.ErrValidation.WithMessage("subject must be kind:id")
		}
		f.Subjects = append(f.Subjects, s)
	}
	if in.Actor != "" {
		f.ActorKind, f.ActorID, _ = strings.Cut(in.Actor, ":")
	}
	now := time.Now()
	for _, t := range []struct {
		raw   string
		dst   *time.Time
		isEnd bool
	}{{in.From, &f.From, false}, {in.To, &f.To, true}} {
		if t.raw == "" {
			continue
		}
		at, err := tsdb.ParseTime(t.raw, now, t.isEnd)
		if err != nil {
			return f, apperrors.ErrValidation.WithMessage(err.Error())
		}
		*t.dst = at
	}

	uc := middleware.GetUser(ctx)
	if uc != nil && auth.Can(ctx, uc.UserID, perm.PermEventsRead) {
		f.Audit = auth.Can(ctx, uc.UserID, perm.PermEventsAudit)
		if err := flowops.LimitEventFlows(ctx, uc.UserID, &f); err != nil {
			return f, apperrors.ErrInternal.WithError(err)
		}
		return f, nil
	}
	scope := dashscope.FromContext(ctx)
	if scope == nil {
		return f, unauthorizedOrForbidden(ctx)
	}
	f.SubjectAllowed = func(s events.Subject) bool { return SubjectVisible(scope, s) }
	return f, nil
}

// SubjectVisible reports whether a dashboard scope shows a subject: a control,
// device, or Home Assistant entity one of its widgets is bound to.
func SubjectVisible(scope *dashscope.Scope, s events.Subject) bool {
	switch s.Kind {
	case "control":
		return scope.Allows(dashscope.Capability{Kind: dashscope.CapControl, Ref: s.ID})
	case "device":
		return scope.Allows(dashscope.Capability{Kind: dashscope.CapDevice, Ref: s.ID})
	case "ha_entity":
		conn, entity, ok := strings.Cut(s.ID, "/")
		return ok && scope.Allows(dashscope.Capability{Kind: dashscope.CapHA, Owner: conn, Ref: entity})
	}
	return false
}

func eventErr(err error) error {
	if errors.Is(err, events.ErrAuditRequired) {
		return apperrors.ErrForbidden.WithMessage(err.Error())
	}
	var app *apperrors.AppError
	if errors.As(err, &app) {
		return app
	}
	return apperrors.ErrValidation.WithMessage(err.Error())
}

func eventDoc(u documented, title, desc string) usecase.Interactor {
	u.SetTitle(title)
	u.SetDescription(desc)
	u.SetTags("Events")
	return u
}

func eventList() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EventListInput, out *apiutil.Success[events.Page]) error {
		f, err := EventFilter(ctx, in)
		if err != nil {
			return err
		}
		page, err := events.List(f)
		if err != nil {
			return eventErr(err)
		}
		out.Data = page
		return nil
	})
	return eventDoc(&u, "List Events", "Lists event log entries newest first. Security entries need the audit permission; a dashboard viewer reads only the subjects its dashboard shows.")
}

// EventSettingsView is the log's settings and its current size.
type EventSettingsView struct {
	events.Settings
	Entries int64 `json:"entries" description:"Entries stored now."`
}

func eventSettingsView() (EventSettingsView, error) {
	var n int64
	if err := db.DB.Model(&models.Event{}).Count(&n).Error; err != nil {
		return EventSettingsView{}, err
	}
	return EventSettingsView{Settings: events.Current(), Entries: n}, nil
}

func eventGetSettings() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[EventSettingsView]) error {
		v, err := eventSettingsView()
		out.Data = v
		return err
	})
	return eventDoc(&u, "Get Event Log Settings", "Reads retention per category, the entry limit, excluded controls, and the Home Assistant entities logged.")
}

func eventSaveSettings() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in events.Settings, out *apiutil.Success[EventSettingsView]) error {
		if _, err := events.SaveSettings(in); err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		v, err := eventSettingsView()
		out.Data = v
		return err
	})
	return eventDoc(&u, "Save Event Log Settings", "Changes retention, the entry limit, excluded controls, and the Home Assistant entities logged.")
}

// RegisterEventRoutes adds the event log routes.
func RegisterEventRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// The list route also serves dashboard viewers, who may hold no account,
	// so its own handler decides; the settings need the permissions.
	s.With(middleware.OptionalAuth(), bearerDoc).Method(http.MethodGet, "/events", nethttp.NewHandler(eventList()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermEventsRead), bearerDoc).
		Method(http.MethodGet, "/events/settings", nethttp.NewHandler(eventGetSettings()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermEventsWrite), bearerDoc).
		Method(http.MethodPut, "/events/settings", nethttp.NewHandler(eventSaveSettings()))
}
