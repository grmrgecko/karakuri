package api

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/swaggest/usecase"
)

// HAEntity is one Home Assistant entity offered to the editor's entity
// autocomplete.
type HAEntity struct {
	EntityID     string `json:"entity_id" description:"Entity identifier, e.g. light.kitchen."`
	FriendlyName string `json:"friendly_name,omitempty" description:"Display name from the entity's attributes."`
	State        string `json:"state" description:"Current state value."`
	homeassistant.EntityInfo
}

// HAEntitiesInput selects a connection and whether hidden entities are listed.
type HAEntitiesInput struct {
	ID     string `path:"id" description:"Connection UUID."`
	Hidden bool   `query:"hidden" description:"Include entities Home Assistant marks hidden."`
}

// HAEntitiesHandler returns the interactor that lists the entities of a
// running Home Assistant connection for pickers: each with its area, floor,
// device, and labels, ordered by floor, area, device, and name so a picker
// can group them, with entities in no area last. The driver caches states
// briefly so repeated picker lookups stay snappy.
func HAEntitiesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HAEntitiesInput, out *apiutil.Success[[]HAEntity]) error {
		driver, err := connops.HADriver(in.ID)
		if err != nil {
			return err
		}

		states, err := driver.StatesCached(ctx)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("Home Assistant is not connected: " + err.Error())
		}
		reg := driver.Registry()

		entities := make([]HAEntity, 0, len(states))
		for _, s := range states {
			e := HAEntity{EntityID: s.EntityID, State: s.State, EntityInfo: reg.Describe(s.EntityID)}
			if e.Hidden && !in.Hidden {
				continue
			}
			if name, ok := s.Attributes["friendly_name"].(string); ok {
				e.FriendlyName = name
			}
			entities = append(entities, e)
		}
		sortEntities(reg, entities)
		out.Data = entities
		return nil
	})
	u.SetTitle("List Home Assistant Entities")
	u.SetDescription("Returns the entities of a running Home Assistant connection for pickers, with area, floor, device, and labels.")
	u.SetTags("Connections")
	return u
}

// sortEntities orders entities by floor level, area, device, and name, with
// entities in no area after every area.
func sortEntities(reg *homeassistant.Registry, list []HAEntity) {
	name := func(e HAEntity) string {
		if e.FriendlyName != "" {
			return strings.ToLower(e.FriendlyName)
		}
		return e.EntityID
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.AreaID == "") != (b.AreaID == "") {
			return a.AreaID != ""
		}
		la, fa := reg.FloorOrder(a.FloorID)
		lb, fb := reg.FloorOrder(b.FloorID)
		if la != lb {
			return la < lb
		}
		if fa != fb {
			return fa < fb
		}
		if a.Area != b.Area {
			return strings.ToLower(a.Area) < strings.ToLower(b.Area)
		}
		if a.Device != b.Device {
			return strings.ToLower(a.Device) < strings.ToLower(b.Device)
		}
		return name(a) < name(b)
	})
}

// HARegistry is a connection's floors, areas, devices, and labels, for target
// pickers, with whether each registry could be read.
type HARegistry struct {
	Floors  []homeassistant.Floor  `json:"floors"`
	Areas   []homeassistant.Area   `json:"areas"`
	Devices []homeassistant.Device `json:"devices"`
	Labels  []homeassistant.Label  `json:"labels"`
	// Status maps each registry to "loaded" or the reason it is empty.
	Status map[string]string `json:"status"`
}

// RegistryView builds the registry answer shared by REST and MCP.
func RegistryView(reg *homeassistant.Registry) HARegistry {
	out := HARegistry{Floors: reg.Floors, Areas: reg.Areas, Devices: reg.Devices, Labels: reg.Labels, Status: reg.Status}
	if out.Floors == nil {
		out.Floors = []homeassistant.Floor{}
	}
	if out.Areas == nil {
		out.Areas = []homeassistant.Area{}
	}
	if out.Devices == nil {
		out.Devices = []homeassistant.Device{}
	}
	if out.Labels == nil {
		out.Labels = []homeassistant.Label{}
	}
	return out
}

// HARegistryHandler returns the interactor that lists a connection's floors,
// areas, devices, and labels.
func HARegistryHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[HARegistry]) error {
		driver, err := connops.HADriver(in.ID)
		if err != nil {
			return err
		}
		out.Data = RegistryView(driver.Registry())
		return nil
	})
	u.SetTitle("List Home Assistant Registries")
	u.SetDescription("Returns the floors, areas, devices, and labels of a running Home Assistant connection, and whether each could be read.")
	u.SetTags("Connections")
	return u
}

// HAState is a full entity state including attributes, for dashboard widgets
// that render more than the bare value.
type HAState struct {
	EntityID    string         `json:"entity_id" description:"Entity identifier, e.g. light.kitchen."`
	State       string         `json:"state" description:"Current state value."`
	Attributes  map[string]any `json:"attributes,omitempty" description:"Entity attributes."`
	LastChanged string         `json:"last_changed,omitempty" description:"Timestamp of the last state change."`
}

// HAStatesHandler returns the interactor that lists full entity states of a
// running Home Assistant connection. Dashboard widgets use this for their
// initial render before live updates arrive over the WebSocket.
func HAStatesHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in ConnectionGetInput, out *apiutil.Success[[]HAState]) error {
		// Decide what this caller may see before reading anything. A caller
		// holding connection read sees the connection; a dashboard viewer sees
		// only the entities that dashboard's widgets bind to, because the
		// alternative is handing an anonymous viewer the inventory of the
		// house to get at the one light on the page.
		allowed, err := haVisibleEntities(ctx, in.ID)
		if err != nil {
			return err
		}

		driver, err := connops.HADriver(in.ID)
		if err != nil {
			return err
		}

		states, err := driver.StatesCached(ctx)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("Home Assistant is not connected: " + err.Error())
		}

		items := make([]HAState, 0, len(states))
		for _, s := range states {
			if allowed != nil && !allowed[s.EntityID] {
				continue
			}
			items = append(items, HAState{
				EntityID:    s.EntityID,
				State:       s.State,
				Attributes:  s.Attributes,
				LastChanged: s.LastChanged,
			})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].EntityID < items[j].EntityID })
		out.Data = items
		return nil
	})
	u.SetTitle("List Home Assistant States")
	u.SetDescription("Returns full entity states with attributes for dashboard widgets.")
	u.SetTags("Connections")
	return u
}

// HACallServiceInput names a Home Assistant service to invoke and its payload.
type HACallServiceInput struct {
	ID          string         `path:"id" description:"Connection UUID."`
	Domain      string         `json:"domain" required:"true" description:"Service domain, e.g. light."`
	Service     string         `json:"service" required:"true" description:"Service name, e.g. turn_on."`
	ServiceData map[string]any `json:"service_data,omitempty" description:"Service call data."`
	Target      map[string]any `json:"target,omitempty" description:"Service call target, e.g. {entity_id: light.kitchen}."`
}

// HACallServiceHandler returns the interactor that invokes a Home Assistant
// service. This is the control path for dashboard widgets: toggling lights,
// activating scenes, and any other service the connection can reach.
func HACallServiceHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in HACallServiceInput, out *apiutil.Success[any]) error {
		if err := authorizeServiceCall(ctx, in); err != nil {
			return err
		}

		driver, err := connops.HADriver(in.ID)
		if err != nil {
			return err
		}

		result, err := driver.CallService(ctx, in.Domain, in.Service, in.ServiceData, in.Target)
		if err != nil {
			return apperrors.ErrBadGateway.WithMessage("service call failed: " + err.Error())
		}

		out.Data = result
		out.Message = "Service called"
		return nil
	})
	u.SetTitle("Call Home Assistant Service")
	u.SetDescription("Invokes a Home Assistant service, the control path for dashboard widgets.")
	u.SetTags("Connections")
	return u
}

// canInvokeConnection reports whether the caller holds the permission that
// covers outbound calls on a connection and may use this one.
func canInvokeConnection(ctx context.Context, connID string) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && auth.Can(ctx, uc.UserID, perm.PermConnectionsInvoke) &&
		access.CanUseConnection(ctx, uc.UserID, connID)
}

// canReadConnection reports whether the caller holds connection read and may
// use this connection.
func canReadConnection(ctx context.Context, connID string) bool {
	uc := middleware.GetUser(ctx)
	return uc != nil && auth.Can(ctx, uc.UserID, perm.PermConnectionsRead) &&
		access.CanUseConnection(ctx, uc.UserID, connID)
}

// haVisibleEntities returns the entity IDs a caller may see on a connection.
// A nil map means every entity, which is what connection read grants; a
// non-nil map is a dashboard viewer's grant and lists exactly what the
// dashboard's widgets bind to on this connection.
func haVisibleEntities(ctx context.Context, connID string) (map[string]bool, error) {
	if canReadConnection(ctx, connID) {
		return nil, nil
	}
	scope := dashscope.FromContext(ctx)
	if scope == nil {
		return nil, unauthorizedOrForbidden(ctx)
	}
	allowed := make(map[string]bool)
	for entity := range scope.Refs(dashscope.CapHA) {
		if scope.Allows(dashscope.Capability{Kind: dashscope.CapHA, Owner: canonicalConnID(connID), Ref: entity}) {
			allowed[entity] = true
		}
	}
	if len(allowed) == 0 {
		return nil, apperrors.ErrForbidden.WithMessage("this dashboard shows nothing on that connection")
	}
	return allowed, nil
}

// authorizeServiceCall admits a service call from a dashboard viewer only when
// every entity it targets is one the dashboard binds to. A call naming no
// entity is refused outright: "turn off the lights" with no target turns off
// every light, which no widget on a dashboard is asking for.
func authorizeServiceCall(ctx context.Context, in HACallServiceInput) error {
	if canInvokeConnection(ctx, in.ID) {
		return nil
	}
	scope := dashscope.FromContext(ctx)
	if scope == nil {
		return unauthorizedOrForbidden(ctx)
	}
	if !scope.Control {
		return apperrors.ErrForbidden.WithMessage("this dashboard is read-only")
	}

	// Home Assistant addresses a service call by entity, device, area, floor,
	// or label, and it acts on the union of them. Checking the entities alone
	// would let a call name a granted entity and an area beside it and reach
	// the whole area, so a dashboard call may address by entity and nothing
	// else.
	if key, found := otherTargeting(in); found {
		return apperrors.ErrForbidden.WithMessage(
			"a service call from a dashboard addresses entities, not " + key)
	}

	targets := serviceTargets(in)
	if len(targets) == 0 {
		return apperrors.ErrForbidden.WithMessage("a service call from a dashboard has to name the entity it acts on")
	}
	conn := canonicalConnID(in.ID)
	for _, entity := range targets {
		if !scope.Allows(dashscope.Capability{Kind: dashscope.CapHA, Owner: conn, Ref: entity, Write: true}) {
			return apperrors.ErrForbidden.WithMessage("this dashboard does not control " + entity)
		}
		// A service of another domain ignores the entity or acts on it in a
		// way no widget offers: script.<name>, shell_command, and
		// rest_command run whatever they are with entity_id as data, and
		// recorder.purge_entities deletes its history.
		if domain, _, _ := strings.Cut(entity, "."); domain != in.Domain {
			return apperrors.ErrForbidden.WithMessage("a service call from a dashboard has to be in the domain of " + entity)
		}
	}
	// Home Assistant registers every script as a service of its own,
	// script.<object_id>, which runs that script whatever entity the call
	// names. Bound scripts are started through turn_on.
	if in.Domain == "script" && !scriptEntityServices[in.Service] {
		return apperrors.ErrForbidden.WithMessage("a dashboard starts a script with script.turn_on, not script." + in.Service)
	}
	return nil
}

// scriptEntityServices are the script services that act on the entities a
// call targets.
var scriptEntityServices = map[string]bool{"turn_on": true, "turn_off": true, "toggle": true}

// haGroupTargets are the ways a Home Assistant service call addresses more
// than one entity at a time. None of them is checkable against a dashboard's
// bindings: an area holds whatever Home Assistant has put in it, and that
// membership changes there rather than here.
var haGroupTargets = []string{"device_id", "area_id", "floor_id", "label_id"}

// otherTargeting reports the first group-addressing key a call carries, in
// either place Home Assistant reads targeting from.
func otherTargeting(in HACallServiceInput) (string, bool) {
	for _, m := range []map[string]any{in.Target, in.ServiceData} {
		if m == nil {
			continue
		}
		for _, key := range haGroupTargets {
			if v, ok := m[key]; ok && v != nil {
				return key, true
			}
		}
	}
	return "", false
}

// serviceTargets collects every entity a service call names, from either place
// Home Assistant accepts one.
func serviceTargets(in HACallServiceInput) []string {
	var out []string
	collect := func(v any) {
		switch t := v.(type) {
		case string:
			if t != "" {
				out = append(out, t)
			}
		case []any:
			for _, item := range t {
				if s, ok := item.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	if in.Target != nil {
		collect(in.Target["entity_id"])
	}
	if in.ServiceData != nil {
		collect(in.ServiceData["entity_id"])
	}
	return out
}

// canonicalConnID normalizes a path parameter to the form a widget's config
// stores, so a capability compares equal however the URL was written.
func canonicalConnID(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}

// unauthorizedOrForbidden distinguishes "sign in" from "you may not", so an
// anonymous caller is told to authenticate rather than that the thing does not
// exist for them.
func unauthorizedOrForbidden(ctx context.Context) error {
	if middleware.GetUser(ctx) == nil {
		return apperrors.ErrUnauthorized
	}
	return apperrors.ErrForbidden
}
