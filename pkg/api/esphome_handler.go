package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/entitydevices"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// esphomeBrowseTime is how long a node search listens for answers.
const esphomeBrowseTime = 3 * time.Second

// ESPHomeNodesOutput is the nodes found on the network.
type ESPHomeNodesOutput struct {
	Nodes []esphome.FoundNode `json:"nodes" description:"ESPHome nodes announcing themselves on the server's network."`
}

// ESPHomeNodesHandler returns the interactor that searches the network for
// ESPHome nodes.
func ESPHomeNodesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, _ struct{}, out *apiutil.Success[ESPHomeNodesOutput]) error {
		browse, cancel := context.WithTimeout(ctx, esphomeBrowseTime)
		defer cancel()
		nodes, err := esphome.Browse(browse)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("searching the network: " + err.Error())
		}
		if nodes == nil {
			nodes = []esphome.FoundNode{}
		}
		out.Data = ESPHomeNodesOutput{Nodes: nodes}
		return nil
	})
	u.SetTitle("Find ESPHome Nodes")
	u.SetDescription("Lists ESPHome nodes answering an mDNS query on the server's network for three seconds.")
	u.SetTags("Connections")
	return u
}

// ESPHomeEntitiesInput names the node to list.
type ESPHomeEntitiesInput struct {
	ConnectionID string `query:"connection_id" required:"true" description:"ESPHome connection of the node."`
}

// ESPHomeEntitiesOutput is a node's entities as the add dialog lists them.
type ESPHomeEntitiesOutput struct {
	Node     esphome.NodeInfo           `json:"node" description:"What the node says about itself."`
	Entities []entitydevices.Discovered `json:"entities" description:"The node's entities with the device each would become."`
}

// ESPHomeEntitiesHandler returns the interactor listing a node's entities.
func ESPHomeEntitiesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ESPHomeEntitiesInput, out *apiutil.Success[ESPHomeEntitiesOutput]) error {
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		driver, err := connops.ESPHomeDriver(in.ConnectionID)
		if err != nil {
			return err
		}
		if driver.Status().State != "connected" {
			return apperrors.ErrBadGateway.WithMessage("the ESPHome node is not connected: " + driver.Status().Error)
		}
		existing, err := models.ListMQTTDevices()
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		out.Data = ESPHomeEntitiesOutput{Node: driver.Info(), Entities: entitydevices.Discover(entitydevices.ESPHome(driver), existing, connID)}
		return nil
	})
	u.SetTitle("List ESPHome Entities")
	u.SetDescription("Lists a connected node's entities with the device kind each would become and whether it is added.")
	u.SetTags("MQTT Devices")
	return u
}

// ESPHomeAddInput names the entities to add.
type ESPHomeAddInput struct {
	ConnectionID string   `json:"connection_id" required:"true" description:"ESPHome connection of the node."`
	ObjectIDs    []string `json:"object_ids" required:"true" minItems:"1" description:"Object IDs of the entities to add."`
}

// ESPHomeAddHandler returns the interactor that adds entities as devices.
func ESPHomeAddHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ESPHomeAddInput, out *apiutil.Success[entitydevices.AddResult]) error {
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		driver, err := connops.ESPHomeDriver(in.ConnectionID)
		if err != nil {
			return err
		}
		result, err := entitydevices.Add(entitydevices.ESPHome(driver), connID, in.ObjectIDs)
		if err != nil {
			return err
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Add ESPHome Entities")
	u.SetDescription("Creates a device for each listed entity of a connected ESPHome node.")
	u.SetTags("MQTT Devices")
	return u
}

// EntitiesInput names the connection whose entities to list.
type EntitiesInput struct {
	ConnectionID string `query:"connection_id" required:"true" description:"ESPHome or Frigate connection."`
}

// EntitiesOutput is a connection's entities as the add dialog lists them.
type EntitiesOutput struct {
	Transport string                     `json:"transport" description:"Transport the devices would use: esphome or frigate."`
	Entities  []entitydevices.Discovered `json:"entities" description:"The connection's entities with the device each would become."`
}

// EntitiesHandler returns the interactor listing any entity connection's
// entities.
func EntitiesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in EntitiesInput, out *apiutil.Success[EntitiesOutput]) error {
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		src, err := entitydevices.SourceFor(in.ConnectionID)
		if err != nil {
			return err
		}
		if !src.Connected() {
			return apperrors.ErrBadGateway.WithMessage("the connection is not connected")
		}
		existing, err := models.ListMQTTDevices()
		if err != nil {
			return apperrors.ErrInternal.WithMessage(err.Error())
		}
		out.Data = EntitiesOutput{Transport: src.Transport(), Entities: entitydevices.Discover(src, existing, connID)}
		return nil
	})
	u.SetTitle("List Connection Entities")
	u.SetDescription("Lists an ESPHome node's entities or a Frigate server's camera switches with the device kind each would become and whether it is added.")
	u.SetTags("MQTT Devices")
	return u
}

// EntitiesAddHandler returns the interactor that adds any entity
// connection's entities as devices.
func EntitiesAddHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ESPHomeAddInput, out *apiutil.Success[entitydevices.AddResult]) error {
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid connection id")
		}
		if err := useConnectionID(ctx, in.ConnectionID); err != nil {
			return err
		}
		src, err := entitydevices.SourceFor(in.ConnectionID)
		if err != nil {
			return err
		}
		result, err := entitydevices.Add(src, connID, in.ObjectIDs)
		if err != nil {
			return err
		}
		out.Data = result
		return nil
	})
	u.SetTitle("Add Connection Entities")
	u.SetDescription("Creates a device for each listed entity of a connected ESPHome node or Frigate server.")
	u.SetTags("MQTT Devices")
	return u
}

// ESPHomeActionInput is one call of a node's action.
type ESPHomeActionInput struct {
	ID     string         `path:"id" description:"ESPHome connection UUID."`
	Action string         `path:"action" description:"Action name from the node's api: actions:."`
	Args   map[string]any `json:"args,omitempty" description:"Arguments by name, checked against the action's declared types."`
}

// ESPHomeActionHandler returns the interactor that calls a node's action.
func ESPHomeActionHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ESPHomeActionInput, out *apiutil.Success[struct{}]) error {
		driver, err := connops.ESPHomeDriver(in.ID)
		if err != nil {
			return err
		}
		args := in.Args
		if args == nil {
			args = map[string]any{}
		}
		if err := driver.CallAction(ctx, in.Action, args); err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}
		out.Message = "Action called"
		return nil
	})
	u.SetTitle("Call ESPHome Action")
	u.SetDescription("Runs one of an ESPHome node's user-defined actions.")
	u.SetTags("Connections")
	return u
}

// registerESPHomeRoutes registers node search, entity listing and adding for
// ESPHome and Frigate, and action calls.
func registerESPHomeRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodGet, "/connections/esphome/nodes",
			nethttp.NewHandler(ESPHomeNodesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodGet, "/mqtt_devices/esphome",
			nethttp.NewHandler(ESPHomeEntitiesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices/esphome",
			nethttp.NewHandler(ESPHomeAddHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodGet, "/mqtt_devices/entities",
			nethttp.NewHandler(EntitiesHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsWrite), bearerDoc).
		Method(http.MethodPost, "/mqtt_devices/entities",
			nethttp.NewHandler(EntitiesAddHandler()))
	s.With(middleware.Auth(), middleware.RequirePermission(perm.PermConnectionsInvoke), requireConnection(access.Use), bearerDoc).
		Method(http.MethodPost, "/connections/{id}/esphome/actions/{action}",
			nethttp.NewHandler(ESPHomeActionHandler()))
}
