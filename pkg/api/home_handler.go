package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/homeops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// HomeUpcomingInput sets how far ahead upcoming items are listed.
type HomeUpcomingInput struct {
	Hours int `query:"hours" minimum:"1" maximum:"24" description:"Hours ahead to look; default 12."`
}

// HomeUpcomingOutput is what is due within the window.
type HomeUpcomingOutput struct {
	Items []homeops.UpcomingItem `json:"items" description:"Schedule firings and wait deadlines, soonest first."`
}

// homeUser returns the signed-in caller's ID.
func homeUser(ctx context.Context) (uuid.UUID, error) {
	uc := middleware.GetUser(ctx)
	if uc == nil {
		return uuid.Nil, apperrors.ErrUnauthorized
	}
	return uc.UserID, nil
}

// HomeAttentionHandler returns the interactor that lists what needs attention.
func HomeAttentionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[homeops.AttentionResult]) error {
		uid, err := homeUser(ctx)
		if err != nil {
			return err
		}
		out.Data = homeops.Attention(ctx, uid, time.Now())
		return nil
	})
	u.SetTitle("Get Attention")
	u.SetDescription("Lists what needs attention among the sources the caller may read: connections in error, unstable, or stuck connecting; offline agents; flow failures of the last 24 hours grouped by error; failed or overdue backups; and failing history delivery. Names the sources checked and any that could not be read.")
	u.SetTags("Home")
	return u
}

// HomeUpcomingHandler returns the interactor that lists upcoming firings and
// wait deadlines.
func HomeUpcomingHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HomeUpcomingInput, out *apiutil.Success[HomeUpcomingOutput]) error {
		uid, err := homeUser(ctx)
		if err != nil {
			return err
		}
		window := homeops.DefaultUpcomingWindow
		if in.Hours > 0 {
			window = time.Duration(in.Hours) * time.Hour
		}
		items, err := homeops.Upcoming(ctx, uid, time.Now(), window)
		if err != nil {
			return apperrors.ErrServiceUnavailable.WithError(err)
		}
		out.Data = HomeUpcomingOutput{Items: items}
		return nil
	})
	u.SetTitle("List Upcoming")
	u.SetDescription("Lists the next schedule trigger firings and pending wait deadlines of enabled, deployed flows the caller may read, soonest first.")
	u.SetTags("Home")
	return u
}

// HomeUnfinishedHandler returns the interactor that lists unfinished work.
func HomeUnfinishedHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[homeops.UnfinishedResult]) error {
		uid, err := homeUser(ctx)
		if err != nil {
			return err
		}
		res, err := homeops.Unfinished(ctx, uid)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		out.Data = res
		return nil
	})
	u.SetTitle("List Unfinished Work")
	u.SetDescription("Lists flows and dashboards the caller may edit whose draft differs from the deployed version or that were never deployed, newest change first.")
	u.SetTags("Home")
	return u
}

// RegisterHomeRoutes registers the overview endpoints. They need only a
// signed-in caller; each source inside is gated by its own permission.
func RegisterHomeRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	r := s.With(middleware.Auth(), bearerDoc)
	r.Method(http.MethodGet, "/home/attention", nethttp.NewHandler(HomeAttentionHandler()))
	r.Method(http.MethodGet, "/home/upcoming", nethttp.NewHandler(HomeUpcomingHandler()))
	r.Method(http.MethodGet, "/home/unfinished", nethttp.NewHandler(HomeUnfinishedHandler()))
}
