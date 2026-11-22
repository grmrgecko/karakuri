package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/autoimport"
	"github.com/grmrgecko/karakuri/pkg/entitydevices"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// AutoImportStatusInput names the connection.
type AutoImportStatusInput struct {
	ID string `path:"id" description:"Connection UUID."`
}

// AutoImportStatusHandler returns the interactor reporting a connection's
// auto import.
func AutoImportStatusHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in AutoImportStatusInput, out *apiutil.Success[autoimport.Status]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if autoimport.M == nil {
			out.Data = autoimport.Status{Skipped: []autoimport.Skip{}}
			return nil
		}
		out.Data = autoimport.M.Status(id)
		return nil
	})
	u.SetTitle("Get Auto Import Status")
	u.SetDescription("Reports whether the connection imports devices automatically, how many it imported, which reported devices it skipped and why, and when it last imported or removed one.")
	u.SetTags("Connections")
	return u
}

// MQTTDiscoveryInput names the MQTT connection to read discovery from.
type MQTTDiscoveryInput struct {
	ConnectionID string `query:"connection_id" required:"true" description:"MQTT connection."`
}

// MQTTDiscoveryOutput is the discovery configs as the add dialog lists them.
type MQTTDiscoveryOutput struct {
	Devices []entitydevices.Discovered `json:"devices" description:"Discovery configs with the device each would become; object_id is the discovery key."`
}

// MQTTDiscoveryHandler returns the interactor listing Home Assistant MQTT
// discovery configs.
func MQTTDiscoveryHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTDiscoveryInput, out *apiutil.Success[MQTTDiscoveryOutput]) error {
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		devices, err := autoimport.DiscoverMQTT(ctx, in.ConnectionID)
		if err != nil {
			return err
		}
		out.Data = MQTTDiscoveryOutput{Devices: devices}
		return nil
	})
	u.SetTitle("List MQTT Discovery Devices")
	u.SetDescription("Reads the retained Home Assistant MQTT discovery configs on a connection for three seconds and lists the device each would become.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTDiscoveryAddInput names the discovery configs to add.
type MQTTDiscoveryAddInput struct {
	ConnectionID string   `json:"connection_id" required:"true" description:"MQTT connection."`
	ObjectIDs    []string `json:"object_ids" required:"true" minItems:"1" description:"Discovery keys of the configs to add."`
}

// MQTTDiscoveryAddHandler returns the interactor adding discovery configs as
// devices.
func MQTTDiscoveryAddHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTDiscoveryAddInput, out *apiutil.Success[entitydevices.AddResult]) error {
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		result, err := autoimport.AddMQTT(ctx, in.ConnectionID, in.ObjectIDs)
		if err != nil {
			return err
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Add MQTT Discovery Devices")
	u.SetDescription("Creates a device for each listed Home Assistant MQTT discovery config.")
	u.SetTags("MQTT Devices")
	return u
}

// registerAutoImportRoutes registers auto import status and the MQTT
// discovery picker.
func registerAutoImportRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsRead), requireConnection(access.Use), bearerDoc).
		Method(http.MethodGet, "/connections/{id}/auto_import",
			nethttp.NewHandler(AutoImportStatusHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodGet, "/mqtt_devices/mqtt_discovery",
			nethttp.NewHandler(MQTTDiscoveryHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices/mqtt_discovery",
			nethttp.NewHandler(MQTTDiscoveryAddHandler()))
}
