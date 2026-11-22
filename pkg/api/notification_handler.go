package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// NotificationServicesHandler exposes configuration metadata for every provider.
func NotificationServicesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in struct{}, out *apiutil.Success[[]notification.Service]) error {
		out.Data = notification.Services()
		return nil
	})
	u.SetTitle("List notification services")
	u.SetTags("Notifications")
	return u
}

// NotificationEndpointsHandler returns IDs and labels, never private targets.
func NotificationEndpointsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[[]notification.Endpoint]) error {
		values, err := connops.NotificationEndpoints(in.ID)
		if err != nil {
			return err
		}
		out.Data = values
		return nil
	})
	u.SetTitle("List allowed notification endpoints")
	u.SetTags("Notifications")
	return u
}

// NotificationSendInput carries content and an approved endpoint ID only.
type NotificationSendInput struct {
	ID       string `path:"id" format:"uuid"`
	Endpoint string `json:"endpoint" required:"true"`
	Subject  string `json:"subject,omitempty"`
	Message  string `json:"message" required:"true"`
	Timeout  *int   `json:"timeout,omitempty"`
}

// NotificationSendHandler acts only through the configured service allowlist.
func NotificationSendHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in NotificationSendInput, out *apiutil.Success[any]) error {
		seconds := 30
		if in.Timeout != nil {
			seconds = *in.Timeout
		}
		if err := connops.SendNotification(ctx, in.ID, in.Endpoint, in.Subject, in.Message, seconds); err != nil {
			return err
		}
		out.Message = "Notification accepted"
		return nil
	})
	u.SetTitle("Send notification")
	u.SetDescription("Sends to one allowed endpoint. Failure or timeout may leave delivery uncertain; no automatic retry.")
	u.SetTags("Notifications")
	return u
}

// RegisterNotificationRoutes keeps settings discovery separate from invocation.
func RegisterNotificationRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).Method(http.MethodGet, "/notification_services", nethttp.NewHandler(NotificationServicesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).Method(http.MethodGet, "/connections/{id}/notifications/endpoints", nethttp.NewHandler(NotificationEndpointsHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsInvoke), requireConnection(access.Use), bearerDoc).Method(http.MethodPost, "/connections/{id}/notifications/send", nethttp.NewHandler(NotificationSendHandler()))
}
