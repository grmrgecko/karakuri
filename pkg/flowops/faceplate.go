package flowops

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// FaceplateTab is one faceplate as the flow page draws it.
type FaceplateTab struct {
	// Key addresses the faceplate: its node ID, "subgraph/node" inside a
	// local subflow, or "subflowNode>innerKey" inside a shared subflow.
	Key      string                  `json:"key" description:"Address of the faceplate within the flow."`
	Name     string                  `json:"name" description:"Tab name."`
	Order    int                     `json:"order" description:"Position in the tab bar."`
	Controls []flow.FaceplateControl `json:"controls" description:"The tab's controls."`
	Values   map[string]any          `json:"values" description:"Applied values by control ID; a missing value means the default."`
	Script   string                  `json:"script,omitempty" description:"Change script run in the browser."`
	// Subflow names the subflow node a shared subflow's tab belongs to.
	Subflow string `json:"subflow,omitempty" description:"Label of the subflow node a shared subflow's tab comes from."`
}

// FaceplateView is a flow's faceplates for one caller.
type FaceplateView struct {
	FlowID      string         `json:"flow_id"`
	Name        string         `json:"name"`
	AccessLevel string         `json:"access_level" description:"The caller's level on the flow."`
	Source      string         `json:"source" description:"draft for editors, deployed for faceplate and view levels."`
	Tabs        []FaceplateTab `json:"tabs"`
}

// GraphHasFaceplates reports whether a graph, its local subflows, or a shared
// subflow it calls holds a faceplate.
func GraphHasFaceplates(g *flow.Graph) bool {
	return len(collectTabs(g)) > 0
}

// collectTabs lists a graph's faceplates in tab order: its own and its local
// subflows', then those of each shared subflow a root node calls, with that
// node's per-instance values over the subflow's own.
func collectTabs(g *flow.Graph) []FaceplateTab {
	if g == nil {
		return nil
	}
	var tabs []FaceplateTab
	tabs = append(tabs, localTabs(g)...)
	for _, n := range g.Nodes {
		if n.Type != flow.NodeTypeSubflow || n.SubflowID == nil {
			continue
		}
		shared, err := engine.ResolveSharedSubflowFromDB(*n.SubflowID)
		if err != nil {
			continue
		}
		overrides := subflowFaceplateValues(n.Config)
		label := nodeLabel(n)
		for _, tab := range localTabs(shared) {
			values := map[string]any{}
			for k, v := range tab.Values {
				values[k] = v
			}
			for k, v := range overrides[tab.Key] {
				values[k] = v
			}
			tab.Values = values
			tab.Key = n.ID + ">" + tab.Key
			tab.Subflow = label
			tabs = append(tabs, tab)
		}
	}
	sort.SliceStable(tabs, func(i, j int) bool {
		if tabs[i].Order != tabs[j].Order {
			return tabs[i].Order < tabs[j].Order
		}
		return tabs[i].Name < tabs[j].Name
	})
	return tabs
}

// localTabs lists the faceplates in a graph and its local subflows.
func localTabs(g *flow.Graph) []FaceplateTab {
	var tabs []FaceplateTab
	add := func(prefix string, nodes []flow.Node) {
		for _, n := range nodes {
			if n.Type != flow.NodeTypeFaceplate {
				continue
			}
			cfg, err := flow.ParseFaceplate(n.Config)
			if err != nil {
				continue
			}
			values := cfg.Values
			if values == nil {
				values = map[string]any{}
			}
			controls := cfg.Controls
			if controls == nil {
				controls = []flow.FaceplateControl{}
			}
			tabs = append(tabs, FaceplateTab{Key: prefix + n.ID, Name: cfg.Name, Order: cfg.Order, Controls: controls, Values: values, Script: cfg.Script})
		}
	}
	add("", g.Nodes)
	ids := make([]string, 0, len(g.Subgraphs))
	for id := range g.Subgraphs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		add(id+"/", g.Subgraphs[id].Nodes)
	}
	return tabs
}

// nodeLabel returns a node's display label, or its ID.
func nodeLabel(n flow.Node) string {
	var cfg struct {
		Label string `json:"_label"`
	}
	_ = json.Unmarshal(n.Config, &cfg)
	if cfg.Label != "" {
		return cfg.Label
	}
	return n.ID
}

// subflowFaceplateValues reads a subflow node's per-instance faceplate
// values, keyed by the inner faceplate's key, then control ID.
func subflowFaceplateValues(raw json.RawMessage) map[string]map[string]any {
	var cfg struct {
		Values map[string]map[string]any `json:"faceplate_values"`
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg.Values
}

// Faceplates returns a flow's faceplates for a user holding at least the
// faceplate level. Editors and viewers see the draft they work on; a
// faceplate-level user sees the deployed version, the only one they apply to.
func Faceplates(ctx context.Context, userID uuid.UUID, f *models.Flow) (*FaceplateView, error) {
	if err := Authorize(ctx, userID, f, models.FlowAccessFaceplate); err != nil {
		return nil, err
	}
	rank := Rank(ctx, userID, f)
	view := &FaceplateView{FlowID: f.ID.String(), Name: f.Name, AccessLevel: models.FlowAccessLevel(rank), Tabs: []FaceplateTab{}}
	var graphJSON string
	if rank >= models.FlowAccessRank(models.FlowAccessView) {
		draft, err := LoadDraft(f)
		if err != nil {
			return nil, err
		}
		view.Source, graphJSON = "draft", draft.Graph
	} else {
		if f.DeployedVersionID == nil {
			return view, nil
		}
		version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
		if err != nil {
			return nil, apperrors.ErrInternal.WithError(err)
		}
		view.Source, graphJSON = "deployed", version.Graph
	}
	g, err := ParseGraph(graphJSON)
	if err != nil {
		return nil, err
	}
	if tabs := collectTabs(g); tabs != nil {
		view.Tabs = tabs
	}
	return view, nil
}

// FaceplateValues are new control values by faceplate key, then control ID.
// A null value clears the applied value, returning the control to its default.
type FaceplateValues map[string]map[string]any

// applyFaceplateValues writes values into a graph, checking each against its
// control. With skipMissing, a faceplate or control the graph lacks is passed
// over rather than refused, for merging values into a draft that has moved on.
func applyFaceplateValues(g *flow.Graph, values FaceplateValues, skipMissing bool) error {
	for key, controls := range values {
		if subNode, inner, ok := strings.Cut(key, ">"); ok {
			if err := applySubflowValues(g, subNode, inner, controls, skipMissing); err != nil {
				return err
			}
			continue
		}
		n := faceplateNode(g, key)
		if n == nil {
			if skipMissing {
				continue
			}
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("faceplate %q not found", key))
		}
		cfg, err := flow.ParseFaceplate(n.Config)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("faceplate %q config is invalid", key))
		}
		stored := cfg.Values
		if stored == nil {
			stored = map[string]any{}
		}
		for id, v := range controls {
			c, found := findControl(cfg.Controls, id)
			if !found {
				if skipMissing {
					continue
				}
				return apperrors.ErrValidation.WithMessage(fmt.Sprintf("faceplate %q has no control %q", key, id))
			}
			if why := flow.CheckControlValue(c, v); why != "" {
				return apperrors.ErrValidation.WithMessage(fmt.Sprintf("%s %s", controlName(c), why))
			}
			if v == nil {
				delete(stored, id)
			} else {
				stored[id] = v
			}
		}
		raw, err := setConfigKey(n.Config, "values", stored)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		n.Config = raw
	}
	return nil
}

// applySubflowValues stores values for a faceplate inside a shared subflow on
// the calling subflow node, so each caller keeps its own.
func applySubflowValues(g *flow.Graph, subNodeID, inner string, controls map[string]any, skipMissing bool) error {
	var n *flow.Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == subNodeID && g.Nodes[i].Type == flow.NodeTypeSubflow && g.Nodes[i].SubflowID != nil {
			n = &g.Nodes[i]
		}
	}
	if n == nil {
		if skipMissing {
			return nil
		}
		return apperrors.ErrValidation.WithMessage(fmt.Sprintf("subflow node %q not found", subNodeID))
	}
	shared, err := engine.ResolveSharedSubflowFromDB(*n.SubflowID)
	if err != nil {
		return apperrors.ErrValidation.WithMessage(err.Error())
	}
	fp := faceplateNode(shared, inner)
	if fp == nil {
		if skipMissing {
			return nil
		}
		return apperrors.ErrValidation.WithMessage(fmt.Sprintf("the subflow has no faceplate %q", inner))
	}
	cfg, err := flow.ParseFaceplate(fp.Config)
	if err != nil {
		return apperrors.ErrValidation.WithMessage("the subflow's faceplate config is invalid")
	}
	all := subflowFaceplateValues(n.Config)
	if all == nil {
		all = map[string]map[string]any{}
	}
	stored := all[inner]
	if stored == nil {
		stored = map[string]any{}
	}
	for id, v := range controls {
		c, found := findControl(cfg.Controls, id)
		if !found {
			if skipMissing {
				continue
			}
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("the subflow's faceplate has no control %q", id))
		}
		if why := flow.CheckControlValue(c, v); why != "" {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("%s %s", controlName(c), why))
		}
		if v == nil {
			delete(stored, id)
		} else {
			stored[id] = v
		}
	}
	if len(stored) == 0 {
		delete(all, inner)
	} else {
		all[inner] = stored
	}
	raw, err := setConfigKey(n.Config, "faceplate_values", all)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	n.Config = raw
	return nil
}

// faceplateNode finds a faceplate by key in a graph or its local subflows.
func faceplateNode(g *flow.Graph, key string) *flow.Node {
	nodes := g.Nodes
	id := key
	if sg, nodeID, ok := strings.Cut(key, "/"); ok {
		sub, found := g.Subgraphs[sg]
		if !found {
			return nil
		}
		for i := range sub.Nodes {
			if sub.Nodes[i].ID == nodeID && sub.Nodes[i].Type == flow.NodeTypeFaceplate {
				// Subgraphs are stored by value, so write the change back
				// through the map when the caller updates the node.
				n := &sub.Nodes[i]
				g.Subgraphs[sg] = sub
				return n
			}
		}
		return nil
	}
	for i := range nodes {
		if nodes[i].ID == id && nodes[i].Type == flow.NodeTypeFaceplate {
			return &nodes[i]
		}
	}
	return nil
}

func findControl(controls []flow.FaceplateControl, id string) (flow.FaceplateControl, bool) {
	for _, c := range controls {
		if c.ID == id {
			return c, true
		}
	}
	return flow.FaceplateControl{}, false
}

func controlName(c flow.FaceplateControl) string {
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

// setConfigKey replaces one key in a node config, keeping every other key,
// presentation keys included.
func setConfigKey(raw json.RawMessage, key string, value any) (json.RawMessage, error) {
	// The other keys stay raw so they are written back as stored, large
	// integers included.
	cfg := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}
	// A stored config of JSON null decodes to a nil map.
	if cfg == nil {
		cfg = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	cfg[key] = encoded
	return json.Marshal(cfg)
}

// ApplyFaceplates applies faceplate values and deploys. An editor's Apply
// saves the draft with the values, draft edits included, and deploys it. A
// faceplate-level user's Apply deploys the current version with only the new
// values, so draft work they cannot see stays undeployed, then writes the
// values into the draft. Viewers may not apply.
func ApplyFaceplates(ctx context.Context, userID uuid.UUID, f *models.Flow, values FaceplateValues, comment string) (int, error) {
	if err := Authorize(ctx, userID, f, models.FlowAccessFaceplate); err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, apperrors.ErrValidation.WithMessage("no values to apply")
	}
	if err := checkFaceplateConnections(ctx, userID, values); err != nil {
		return 0, err
	}
	if comment == "" {
		comment = "Faceplate values applied"
	}
	rank := Rank(ctx, userID, f)
	switch {
	case rank >= models.FlowAccessRank(models.FlowAccessEdit):
		draft, err := LoadDraft(f)
		if err != nil {
			return 0, err
		}
		g, err := ParseGraph(draft.Graph)
		if err != nil {
			return 0, err
		}
		if err := applyFaceplateValues(g, values, false); err != nil {
			return 0, err
		}
		if err := ValidateDraft(f, g); err != nil {
			return 0, err
		}
		if err := SaveDraftChecked(f, g, ""); err != nil {
			return 0, err
		}
		return Deploy(ctx, f, comment, userID)
	case rank == models.FlowAccessRank(models.FlowAccessFaceplate):
		return applyToDeployed(ctx, userID, f, values, comment)
	default:
		return 0, apperrors.ErrForbidden.WithMessage("a view share cannot apply faceplate values")
	}
}

// applyToDeployed freezes the deployed graph with new values as the next
// version and merges the values into the draft.
func applyToDeployed(ctx context.Context, userID uuid.UUID, f *models.Flow, values FaceplateValues, comment string) (int, error) {
	if f.DeployedVersionID == nil {
		return 0, apperrors.ErrValidation.WithMessage("the flow has not been deployed")
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	g, err := ParseGraph(version.Graph)
	if err != nil {
		return 0, err
	}
	if err := applyFaceplateValues(g, values, false); err != nil {
		return 0, err
	}
	settings := *f
	version.ApplySettings(&settings)
	if err := ValidateExecutable(&settings, g); err != nil {
		return 0, err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	// The applied version keeps acting as whoever deployed the flow; a
	// faceplate-level user changes values, not whose access the flow uses.
	runAs := engine.RunAsFor(f, version)
	next, err := freezeVersion(ctx, f, g, string(raw), models.FlowVersion{
		Name: settings.Name, Description: settings.Description, ErrorFlowID: settings.ErrorFlowID,
		RunAsID: &runAs,
	}, comment, userID)
	if err != nil {
		return 0, err
	}

	// The draft takes the new values too, so the next editor deploy does not
	// put the old ones back. Faceplates the draft no longer has are skipped.
	if draft, err := LoadDraft(f); err == nil {
		if dg, err := ParseGraph(draft.Graph); err == nil {
			if applyFaceplateValues(dg, values, true) == nil {
				_ = SaveDraftChecked(f, dg, "")
			}
		}
	}
	return next, nil
}

// ReorderFaceplates sets the tab order of the draft's own faceplates. Tabs
// from shared subflows keep their subflow's order.
func ReorderFaceplates(ctx context.Context, userID uuid.UUID, f *models.Flow, keys []string) error {
	if err := Authorize(ctx, userID, f, models.FlowAccessEdit); err != nil {
		return err
	}
	draft, err := LoadDraft(f)
	if err != nil {
		return err
	}
	g, err := ParseGraph(draft.Graph)
	if err != nil {
		return err
	}
	for i, key := range keys {
		if strings.Contains(key, ">") {
			continue
		}
		n := faceplateNode(g, key)
		if n == nil {
			return apperrors.ErrValidation.WithMessage(fmt.Sprintf("faceplate %q not found", key))
		}
		raw, err := setConfigKey(n.Config, "order", i)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		n.Config = raw
	}
	return SaveDraftChecked(f, g, "")
}
