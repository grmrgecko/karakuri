package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/zigbee2mqtt"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// Zigbee2MQTTDiscoverInput names the connection and base topic to read.
type Zigbee2MQTTDiscoverInput struct {
	ConnectionID string `query:"connection_id" required:"true" description:"MQTT connection Zigbee2MQTT publishes on."`
	BaseTopic    string `query:"base_topic" description:"Zigbee2MQTT base topic, default zigbee2mqtt."`
}

// Zigbee2MQTTDiscoverOutput is the device list as discovery shows it.
type Zigbee2MQTTDiscoverOutput struct {
	BaseTopic string                   `json:"base_topic" description:"Base topic read."`
	Devices   []zigbee2mqtt.Discovered `json:"devices" description:"Zigbee2MQTT devices, the coordinator left out."`
}

// Zigbee2MQTTDiscoverHandler returns the interactor listing Zigbee2MQTT's
// devices against the ones already added.
func Zigbee2MQTTDiscoverHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in Zigbee2MQTTDiscoverInput, out *apiutil.Success[Zigbee2MQTTDiscoverOutput]) error {
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		driver, err := connops.MQTTDriver(in.ConnectionID)
		if err != nil {
			return err
		}
		base := zigbee2mqtt.NormalizeBase(in.BaseTopic)
		devices, err := zigbee2mqtt.ReadDevices(ctx, driver, base)
		if err != nil {
			return err
		}
		existing, err := models.ListMQTTDevices()
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		out.Data = Zigbee2MQTTDiscoverOutput{BaseTopic: base, Devices: zigbee2mqtt.Discover(devices, existing, connID, base)}
		return nil
	})
	u.SetTitle("Discover Zigbee2MQTT Devices")
	u.SetDescription("Reads Zigbee2MQTT's retained device list and proposes a karakuri device for each.")
	u.SetTags("MQTT Devices")
	return u
}

// Zigbee2MQTTAddInput names the devices to add.
type Zigbee2MQTTAddInput struct {
	ConnectionID  string   `json:"connection_id" required:"true" description:"MQTT connection Zigbee2MQTT publishes on."`
	BaseTopic     string   `json:"base_topic,omitempty" description:"Zigbee2MQTT base topic, default zigbee2mqtt."`
	IEEEAddresses []string `json:"ieee_addresses" required:"true" minItems:"1" description:"IEEE addresses of the devices to add."`
}

// Zigbee2MQTTAddHandler returns the interactor that adds Zigbee2MQTT devices.
func Zigbee2MQTTAddHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in Zigbee2MQTTAddInput, out *apiutil.Success[zigbee2mqtt.AddResult]) error {
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		driver, err := connops.MQTTDriver(in.ConnectionID)
		if err != nil {
			return err
		}
		result, err := zigbee2mqtt.Add(ctx, driver, connID, in.BaseTopic, in.IEEEAddresses)
		if err != nil {
			return err
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Add Zigbee2MQTT Devices")
	u.SetDescription("Creates MQTT devices for the listed Zigbee2MQTT devices, with kind, options, and scripts generated from their exposes.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTDeviceRegenerateHandler returns the interactor that rebuilds a device
// added from Zigbee2MQTT.
func MQTTDeviceRegenerateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[mqttrelay.View]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid device id")
		}
		device, err := deviceForCaller(ctx, id)
		if err != nil {
			return err
		}
		driver, err := connops.MQTTDriver(device.ConnectionID.String())
		if err != nil {
			return err
		}
		saved, err := zigbee2mqtt.Regenerate(ctx, driver, id)
		if err != nil {
			return err
		}
		out.Data = mqttrelay.BuildView(saved, mqttrelay.D)
		out.Message = "Device regenerated"
		return nil
	})
	u.SetTitle("Regenerate MQTT Device")
	u.SetDescription("Rebuilds the kind, options, scripts, and topics of a device added from Zigbee2MQTT, replacing hand edits.")
	u.SetTags("MQTT Devices")
	return u
}

// registerZigbee2MQTTRoutes registers discovery, adding, and regeneration,
// gated like creating a device.
func registerZigbee2MQTTRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodGet, "/mqtt_devices/zigbee2mqtt",
			nethttp.NewHandler(Zigbee2MQTTDiscoverHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices/zigbee2mqtt",
			nethttp.NewHandler(Zigbee2MQTTAddHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices/{id}/regenerate",
			nethttp.NewHandler(MQTTDeviceRegenerateHandler()))
}
