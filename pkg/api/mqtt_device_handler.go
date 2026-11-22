package api

import (
	"context"
	"errors"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// MQTT devices are the hand-written answer to MQTT having no discovery: a
// device names the topics it speaks on and the script that reads them, and the
// relay keeps the values that script returns. These handlers define them; the
// values themselves reach a dashboard over the broker, not over HTTP.

// MQTTDeviceListHandler returns the interactor listing every device.
func MQTTDeviceListHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[[]mqttrelay.View]) error {
		devices, err := models.ListMQTTDevices()
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}

		// A caller with connection read sees the devices on connections they
		// may use. A dashboard viewer also sees the devices that dashboard's
		// widgets bind to and no others: a row needs its own device, not the
		// list of every device the house has.
		uc := middleware.GetUser(ctx)
		reader := uc != nil && auth.Can(ctx, uc.UserID, perm.PermConnectionsRead)
		var usable map[uuid.UUID]bool
		if reader {
			var err error
			if usable, err = access.ConnectionIDs(ctx, uc.UserID, access.Use); err != nil {
				return apperrors.ErrInternal.WithError(err)
			}
		}
		var visible map[string]bool
		if scope := dashscope.FromContext(ctx); scope != nil {
			visible = scope.Refs(dashscope.CapDevice)
		} else if !reader {
			return unauthorizedOrForbidden(ctx)
		}

		views := make([]mqttrelay.View, 0, len(devices))
		for _, d := range devices {
			owned := reader && access.Allowed(usable, d.ConnectionID)
			if !owned && !visible[d.ID.String()] {
				continue
			}
			view := mqttrelay.BuildView(d, mqttrelay.D)
			if !owned {
				view = view.ForViewer()
			}
			views = append(views, view)
		}
		out.Data = views
		return nil
	})
	u.SetTitle("List MQTT Devices")
	u.SetDescription("Returns every declared MQTT device with the values its decoder has accumulated.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTDeviceInput is a device definition being created.
type MQTTDeviceInput = mqttrelay.Definition

// MQTTDeviceUpdateInput is a definition replacing an existing device.
type MQTTDeviceUpdateInput struct {
	ID string `path:"id" description:"Device UUID."`
	MQTTDeviceInput
}

// MQTTDeviceCreateHandler returns the interactor that declares a device.
func MQTTDeviceCreateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTDeviceInput, out *apiutil.Success[mqttrelay.View]) error {
		if err := useConnectionID(ctx, in.ConnectionID.String()); err != nil {
			return err
		}
		d, err := mqttrelay.SaveDefinition(uuid.Nil, in, mqttrelay.Origin{})
		if err != nil {
			return err
		}
		out.Data = mqttrelay.BuildView(d, mqttrelay.D)
		out.Message = "Device created"
		return nil
	})
	u.SetTitle("Create MQTT Device")
	u.SetDescription("Declares an MQTT device: the topics it speaks on and the script that reads them.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTDeviceUpdateHandler returns the interactor that replaces a definition.
func MQTTDeviceUpdateHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTDeviceUpdateInput, out *apiutil.Success[mqttrelay.View]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid device id")
		}
		if _, err := deviceForCaller(ctx, id); err != nil {
			return err
		}
		if err := useConnectionID(ctx, in.ConnectionID.String()); err != nil {
			return err
		}
		d, err := mqttrelay.SaveDefinition(id, in.MQTTDeviceInput, mqttrelay.Origin{})
		if err != nil {
			return err
		}
		out.Data = mqttrelay.BuildView(d, mqttrelay.D)
		out.Message = "Device saved"
		return nil
	})
	u.SetTitle("Update MQTT Device")
	u.SetDescription("Replaces a device definition and restarts its subscriptions.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTDeviceDeleteHandler returns the interactor that removes a device.
func MQTTDeviceDeleteHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[struct{}]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid device id")
		}
		if _, err := deviceForCaller(ctx, id); err != nil {
			return err
		}
		if err := mqttrelay.DeleteDevice(id); err != nil {
			return err
		}
		tsdbops.DeleteOwned(id)
		out.Message = "Device deleted"
		return nil
	})
	u.SetTitle("Delete MQTT Device")
	u.SetDescription("Removes a device and withdraws its subscriptions.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTDeviceCommandInput is one interaction with a device: which of its values
// is being commanded and what to. The caller sends the interaction, never the
// payload — the device owns how that is built, which is what lets a viewer who
// may not write scripts still press a button.
type MQTTDeviceCommandInput struct {
	ID string `path:"id" description:"Device UUID."`
	// Field names the attribute of the device's control kind being commanded:
	// "on" for a switch, "brightness" or "color" for a light, "position" for a
	// cover. Empty means the kind's primary attribute, which is what a face
	// with one thing to press sends.
	Field    string `json:"field,omitempty" description:"Attribute of the device's control kind being set."`
	Value    any    `json:"value,omitempty" description:"The value being commanded."`
	On       *bool  `json:"on,omitempty" description:"Direction, for an attribute that has two."`
	Previous any    `json:"previous,omitempty" description:"What the value was before."`
}

// MQTTDeviceCommandHandler returns the interactor that commands a device. The
// payload is built from the device's own template or script, so what reaches
// the broker is what the person who declared the device wrote, not what the
// browser asked for.
func MQTTDeviceCommandHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTDeviceCommandInput, out *apiutil.Success[struct{}]) error {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid device id")
		}
		// Commanding is either the connection permission or a dashboard that
		// puts this device on the page and permits operating it. The check
		// needs only the id, so it runs before the lookup — a 404 would tell
		// an unauthorized caller which device UUIDs exist.
		viaDashboard := dashscope.Allows(ctx, dashscope.Capability{Kind: dashscope.CapDevice, Ref: id.String(), Write: true})
		device, err := models.FindMQTTDeviceByID(id)
		if !viaDashboard && (err != nil || !canInvokeConnection(ctx, device.ConnectionID.String())) {
			return unauthorizedOrForbidden(ctx)
		}
		if err != nil {
			return apperrors.ErrNotFound
		}

		if mqttrelay.D == nil {
			return apperrors.ErrBadGateway.WithMessage("devices are not running")
		}
		// The kind checks the interaction before anything is sent: a slider
		// outside its bounds or an effect the light does not have never
		// reaches the device.
		result, err := mqttrelay.D.Command(ctx, device, in.Field, in.Value, in.On, in.Previous)
		if err != nil {
			var offline *mqttrelay.OfflineError
			var invalid *mqttrelay.InvalidCommandError
			switch {
			case errors.As(err, &offline):
				return apperrors.ErrConflict.WithMessage(err.Error())
			case errors.As(err, &invalid):
				return apperrors.ErrValidation.WithMessage(err.Error())
			}
			return apperrors.ErrBadGateway.WithMessage(err.Error())
		}
		// No topic is the payload script saying this change means nothing.
		if !result.Sent {
			out.Message = "No action"
			return nil
		}
		out.Message = "Command sent"
		return nil
	})
	u.SetTitle("Command a Device")
	u.SetDescription("Sends one interaction to a device: its payload script for MQTT, its entity for ESPHome.")
	u.SetTags("MQTT Devices")
	return u
}

// MQTTScriptTestInput runs one of a device's scripts against a sample. The
// sample is whatever that script reads, so the caller names the mode rather
// than the endpoint guessing from the shape of what it was handed.
type MQTTScriptTestInput struct {
	Mode   string `json:"mode" required:"true" enum:"state,payload" description:"Which script is being tested."`
	Script string `json:"script" required:"true" description:"The script source."`
	// State mode.
	Topic    string         `json:"topic,omitempty" description:"Topic the sample arrived on."`
	Payload  string         `json:"payload,omitempty" description:"Sample payload."`
	State    map[string]any `json:"state,omitempty" description:"Values the device already holds."`
	Retained bool           `json:"retained,omitempty" description:"Test the sample as a message delivered from the broker's retained store."`
	// TimeoutMS is the limit to test under, as the device would set it.
	TimeoutMS int `json:"timeout_ms,omitempty" description:"Time limit in milliseconds; 0 is the default for the mode."`
	// Payload mode.
	Field    string `json:"field,omitempty" description:"Which value is being set."`
	Value    any    `json:"value,omitempty" description:"The value being commanded."`
	On       *bool  `json:"on,omitempty" description:"Direction, for a control that has two."`
	Previous any    `json:"previous,omitempty" description:"What the value was before."`
	// Control and Options let the test hold the sample to the device's kind
	// the way a real command is held, so a value the kind would refuse fails
	// here rather than passing the test and failing the press.
	Control string         `json:"control,omitempty" description:"Control kind to check the command against, as the device declares."`
	Options map[string]any `json:"options,omitempty" description:"Settings of that kind."`
}

// MQTTScriptTestResult is what a test run produced: what the script printed and
// what it returned, or why it failed. A failure is part of the result rather
// than an error status, because a script that throws is the thing being looked
// at, not a problem with the request.
type MQTTScriptTestResult struct {
	Value  any      `json:"value" description:"What the script returned."`
	Output []string `json:"output" description:"Console lines, in order."`
	Error  string   `json:"error,omitempty" description:"Why it failed, when it did."`
}

// MQTTScriptTestHandler returns the interactor behind the editor's Test button.
// It runs the same code the device runs, so what an author sees here is what
// will happen — and it touches nothing: no subscription, no publish, no state.
func MQTTScriptTestHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in MQTTScriptTestInput, out *apiutil.Success[MQTTScriptTestResult]) error {
		var result MQTTScriptTestResult
		if err := mqttrelay.CheckTestTimeout(in.Mode, in.TimeoutMS); err != nil {
			return err
		}

		if in.Mode == "payload" {
			// Hold the sample to the kind the way the command endpoint holds a
			// real press. A refusal is a result rather than an error status:
			// the mismatch is exactly what the author is here to see.
			field, value := in.Field, in.Value
			if strings.TrimSpace(in.Control) != "" {
				device := &models.MQTTDevice{Name: "test", Control: in.Control}
				device.SetOptionMap(in.Options)
				var err error
				if field, value, err = mqttrelay.ResolveCommand(device, field, value); err != nil {
					result.Error = err.Error()
					out.Data = result
					return nil
				}
			}
			vars := mqttrelay.CommandVars(field, value, in.On, in.Previous)
			message, output, err := mqttrelay.RunPayloadScriptWithin(ctx, in.Script, vars,
				time.Duration(in.TimeoutMS)*time.Millisecond)
			result.Output = output
			if err != nil {
				result.Error = err.Error()
			} else {
				// Show what would be sent, not the raw return, so the author
				// sees the null action for what it is.
				if message.Topic == "" {
					result.Value = map[string]any{"action": "none"}
				} else {
					sent := map[string]any{"topic": message.Topic, "payload": message.Payload}
					if message.QoS != nil {
						sent["qos"] = *message.QoS
					}
					if message.Retain != nil {
						sent["retain"] = *message.Retain
					}
					result.Value = sent
				}
			}
			out.Data = result
			return nil
		}

		values, output, err := mqttrelay.TestStateScriptWith(ctx, in.Script, in.Topic, in.Payload, in.State,
			in.Retained, time.Duration(in.TimeoutMS)*time.Millisecond)
		result.Output = output
		if err != nil {
			result.Error = err.Error()
		} else if values == nil {
			result.Value = map[string]any{"action": "ignored"}
		} else {
			result.Value = values
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Test an MQTT Device Script")
	u.SetDescription("Runs a device's state or payload script against a sample and returns its output and result.")
	u.SetTags("MQTT Devices")
	return u
}

// RegisterMQTTDeviceRoutes registers the device endpoints. Reading is gated on
// connection read, the same gate that covers the topics a device listens to;
// defining one is gated on connection write, since a device is part of how a
// connection is wired rather than a dashboard's own furniture.
func RegisterMQTTDeviceRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	// The device list is how a dashboard row finds the device it is bound to,
	// so it is optionally authenticated and filtered: connection read sees
	// every device, a dashboard viewer sees the ones on their dashboard.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodGet, "/mqtt_devices",
			nethttp.NewHandler(MQTTDeviceListHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices",
			nethttp.NewHandler(MQTTDeviceCreateHandler(), nethttp.SuccessStatus(http.StatusCreated)))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPut, "/mqtt_devices/{id}",
			nethttp.NewHandler(MQTTDeviceUpdateHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodDelete, "/mqtt_devices/{id}",
			nethttp.NewHandler(MQTTDeviceDeleteHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices/test",
			nethttp.NewHandler(MQTTScriptTestHandler()))
	// Commanding is the operate path, gated like any other outbound call on a
	// connection rather than like editing the device.
	s.With(middleware.OptionalAuth()).
		Method(http.MethodPost, "/mqtt_devices/{id}/command",
			nethttp.NewHandler(MQTTDeviceCommandHandler()))
	registerZigbee2MQTTRoutes(s, bearerDoc)
	registerESPHomeRoutes(s, bearerDoc)
	registerAutoImportRoutes(s, bearerDoc)
	registerEnergyRoutes(s, bearerDoc)
}
