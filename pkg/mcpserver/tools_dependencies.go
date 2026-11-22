package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/agentops"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/dashscope"
	"github.com/grmrgecko/karakuri/pkg/dashvars"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

type documentDependency struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Owner  string `json:"owner,omitempty"`
	Path   string `json:"path"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type dependencyReport struct {
	Dependencies []documentDependency `json:"dependencies"`
	Truncated    bool                 `json:"truncated"`
}

type dependencyCollector struct {
	referencesOnly bool
	ctx            context.Context
	report         dependencyReport
	permissions    map[string]bool
	expanded       map[string]bool
	entities       map[string]map[string]string
	entityErrors   map[string]error
}

func newDependencyCollector(ctx context.Context) *dependencyCollector {
	return &dependencyCollector{ctx: ctx, report: dependencyReport{Dependencies: []documentDependency{}}, permissions: map[string]bool{}, expanded: map[string]bool{}, entities: map[string]map[string]string{}, entityErrors: map[string]error{}}
}

func addDependencyTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "get_document_dependencies", Description: "Inspect a flow or dashboard draft (or deployment), including nested shared subflows, controls, connections, devices, entities, and agents. Reports missing, unavailable, dynamic, and permission-restricted references without executing actions."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Kind string `json:"kind" jsonschema:"flow or dashboard."`
			ID   string `json:"id" jsonschema:"Document UUID; dashboards also accept a slug."`
			View string `json:"view,omitempty" jsonschema:"draft (default) or deployed."`
		}) (*mcp.CallToolResult, any, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if in.View == "" {
				in.View = "draft"
			}
			if in.View != "draft" && in.View != "deployed" {
				return nil, nil, fmt.Errorf("view must be draft or deployed")
			}
			c := newDependencyCollector(ctx)
			switch in.Kind {
			case "flow":
				if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
					return nil, nil, err
				}
				f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
				if err != nil {
					return nil, nil, err
				}
				if err := c.flowDocument(f, in.View, "flow/"+f.ID.String(), 0); err != nil {
					return nil, nil, err
				}
			case "dashboard":
				d, err := editableDashboard(ctx, in.ID)
				if err != nil {
					return nil, nil, err
				}
				snap, err := dashboardSnapshot(d, in.View)
				if err != nil {
					return nil, nil, err
				}
				if snap == nil {
					return nil, nil, fmt.Errorf("dashboard has no deployment")
				}
				rows, err := snap.WidgetRows(d.ID)
				if err != nil {
					return nil, nil, err
				}
				c.dashboard(rows, snap.Display)
			default:
				return nil, nil, fmt.Errorf("kind must be flow or dashboard")
			}
			return nil, c.report, nil
		})
}

func (c *dependencyCollector) allowed(permission string) bool {
	allowed, ok := c.permissions[permission]
	if !ok {
		allowed = requirePermission(c.ctx, permission) == nil
		c.permissions[permission] = allowed
	}
	return allowed
}

func (c *dependencyCollector) add(d documentDependency) {
	if len(c.report.Dependencies) >= 2000 {
		c.report.Truncated = true
		return
	}
	c.report.Dependencies = append(c.report.Dependencies, d)
}

func (c *dependencyCollector) reference(kind, ref, owner, path string) documentDependency {
	d := documentDependency{Kind: kind, Ref: ref, Owner: owner, Path: path, Status: "available"}
	if strings.Contains(ref, "{{") || strings.Contains(owner, "{{") {
		d.Status = "dynamic"
		d.Detail = "reference is evaluated at runtime"
		return d
	}
	if strings.TrimSpace(ref) == "" {
		d.Status = "missing"
		d.Detail = "reference is not configured"
		return d
	}
	permission := map[string]string{"connection": perm.PermConnectionsRead, "mqtt_device": perm.PermConnectionsRead, "ha_entity": perm.PermConnectionsRead, "control": perm.PermControlsRead, "flow": perm.PermFlowsRead, "agent": perm.PermAgentsRead}[kind]
	if permission != "" && !c.allowed(permission) {
		d.Status = "unchecked"
		d.Detail = "requires " + permission
		return d
	}
	// A connection, device, entity, or agent the caller cannot use is reported
	// without its name or status, whether or not it exists.
	if referenceAccess(c.ctx, kind, ref, owner) != nil {
		d.Status = "unchecked"
		d.Detail = "not shared with you"
		return d
	}
	var err error
	switch kind {
	case "connection":
		var row models.Connection
		err = db.DB.Where("id = ?", ref).First(&row).Error
		if err == nil {
			d.Name = row.Name
			if !row.Enabled {
				d.Status = "unavailable"
				d.Detail = "connection is disabled"
			} else if mgr := connops.Manager(); mgr != nil {
				state := mgr.Statuses()[row.ID].State
				if state != integrations.StateConnected {
					d.Status = "unavailable"
					d.Detail = "connection is not connected"
				}
			} else {
				d.Status = "unchecked"
				d.Detail = "connection manager is unavailable"
			}
		}
	case "control":
		var row models.Control
		if _, parseErr := uuid.Parse(ref); parseErr == nil {
			err = db.DB.Where("id = ?", ref).First(&row).Error
		} else {
			err = db.DB.Where("name = ?", ref).First(&row).Error
		}
		d.Name = row.Name
	case "mqtt_device":
		var row models.MQTTDevice
		err = db.DB.Where("id = ?", ref).First(&row).Error
		d.Name = row.Name
		if err == nil {
			c.add(c.reference("connection", row.ConnectionID.String(), "", path+"/connection"))
		}
	case "agent":
		var row models.Agent
		err = db.DB.Where("id = ?", ref).First(&row).Error
		d.Name = row.Name
		if err == nil {
			status := agentops.Describe(&row)
			if !status.Enabled || !status.Connected {
				d.Status = "unavailable"
				d.Detail = "agent is disabled or disconnected"
			}
		}
	case "flow":
		// A flow the caller may not read is reported without its name or
		// contents, whether or not it exists.
		var row models.Flow
		err = db.DB.Where("id = ?", ref).First(&row).Error
		if err == nil && !flowops.Holds(c.ctx, callerID(c.ctx), &row, models.FlowAccessView) {
			d.Status = "unchecked"
			d.Detail = "flow is not shared with you"
			return d
		}
		d.Name = row.Name
		if err == nil && row.DeployedVersionID == nil {
			d.Status = "unavailable"
			d.Detail = "flow has no deployed version"
		}
	case "ha_entity":
		if c.referencesOnly {
			return d
		}
		if _, loaded := c.entities[owner]; !loaded {
			c.entities[owner] = map[string]string{}
			driver, e := connops.HADriver(owner)
			if e == nil {
				states, getErr := driver.StatesCached(c.ctx)
				e = getErr
				for _, state := range states {
					c.entities[owner][state.EntityID] = state.State
				}
			}
			c.entityErrors[owner] = e
		}
		if c.entityErrors[owner] != nil {
			d.Status = "unchecked"
			d.Detail = "Home Assistant states are unavailable"
		} else if state, ok := c.entities[owner][ref]; !ok {
			d.Status = "missing"
			d.Detail = "entity not found"
		} else if state == "unavailable" || state == "unknown" {
			d.Status = "unavailable"
			d.Detail = "entity state is " + state
		}
	}
	if err != nil {
		d.Name = ""
		if errors.Is(err, gorm.ErrRecordNotFound) {
			d.Status = "missing"
			d.Detail = "referenced object not found"
		} else {
			d.Status = "unchecked"
			d.Detail = "could not read referenced object"
		}
	}
	return d
}

func (c *dependencyCollector) flowDocument(f *models.Flow, view, path string, depth int) error {
	if depth > 32 {
		c.report.Truncated = true
		return nil
	}
	key := f.ID.String() + "/" + view
	if c.expanded[key] {
		return nil
	}
	c.expanded[key] = true
	var version *models.FlowVersion
	var err error
	if view == "draft" {
		version, err = flowops.LoadDraft(f)
	} else if f.DeployedVersionID != nil {
		version, err = models.FindFlowVersionByID(*f.DeployedVersionID)
	} else {
		return fmt.Errorf("flow has no deployment")
	}
	if err != nil {
		return err
	}
	settings := *f
	if view != "draft" {
		version.ApplySettings(&settings)
	}
	if settings.ErrorFlowID != nil {
		c.sharedFlow(settings.ErrorFlowID.String(), path+"/error_flow_id", depth+1)
	}
	g, err := flowops.ParseGraph(version.Graph)
	if err != nil {
		return err
	}
	c.graph(g, path+"/graph", depth, g.Subgraphs)
	return nil
}

func (c *dependencyCollector) sharedFlow(id, path string, depth int) {
	d := c.reference("flow", id, "", path)
	c.add(d)
	if d.Status == "available" {
		f, err := flowops.Load(id)
		if err == nil {
			if err = c.flowDocument(f, "deployed", path+"/deployed", depth); err != nil {
				c.add(documentDependency{Kind: "flow", Ref: id, Path: path, Status: "unchecked", Detail: "could not inspect deployed graph"})
			}
		}
	}
}

func (c *dependencyCollector) graph(g *flow.Graph, path string, depth int, locals map[string]flow.Graph) {
	if depth > 32 || len(c.report.Dependencies) >= 2000 {
		c.report.Truncated = true
		return
	}
	for _, n := range g.Nodes {
		p := path + "/nodes/" + n.ID
		if n.SubflowID != nil {
			c.sharedFlow(n.SubflowID.String(), p+"/subflow_id", depth+1)
		}
		if n.SubgraphID != "" {
			if _, ok := locals[n.SubgraphID]; !ok {
				c.add(documentDependency{Kind: "local_subgraph", Ref: n.SubgraphID, Path: p, Status: "missing"})
			}
		}
		if parts := strings.SplitN(n.Type, ".", 3); len(parts) == 3 && parts[0] == "agent" {
			c.add(c.reference("agent", parts[1], "", p+"/type"))
		}
		meta, ok := engine.Default().MetaFor(n.Type)
		if !ok {
			c.add(documentDependency{Kind: "node_type", Ref: n.Type, Path: p, Status: "missing", Detail: "node type is not registered"})
			continue
		}
		var cfg map[string]any
		_ = json.Unmarshal(n.Config, &cfg)
		for _, field := range meta.ConfigSpec {
			kind := ""
			switch {
			case field.Type == flow.FieldConnection:
				kind = "connection"
			case field.Type == flow.FieldEntity:
				kind = "ha_entity"
			case field.Control == "control-picker", field.Control == "control-list":
				kind = "control"
			case field.Control == "mqtt-device":
				kind = "mqtt_device"
			}
			if kind == "" {
				continue
			}
			ref, _ := cfg[field.ID].(string)
			dynamic := false
			for _, edge := range g.Edges {
				if edge.IsValue() && edge.Target == n.ID && edge.TargetHandle == "param:"+field.ID {
					dynamic = true
					break
				}
			}
			if dynamic {
				c.add(documentDependency{Kind: kind, Path: p + "/config/" + field.ID, Status: "dynamic", Detail: "reference is supplied through a value edge"})
			} else if ref != "" || field.Required {
				if kind == "ha_entity" {
					owner, _ := cfg["connection"].(string)
					ownerDynamic := false
					for _, edge := range g.Edges {
						if edge.IsValue() && edge.Target == n.ID && edge.TargetHandle == "param:connection" {
							ownerDynamic = true
							break
						}
					}
					if ownerDynamic {
						c.add(documentDependency{Kind: kind, Ref: ref, Path: p + "/config/" + field.ID, Status: "dynamic", Detail: "connection is supplied through a value edge"})
					} else if strings.Contains(ref, "{{") || strings.TrimSpace(ref) == "" {
						c.add(c.reference(kind, ref, owner, p+"/config/"+field.ID))
					} else {
						for _, entity := range strings.Split(ref, ",") {
							if entity = strings.TrimSpace(entity); entity != "" {
								c.add(c.reference(kind, entity, owner, p+"/config/"+field.ID))
							}
						}
					}
				} else if field.Control == "control-list" {
					for _, item := range strings.Split(ref, ",") {
						if item = strings.TrimSpace(item); item != "" {
							c.add(c.reference(kind, item, "", p+"/config/"+field.ID))
						}
					}
				} else {
					c.add(c.reference(kind, ref, "", p+"/config/"+field.ID))
				}
			}
		}
	}
	keys := make([]string, 0, len(g.Subgraphs))
	for key := range g.Subgraphs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		sub := g.Subgraphs[key]
		c.graph(&sub, path+"/subgraphs/"+key, depth+1, locals)
	}
}

// dashboard reports a layout's bindings and condition sources. A placement
// templated on variables is reported once per combination of option values, the
// same expansion dashboard scope grants, with the values in the path.
func (c *dependencyCollector) dashboard(rows []models.DashboardWidget, display *models.DashboardDisplay) {
	var variables []models.DashboardVariable
	if display != nil {
		for i, v := range display.Variables {
			if v.Kind() != models.VariableTypeQuery || v.Query == nil {
				continue
			}
			p := fmt.Sprintf("display/variables/%d/query", i)
			c.add(c.reference("connection", v.Query.ConnectionID, "", p))
			if v.Query.Source == models.QueryHASelect {
				c.add(c.reference("ha_entity", v.Query.EntityID, v.Query.ConnectionID, p))
			}
		}
		variables, _ = dashvars.Resolve(display.Variables)
	}
	for _, w := range rows {
		path := "widgets/" + w.ID.String()
		spec, _, ok := widgets.ScopeOf(w.Widget.PackageName)
		if !ok {
			c.add(documentDependency{Kind: "widget_package", Ref: w.Widget.PackageName, Path: path, Status: "unavailable", Detail: "widget package is not loaded"})
			continue
		}
		combos, ok := dashscope.VariableCombinations(variables, dashscope.PlacementReferences(w))
		if !ok {
			c.add(documentDependency{Kind: "binding", Path: path, Status: "invalid", Detail: "variable combinations exceed the limit, so the placement grants none of its bindings"})
			continue
		}
		// Every combination is checked, but a dependency is reported once per
		// placement: variables that do not change a binding would otherwise
		// repeat it once for each of their values.
		first := len(c.report.Dependencies)
		for _, values := range combos {
			suffix := variableSuffix(values, variables)
			bindings, err := dashscope.ParseBindings(dashscope.SubstituteConfig(w.Config, values), spec.Bindings)
			if err != nil {
				c.add(documentDependency{Kind: "binding", Path: path + suffix, Status: "invalid", Detail: "binding must be an object or array of objects"})
				continue
			}
			for i, b := range bindings {
				c.dashboardBinding(b, fmt.Sprintf("%s/bindings/%d%s", path, i, suffix))
			}
			display := dashscope.SubstituteDisplay(w.Display, values)
			paths := conditionPaths(display)
			for i, b := range dashscope.ConditionBindings(display) {
				p := path + "/" + paths[i] + suffix
				switch b.Source {
				case dashscope.SourceControl:
					c.add(c.reference("control", b.ControlID, "", p))
				case dashscope.SourceMQTT:
					c.add(c.reference("mqtt_device", b.DeviceID, "", p))
				case dashscope.SourceHA:
					c.add(c.reference("connection", b.ConnectionID, "", p))
					c.add(c.reference("ha_entity", b.EntityID, b.ConnectionID, p))
				}
			}
		}
		c.report.Dependencies = append(c.report.Dependencies[:first], dedupeDependencies(c.report.Dependencies[first:])...)
	}
}

// dedupeDependencies keeps the first report of each dependency.
func dedupeDependencies(deps []documentDependency) []documentDependency {
	seen := map[string]bool{}
	out := make([]documentDependency, 0, len(deps))
	for _, d := range deps {
		key := d.Kind + "\x00" + d.Ref + "\x00" + d.Owner + "\x00" + d.Status
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// variableSuffix names the variable values a dependency was found under, as
// [name=value,...] by name, or nothing for a placement without them.
func variableSuffix(values dashscope.VariableValues, variables []models.DashboardVariable) string {
	names := make([]string, 0, len(values))
	for name := range values {
		// A variable with one option, such as a constant, says nothing about
		// which combination a dependency came from.
		for _, v := range variables {
			if v.Name == name && len(v.Options) > 1 {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + "=" + values[name].Value
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// dashboardBinding reports one widget binding's sources.
func (c *dependencyCollector) dashboardBinding(b dashscope.Binding, p string) {
	switch b.Source {
	case dashscope.SourceControl:
		c.add(c.reference("control", b.ControlID, "", p))
	case dashscope.SourceMQTT:
		c.add(c.reference("mqtt_device", b.DeviceID, "", p))
	case dashscope.SourceHA:
		c.add(c.reference("connection", b.ConnectionID, "", p))
		c.add(c.reference("ha_entity", b.EntityID, b.ConnectionID, p))
	case dashscope.SourceNVR:
		c.add(c.reference("connection", b.ConnectionID, "", p))
	case dashscope.SourceTrigger:
		triggers := b.Triggers
		if len(triggers) == 0 {
			triggers = []dashscope.TriggerButton{{FlowID: b.FlowID, Trigger: b.Trigger}}
		}
		for _, t := range triggers {
			c.sharedFlow(t.FlowID, p+"/flow", 0)
			d := documentDependency{Kind: "widget_trigger", Ref: t.Trigger, Owner: t.FlowID, Path: p, Status: "available"}
			if !c.allowed(perm.PermFlowsRead) {
				d.Status = "unchecked"
				d.Detail = "requires " + perm.PermFlowsRead
			} else if f, err := flowops.LoadFor(c.ctx, callerID(c.ctx), t.FlowID, models.FlowAccessView); err != nil || f.DeployedVersionID == nil {
				d.Status = "unavailable"
				d.Detail = "flow has no available deployment"
			} else {
				v, e := models.FindFlowVersionByID(*f.DeployedVersionID)
				if e != nil {
					d.Status = "unchecked"
				} else {
					g, e := flowops.ParseGraph(v.Graph)
					found := false
					if e == nil {
						for _, trigger := range flow.WidgetTriggers(g) {
							if trigger.Name == t.Trigger {
								found = true
							}
						}
					}
					if !found {
						d.Status = "missing"
						d.Detail = "named trigger not found in deployed flow"
					} else if !f.Enabled {
						d.Status = "unavailable"
						d.Detail = "flow is disabled"
					}
				}
			}
			c.add(d)
		}
	default:
		c.add(documentDependency{Kind: "binding", Ref: b.Source, Path: p, Status: "invalid", Detail: "select a supported binding source"})
	}
}

// conditionPaths names each state condition ConditionBindings returns, in
// the same order: visibility conditions, then each cue's.
func conditionPaths(d *models.WidgetDisplay) []string {
	if d == nil {
		return nil
	}
	var out []string
	for i, c := range d.Conditions {
		if c.Kind == models.ConditionState {
			out = append(out, fmt.Sprintf("display/conditions/%d", i))
		}
	}
	for i, cue := range d.Cues {
		for j, c := range cue.Conditions {
			if c.Kind == models.ConditionState {
				out = append(out, fmt.Sprintf("display/cues/%d/conditions/%d", i, j))
			}
		}
	}
	return out
}
