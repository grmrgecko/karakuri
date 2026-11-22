package api

import (
	"context"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
)

// FaceplateFlowInput identifies the flow whose faceplates are read.
type FaceplateFlowInput struct {
	ID string `path:"id" format:"uuid" description:"Flow UUID."`
}

// FaceplatesHandler returns the interactor that lists a flow's faceplates.
func FaceplatesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FaceplateFlowInput, out *apiutil.Success[*flowops.FaceplateView]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessFaceplate)
		if err != nil {
			return err
		}
		view, err := flowops.Faceplates(ctx, callerID(ctx), f)
		if err != nil {
			return err
		}
		out.Data = view
		return nil
	})
	u.SetTitle("List Flow Faceplates")
	u.SetDescription("Returns a flow's faceplate tabs with their controls and applied values. Editors and viewers see the draft; a faceplate-level share sees the deployed version.")
	u.SetTags("Flows")
	return u
}

// FaceplateApplyInput carries the values to apply.
type FaceplateApplyInput struct {
	ID      string                    `path:"id" format:"uuid" description:"Flow UUID."`
	Values  map[string]map[string]any `json:"values" required:"true" description:"New values by faceplate key, then control ID; null returns a control to its default."`
	Comment string                    `json:"comment,omitempty" description:"Version comment."`
}

// FaceplateApplied reports the version an Apply deployed.
type FaceplateApplied struct {
	Version int `json:"version" description:"The deployed version number."`
}

// FaceplateApplyHandler returns the interactor that applies faceplate values.
func FaceplateApplyHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FaceplateApplyInput, out *apiutil.Success[FaceplateApplied]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessFaceplate)
		if err != nil {
			return err
		}
		version, err := flowops.ApplyFaceplates(ctx, callerID(ctx), f, flowops.FaceplateValues(in.Values), in.Comment)
		if err != nil {
			return err
		}
		out.Data = FaceplateApplied{Version: version}
		out.Message = "Applied"
		return nil
	})
	u.SetTitle("Apply Faceplate Values")
	u.SetDescription("Applies faceplate values and deploys. Edit access saves the draft with the values and deploys it; a faceplate-level share deploys the current version with only the new values.")
	u.SetTags("Flows")
	return u
}

// FaceplateOrderInput names the tab order.
type FaceplateOrderInput struct {
	ID   string   `path:"id" format:"uuid" description:"Flow UUID."`
	Keys []string `json:"keys" required:"true" description:"Faceplate keys in tab order."`
}

// FaceplateOrderHandler returns the interactor that reorders faceplate tabs.
func FaceplateOrderHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FaceplateOrderInput, out *apiutil.Success[any]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}
		if err := flowops.ReorderFaceplates(ctx, callerID(ctx), f, in.Keys); err != nil {
			return err
		}
		out.Message = "Tab order saved to the draft"
		return nil
	})
	u.SetTitle("Reorder Faceplate Tabs")
	u.SetDescription("Saves the faceplate tab order into the draft. Needs edit access.")
	u.SetTags("Flows")
	return u
}

// FaceplateReading is the live state of the entity one control holds.
type FaceplateReading struct {
	State     string `json:"state" description:"The entity's state."`
	Unit      string `json:"unit,omitempty" description:"Unit of measurement."`
	Name      string `json:"name,omitempty" description:"Friendly name."`
	Available bool   `json:"available" description:"Whether a reading could be had."`
}

// FaceplateReadingsHandler returns the interactor that reads the entities a
// flow's entity controls hold. The faceplate is the grant, as a dashboard's
// binding is: a faceplate-level share reads exactly those entities and no
// others, without connection permissions.
func FaceplateReadingsHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FaceplateFlowInput, out *apiutil.Success[map[string]map[string]FaceplateReading]) error {
		f, err := loadFlow(ctx, in.ID, models.FlowAccessFaceplate)
		if err != nil {
			return err
		}
		view, err := flowops.Faceplates(ctx, callerID(ctx), f)
		if err != nil {
			return err
		}
		states := map[string]map[string]FaceplateReading{}
		byConnection := map[string]map[string]FaceplateReading{}
		read := func(connectionID string) map[string]FaceplateReading {
			if cached, ok := byConnection[connectionID]; ok {
				return cached
			}
			found := map[string]FaceplateReading{}
			byConnection[connectionID] = found
			driver, err := connops.HADriver(connectionID)
			if err != nil {
				return found
			}
			list, err := driver.StatesCached(ctx)
			if err != nil {
				return found
			}
			for _, s := range list {
				r := FaceplateReading{State: s.State, Available: s.State != "unavailable" && s.State != "unknown"}
				if unit, ok := s.Attributes["unit_of_measurement"].(string); ok {
					r.Unit = unit
				}
				if name, ok := s.Attributes["friendly_name"].(string); ok {
					r.Name = name
				}
				found[s.EntityID] = r
			}
			return found
		}
		for _, tab := range view.Tabs {
			for _, c := range tab.Controls {
				if c.Type != flow.ControlEntity {
					continue
				}
				v, _ := tab.Values[c.ID].(map[string]any)
				if v == nil {
					v, _ = c.Default.(map[string]any)
				}
				conn, _ := v["connection_id"].(string)
				entity, _ := v["entity_id"].(string)
				if conn == "" || entity == "" {
					continue
				}
				if states[tab.Key] == nil {
					states[tab.Key] = map[string]FaceplateReading{}
				}
				states[tab.Key][c.ID] = read(conn)[entity]
			}
		}
		out.Data = states
		return nil
	})
	u.SetTitle("Read Faceplate Entities")
	u.SetDescription("Returns the live state of every entity a flow's entity controls hold, by faceplate key and control ID.")
	u.SetTags("Flows")
	return u
}

// RegisterFaceplateRoutes registers the faceplate endpoints. A faceplate-level
// share holds no global flow permission, so the per-flow level is the check.
func RegisterFaceplateRoutes(s *web.Service, bearerDoc func(http.Handler) http.Handler) {
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/faceplates",
			nethttp.NewHandler(FaceplatesHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/faceplates/apply",
			nethttp.NewHandler(FaceplateApplyHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodPost, "/flows/{id}/faceplates/order",
			nethttp.NewHandler(FaceplateOrderHandler()))
	s.With(middleware.Auth(), bearerDoc).
		Method(http.MethodGet, "/flows/{id}/faceplates/readings",
			nethttp.NewHandler(FaceplateReadingsHandler()))
}
