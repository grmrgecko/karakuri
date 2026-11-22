package flowops

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// ExportDoc is the portable flow document format: the draft graph with every
// shared subflow reference embedded as a local subgraph, so the file is
// self-contained and importable on another instance. Connection config values
// are blanked on export because connection IDs never resolve elsewhere.
type ExportDoc struct {
	KarakuriFlow int         `json:"karakuri_flow" required:"true" description:"Document format version; currently 1."`
	Name         string      `json:"name" description:"Flow display name."`
	Description  string      `json:"description,omitempty" description:"Flow summary."`
	IsSubflow    bool        `json:"is_subflow,omitempty" description:"Whether the flow is a reusable subflow."`
	Graph        *flow.Graph `json:"graph" required:"true" description:"Self-contained graph document."`
}

// maxEmbedPasses bounds the shared-subflow embedding loop as a guard against
// pathological stored data; validation already rejects cycles and nesting
// beyond flow.MaxSubflowDepth.
const maxEmbedPasses = 100

// embedSharedSubflows rewrites every shared subflow reference in the document
// (root, existing local subgraphs, and subgraphs added while embedding) into
// a local subgraph copied from the shared flow's deployed graph. Nested
// local subgraphs of a shared flow are copied under fresh IDs; a shared flow
// referenced multiple times is embedded once and shared, except by a calling
// node that holds faceplate values of its own. Unresolvable
// references are collected and returned as one error listing them. So is a
// shared subflow the exporting user may not read: embedding copies its graph,
// so exporting a parent must not reveal a subflow its author was never shared.
func embedSharedSubflows(ctx context.Context, g *flow.Graph, by uuid.UUID) error {
	// sharedLocal maps an already embedded shared flow ID to its subgraph ID.
	sharedLocal := make(map[uuid.UUID]string)
	unresolved := make(map[string]bool)

	// embedNode replaces one node's shared reference with a local subgraph.
	embedNode := func(n *flow.Node) {
		id := *n.SubflowID
		// Faceplate values applied on this calling node are read only while
		// the reference is shared, so a node holding any gets a copy of its
		// own with the values written in.
		overrides := subflowFaceplateValues(n.Config)
		if lid, ok := sharedLocal[id]; ok && len(overrides) == 0 {
			n.SubgraphID = lid
			n.SubflowID = nil
			return
		}
		if sf, err := models.FindFlowByID(id); err != nil || !Holds(ctx, by, sf, models.FlowAccessView) {
			unresolved[fmt.Sprintf("shared subflow %s is not one you may read", id)] = true
			return
		}
		shared, err := engine.ResolveSharedSubflowFromDB(id)
		if err != nil {
			unresolved[err.Error()] = true
			return
		}
		// The resolved graph is decoded for this call alone, so it is written
		// in place. A value that no longer fits its control is left out, and
		// the subflow's own stands, as it does when the flow runs.
		for key, values := range overrides {
			fp := faceplateNode(shared, key)
			if fp == nil {
				continue
			}
			cfg, err := flow.ParseFaceplate(fp.Config)
			if err != nil {
				continue
			}
			stored := cfg.Values
			if stored == nil {
				stored = map[string]any{}
			}
			for controlID, v := range values {
				c, found := findControl(cfg.Controls, controlID)
				if !found || v == nil || flow.CheckControlValue(c, v) != "" {
					continue
				}
				stored[controlID] = v
			}
			if raw, err := setConfigKey(fp.Config, "values", stored); err == nil {
				fp.Config = raw
			}
		}
		if g.Subgraphs == nil {
			g.Subgraphs = make(map[string]flow.Graph)
		}
		// Copy the shared document in under fresh subgraph IDs, mirroring the
		// import_subflow logic: nested local subgraphs move to this document
		// and references to them are rewritten.
		renames := make(map[string]string, len(shared.Subgraphs))
		for sid := range shared.Subgraphs {
			renames[sid] = uuid.New().String()
		}
		renameRefs := func(gr *flow.Graph) {
			for i := range gr.Nodes {
				if to, ok := renames[gr.Nodes[i].SubgraphID]; ok {
					gr.Nodes[i].SubgraphID = to
				}
			}
		}
		for sid, nested := range shared.Subgraphs {
			renameRefs(&nested)
			g.Subgraphs[renames[sid]] = nested
		}
		root := flow.Graph{Nodes: shared.Nodes, Edges: shared.Edges, Viewport: shared.Viewport}
		renameRefs(&root)
		lid := uuid.New().String()
		g.Subgraphs[lid] = root
		if len(overrides) == 0 {
			sharedLocal[id] = lid
		}
		n.SubgraphID = lid
		n.SubflowID = nil
	}

	// Sweep until no shared references remain: embedded shared graphs may
	// themselves reference further shared subflows.
	for pass := 0; pass < maxEmbedPasses; pass++ {
		changed := false
		for i := range g.Nodes {
			if g.Nodes[i].SubflowID != nil {
				embedNode(&g.Nodes[i])
				changed = true
			}
		}
		ids := make([]string, 0, len(g.Subgraphs))
		for id := range g.Subgraphs {
			ids = append(ids, id)
		}
		for _, id := range ids {
			sub := g.Subgraphs[id]
			subChanged := false
			for i := range sub.Nodes {
				if sub.Nodes[i].SubflowID != nil {
					embedNode(&sub.Nodes[i])
					subChanged = true
				}
			}
			if subChanged {
				g.Subgraphs[id] = sub
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	if len(unresolved) > 0 {
		msgs := make([]string, 0, len(unresolved))
		for m := range unresolved {
			msgs = append(msgs, m)
		}
		sort.Strings(msgs)
		return apperrors.ErrValidation.
			WithMessage("cannot export: shared subflow references could not be embedded: " + strings.Join(msgs, "; "))
	}
	return nil
}

// blankConnectionFields empties every connection-typed config value in the
// document. Connection IDs are instance-local, so exported values would never
// resolve after import; blank values read as "unconfigured" in the editor.
func blankConnectionFields(g *flow.Graph, reg flow.MetaRegistry) {
	blankGraph := func(gr *flow.Graph) {
		for i := range gr.Nodes {
			n := &gr.Nodes[i]
			meta, ok := reg.MetaFor(n.Type)
			if !ok || len(n.Config) == 0 {
				continue
			}
			// Values stay raw so every field but the connection is written
			// back exactly as stored, large integers included.
			var values map[string]json.RawMessage
			if json.Unmarshal(n.Config, &values) != nil {
				continue
			}
			changed := false
			for _, field := range meta.ConfigSpec {
				if field.Type != flow.FieldConnection {
					continue
				}
				if v, ok := values[field.ID]; ok && string(v) != `""` {
					values[field.ID] = json.RawMessage(`""`)
					changed = true
				}
			}
			if changed {
				if raw, err := json.Marshal(values); err == nil {
					n.Config = raw
				}
			}
		}
	}
	blankGraph(g)
	for id := range g.Subgraphs {
		sub := g.Subgraphs[id]
		blankGraph(&sub)
		g.Subgraphs[id] = sub
	}
}

// availableFlowName returns a flow name the importing user does not already
// see in use: the wanted name, then " (imported)" and numbered variants. Names
// used only by flows they cannot see are free to them.
func availableFlowName(ctx context.Context, wanted string, by uuid.UUID) string {
	candidates := []string{wanted, wanted + " (imported)"}
	for i := 2; i <= 50; i++ {
		candidates = append(candidates, fmt.Sprintf("%s (imported %d)", wanted, i))
	}
	for _, name := range candidates {
		if !NameTaken(ctx, by, name) {
			return name
		}
	}
	return fmt.Sprintf("%s (imported %s)", wanted, uuid.New().String()[:8])
}

// Export builds the portable document for a flow's draft graph, with shared
// subflows embedded and connection values blanked. Every embedded subflow must
// be one the exporting user may read.
func Export(ctx context.Context, f *models.Flow, by uuid.UUID) (*ExportDoc, error) {
	draft, err := LoadDraft(f)
	if err != nil {
		return nil, err
	}
	g, err := ParseGraph(draft.Graph)
	if err != nil {
		return nil, err
	}

	// Embed shared subflows and strip instance-local connection values.
	if err := embedSharedSubflows(ctx, g, by); err != nil {
		return nil, err
	}
	blankConnectionFields(g, engine.Default())

	return &ExportDoc{
		KarakuriFlow: 1,
		Name:         f.Name,
		Description:  f.Description,
		IsSubflow:    f.IsSubflow,
		Graph:        g,
	}, nil
}

// Import creates a new flow from an exported document owned by the given
// user. The graph becomes the new flow's draft; blanked connection fields are
// tolerated (drafts validate leniently) and must be configured before deploy.
func Import(ctx context.Context, doc *ExportDoc, ownerID uuid.UUID) (*models.Flow, error) {
	// Validate the document shape.
	if doc.KarakuriFlow != 1 {
		return nil, apperrors.ErrValidation.WithMessage("unsupported document: karakuri_flow must be 1")
	}
	if doc.Graph == nil {
		return nil, apperrors.ErrValidation.WithMessage("document has no graph")
	}
	name := strings.TrimSpace(doc.Name)
	if name == "" {
		name = "Imported flow"
	}
	name = availableFlowName(ctx, name, ownerID)

	// Validate the graph leniently, like a draft save: unconfigured (blanked)
	// required fields are allowed until deploy.
	newID := uuid.New()
	f := &models.Flow{ID: newID, Name: name, Description: doc.Description, IsSubflow: doc.IsSubflow, OwnerID: ownerID}
	if err := Validate(doc.Graph, newID, doc.IsSubflow, false); err != nil {
		return nil, err
	}
	// A document may name shared subflows by ID. Every one is a new
	// reference, and must be a subflow the importer may read.
	if err := CheckSubflowReferences(ctx, ownerID, f, doc.Graph); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc.Graph)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}

	// Create the flow with the document as its draft graph.
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(f).Error; err != nil {
			return err
		}
		draft := &models.FlowVersion{FlowID: f.ID, Version: 0, Graph: string(raw), CreatedByID: &ownerID}
		if err := tx.Create(draft).Error; err != nil {
			return err
		}
		f.DraftVersionID = &draft.ID
		return tx.Model(f).Update("draft_version_id", draft.ID).Error
	})
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(fmt.Errorf("failed to import flow: %w", err))
	}

	// Rebuild dependency rows from the imported graph.
	var deps []models.FlowDependency
	for _, d := range flow.Dependencies(doc.Graph, engine.Default()) {
		deps = append(deps, models.FlowDependency{Kind: d.Kind, RefID: d.RefID})
	}
	if err := models.ReplaceFlowDependencies(f.ID, deps); err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	return f, nil
}
