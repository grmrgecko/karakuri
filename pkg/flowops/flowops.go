// Package flowops holds the flow lifecycle operations shared by the REST
// handlers and the MCP tools: loading, validation, draft saves, deploy,
// enable/disable, delete, test runs, and export/import. Both callers apply
// their own authentication and permission checks first; everything here
// assumes the caller is allowed to act. Errors are AppErrors so both layers
// report the same codes and messages.
package flowops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"gorm.io/gorm"
)

// FlowItem augments a flow with derived deployment state.
type FlowItem struct {
	models.Flow
	Deployed        bool `json:"deployed" description:"Whether the flow has a deployed version."`
	HasDraftChanges bool `json:"has_draft_changes" description:"Whether the draft differs from the deployed version."`
	// AccessLevel is the caller's level on the flow, filled in by callers
	// that know who is asking. It tells the editor which controls to offer;
	// every route still checks for itself.
	AccessLevel string `json:"access_level,omitempty" description:"The caller's access level: faceplate, view, edit, or admin."`
}

// FlowDetail is a flow with its parsed draft graph.
type FlowDetail struct {
	DraftRevision string `json:"draft_revision"`
	FlowItem
	Graph *flow.Graph `json:"graph" description:"Draft graph document."`
}

// dbSubflowResolver resolves shared subflow references against their deployed
// versions for graph validation, and reports the flow being validated so
// self-references are caught as cycles.
type dbSubflowResolver struct {
	selfID uuid.UUID
}

// ResolveShared loads the deployed graph of a shared subflow.
func (r dbSubflowResolver) ResolveShared(id uuid.UUID) (*flow.Graph, error) {
	return engine.ResolveSharedSubflowFromDB(id)
}

// SelfFlowID implements flow.SelfIdentifier.
func (r dbSubflowResolver) SelfFlowID() uuid.UUID { return r.selfID }

// ValidateFull runs full strict validation of a flow's graph with the
// database-backed subflow resolver. Used for deploy and test runs.
func ValidateFull(f *models.Flow, g *flow.Graph) error {
	if err := flow.ValidateFull(g, engine.Default(), dbSubflowResolver{selfID: f.ID}, f.IsSubflow); err != nil {
		return err
	}
	return validateScripts(g)
}

// ValidateExecutable applies strict checks to every branch a deployed trigger
// can reach. Other canvas fragments remain editable draft material and cannot
// run, so an unfinished disconnected node does not block a working flow.
func ValidateExecutable(f *models.Flow, g *flow.Graph) error {
	if f.IsSubflow {
		return ValidateFull(f, g)
	}
	activeNodes := make(map[string]bool)
	// Edges are collected by value: an edge ID is optional, so several edges
	// can share the empty one.
	activeEdges := make(map[flow.Edge]bool)
	for i := range g.Nodes {
		meta, ok := engine.Default().MetaFor(g.Nodes[i].Type)
		if !ok || !meta.IsTrigger() {
			continue
		}
		branch := flow.ExecutionGraph(g, g.Nodes[i].ID)
		for _, node := range branch.Nodes {
			activeNodes[node.ID] = true
		}
		for _, edge := range branch.Edges {
			activeEdges[edge] = true
		}
	}
	if len(activeNodes) == 0 {
		return ValidateFull(f, g)
	}
	executable := &flow.Graph{Viewport: g.Viewport, Subgraphs: g.Subgraphs}
	for _, node := range g.Nodes {
		if activeNodes[node.ID] {
			executable.Nodes = append(executable.Nodes, node)
		}
	}
	for _, edge := range g.Edges {
		if activeEdges[edge] {
			executable.Edges = append(executable.Edges, edge)
		}
	}
	return ValidateFull(f, withReferencedSubgraphs(executable))
}

// withReferencedSubgraphs returns g holding only the local subgraphs its nodes
// reference, directly or through another subgraph. A graph cut down to what
// can run still carries every subgraph in the document, and the body of a
// subflow node that was cut away is draft material like the node itself.
func withReferencedSubgraphs(g *flow.Graph) *flow.Graph {
	if len(g.Subgraphs) == 0 {
		return g
	}
	referenced := CollectSubgraphClosure(g, g.Subgraphs)
	out := *g
	out.Subgraphs = make(map[string]flow.Graph, len(referenced))
	for id, sub := range g.Subgraphs {
		if referenced[id] {
			out.Subgraphs[id] = sub
		}
	}
	return &out
}

// ValidateDraft validates a draft save leniently: required config fields may
// be missing or empty so unfinished or imported flows can be saved. Deploy
// still enforces the full schema.
func ValidateDraft(f *models.Flow, g *flow.Graph) error {
	return flow.ValidateDraft(g, engine.Default(), dbSubflowResolver{selfID: f.ID}, f.IsSubflow)
}

// Validate checks a graph document without saving it. selfID identifies the
// flow the graph belongs to so self-references are caught as cycles; pass a
// fresh UUID for a graph that has no flow yet. Strict is the deploy-level
// check, lenient is the draft-save one.
func Validate(g *flow.Graph, selfID uuid.UUID, isSubflow, strict bool) error {
	if strict {
		if err := flow.ValidateFull(g, engine.Default(), dbSubflowResolver{selfID: selfID}, isSubflow); err != nil {
			return err
		}
		return validateScripts(g)
	}
	return flow.ValidateDraft(g, engine.Default(), dbSubflowResolver{selfID: selfID}, isSubflow)
}

// Load parses a flow ID and loads the flow, mapping failures to AppErrors.
func Load(id string) (*models.Flow, error) {
	fid, err := uuid.Parse(id)
	if err != nil {
		return nil, apperrors.ErrValidation.WithMessage("invalid flow ID")
	}
	f, err := models.FindFlowByID(fid)
	if err != nil {
		return nil, apperrors.ErrNotFound.WithMessage("flow not found")
	}
	return f, nil
}

// LoadDraft returns a flow's draft version row.
func LoadDraft(f *models.Flow) (*models.FlowVersion, error) {
	if f.DraftVersionID == nil {
		return nil, apperrors.ErrInternal.WithMessage("flow has no draft version")
	}
	draft, err := models.FindFlowVersionByID(*f.DraftVersionID)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(fmt.Errorf("failed to load draft version: %w", err))
	}
	return draft, nil
}

// ParseGraph decodes a stored graph JSON blob into a Graph document.
func ParseGraph(raw string) (*flow.Graph, error) {
	var g flow.Graph
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, apperrors.ErrInternal.WithError(fmt.Errorf("failed to parse stored graph: %w", err))
	}
	return &g, nil
}

// Item derives deployment state for a flow by comparing the draft and
// deployed graph blobs, returning the draft row alongside when it loads.
func Item(f *models.Flow) (FlowItem, *models.FlowVersion) {
	item := FlowItem{Flow: *f, Deployed: f.DeployedVersionID != nil}

	// Load the draft row when present; missing drafts are treated as changed.
	var draft *models.FlowVersion
	if f.DraftVersionID != nil {
		if v, err := models.FindFlowVersionByID(*f.DraftVersionID); err == nil {
			draft = v
		}
	}

	// The draft has changes when nothing is deployed or the graphs differ.
	if f.DeployedVersionID == nil {
		item.HasDraftChanges = true
		return item, draft
	}
	deployed, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil || draft == nil || draft.Graph != deployed.Graph || (deployed.HasSettings && (f.Name != deployed.Name || f.Description != deployed.Description || !sameID(f.ErrorFlowID, deployed.ErrorFlowID))) {
		item.HasDraftChanges = true
	}
	return item, draft
}

// Detail assembles the full flow response including the draft graph.
func Detail(f *models.Flow) (*FlowDetail, error) {
	item, draft := Item(f)
	if draft == nil {
		return nil, apperrors.ErrInternal.WithMessage("flow has no draft version")
	}
	g, err := ParseGraph(draft.Graph)
	if err != nil {
		return nil, err
	}
	return &FlowDetail{FlowItem: item, Graph: g, DraftRevision: DraftRevision(draft.Graph)}, nil
}

// Create creates a flow with an empty draft version owned by the given user.
func Create(ctx context.Context, name, description string, isSubflow bool, folderID *uint, ownerID uuid.UUID) (*models.Flow, error) {
	// Refuse a name the owner can already see in use, which is the clash
	// worth reporting. A clash with a flow they cannot see is resolved by a
	// suffixed slug instead.
	if NameTaken(ctx, ownerID, name) {
		return nil, apperrors.ErrConflict.WithMessage("a flow with this name already exists")
	}

	// The folder must be one the owner may add to: its shares will reach the
	// new flow.
	if folderID != nil {
		if err := CheckFolderTarget(ctx, ownerID, folderID); err != nil {
			return nil, err
		}
	}

	// Create the flow and its empty draft version in a transaction.
	f := &models.Flow{
		Name:        name,
		Description: description,
		IsSubflow:   isSubflow,
		FolderID:    folderID,
		OwnerID:     ownerID,
	}
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(f).Error; err != nil {
			return err
		}
		draft := &models.FlowVersion{
			FlowID:      f.ID,
			Version:     0,
			Graph:       flow.EmptyGraphJSON,
			CreatedByID: &ownerID,
		}
		if err := tx.Create(draft).Error; err != nil {
			return err
		}
		f.DraftVersionID = &draft.ID
		return tx.Model(f).Update("draft_version_id", draft.ID).Error
	})
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(fmt.Errorf("failed to create flow: %w", err))
	}
	return f, nil
}

// Patch holds the optional metadata fields Update applies to a flow. A nil
// field is left alone; ErrorFlowID set to an empty string clears the link.
type Patch struct {
	Name        *string
	Description *string
	ErrorFlowID *string
	// OwnerID transfers the flow to another user. Only a holder of the manage
	// permission may, which covers flows left behind by a deleted account.
	OwnerID *string
	// By is the user making the change. A new error flow must be one they may
	// read, so naming it cannot reach a flow they were never shared.
	By uuid.UUID
}

// Update applies metadata changes to a flow and persists it.
func Update(ctx context.Context, f *models.Flow, p Patch) error {
	// Apply requested field updates.
	if p.Name != nil {
		f.Name = *p.Name
	}
	if p.Description != nil {
		f.Description = *p.Description
	}
	if p.ErrorFlowID != nil {
		if *p.ErrorFlowID == "" {
			f.ErrorFlowID = nil
		} else {
			errFlowID, err := uuid.Parse(*p.ErrorFlowID)
			if err != nil {
				return apperrors.ErrValidation.WithMessage("invalid error flow ID")
			}
			target, err := models.FindFlowByID(errFlowID)
			unchanged := f.ErrorFlowID != nil && *f.ErrorFlowID == errFlowID
			if err != nil || (!unchanged && !Holds(ctx, p.By, target, models.FlowAccessView)) {
				return apperrors.ErrValidation.WithMessage("error flow not found")
			}
			f.ErrorFlowID = &errFlowID
		}
	}
	if p.OwnerID != nil {
		if !auth.Can(ctx, p.By, perm.PermFlowsManage) {
			return apperrors.ErrForbidden.WithMessage("changing a flow's owner needs the flow manage permission")
		}
		ownerID, err := uuid.Parse(*p.OwnerID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid owner ID")
		}
		var count int64
		if err := db.DB.Model(&models.User{}).Where("id = ?", ownerID).Count(&count).Error; err != nil || count == 0 {
			return apperrors.ErrValidation.WithMessage("owner not found")
		}
		f.OwnerID = ownerID
	}

	// Persist changes.
	fields := []string{"updated_at"}
	if p.Name != nil {
		fields = append(fields, "name")
	}
	if p.Description != nil {
		fields = append(fields, "description")
	}
	if p.ErrorFlowID != nil {
		fields = append(fields, "error_flow_id")
	}
	if p.OwnerID != nil {
		fields = append(fields, "owner_id")
	}
	if err := db.DB.Model(f).Select(fields).Updates(f).Error; err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	NotifyChanged(f.ID)
	return nil
}

// Delete removes a flow with its versions and dependencies, disarming its
// triggers and cancelling its active runs first. A shared subflow with dependents is refused, since deleting
// it would break every graph referencing it.
func Delete(f *models.Flow) error {
	if f.IsSubflow {
		dependents, err := models.ListSubflowDependents(f.ID)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}
		if len(dependents) > 0 {
			details := make(map[string]string, len(dependents))
			for _, d := range dependents {
				details[d.ID.String()] = d.Name
			}
			return apperrors.ErrConflict.
				WithMessage(fmt.Sprintf("subflow is used by %d flow(s); remove those references first", len(dependents))).
				WithDetails(details)
		}
	}

	Disarm(f.ID)
	if eng != nil {
		// A run in flight would keep acting for a flow that is gone.
		eng.CancelFlowRuns(f.ID)
	}
	if err := f.Delete(); err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	// The flow's rows are gone (f.Delete's transaction removes its durable
	// node store); drop the engine's cached copy too.
	if eng != nil {
		eng.PurgeDurableNodeState(f.ID)
		eng.PurgeWaits(f.ID)
	}
	return nil
}

// SaveDraft marshals and persists a flow's draft graph and rebuilds its
// dependency rows. Validation is the caller's job: a draft save validates
// leniently, a version restore saves an already-deployed graph as is.
func SaveDraft(f *models.Flow, g *flow.Graph) error {
	return SaveDraftChecked(f, g, "")
}

// DraftRevision identifies the exact serialized draft used by an editor or MCP reader.
func DraftRevision(raw string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) }

// NotifyChanged tells open editors to fetch the committed document.
func NotifyChanged(id uuid.UUID) { mq.B.Publish("editor:flow:"+id.String(), "changed", nil) }

// SaveDraftChecked rejects a stale whole-graph write when a revision is supplied.
func SaveDraftChecked(f *models.Flow, g *flow.Graph, expected string) error {
	return saveDraft(f, g, expected, nil)
}

func saveDraft(f *models.Flow, g *flow.Graph, expected string, settings *models.FlowVersion) error {
	draft, err := LoadDraft(f)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	if expected != "" && expected != DraftRevision(draft.Graph) {
		return apperrors.ErrConflict.WithMessage("the draft changed in another editor; reload it before saving")
	}
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if settings != nil {
			settings.ApplySettings(f)
			if err := tx.Model(f).Updates(map[string]any{"name": f.Name, "description": f.Description, "error_flow_id": f.ErrorFlowID}).Error; err != nil {
				return err
			}
		}
		result := tx.Model(draft).Where("graph = ?", draft.Graph).Update("graph", string(raw))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return apperrors.ErrConflict.WithMessage("the draft changed in another editor; reload it before saving")
		}
		if err := tx.Where("flow_id = ?", f.ID).Delete(&models.FlowDependency{}).Error; err != nil {
			return err
		}
		deps := []models.FlowDependency{}
		for _, dep := range flow.Dependencies(g, engine.Default()) {
			deps = append(deps, models.FlowDependency{FlowID: f.ID, Kind: dep.Kind, RefID: dep.RefID})
		}
		if len(deps) > 0 {
			return tx.Create(&deps).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	NotifyChanged(f.ID)
	return nil
}

// Deploy validates the draft strictly and freezes it as an immutable numbered
// version, returning the new version number. Runs of the previous version are
// canceled, and an enabled flow is rearmed against the new deployed graph.
func Deploy(ctx context.Context, f *models.Flow, comment string, userID uuid.UUID) (int, error) {
	// Load and validate the draft.
	draft, err := LoadDraft(f)
	if err != nil {
		return 0, err
	}
	g, err := ParseGraph(draft.Graph)
	if err != nil {
		return 0, err
	}
	if err := ValidateExecutable(f, g); err != nil {
		return 0, err
	}
	if err := CheckResourceAccess(ctx, userID, g); err != nil {
		return 0, err
	}
	// Runs of the new version act as the user deploying it.
	runAs := userID
	return freezeVersion(ctx, f, g, draft.Graph, models.FlowVersion{
		HasSettings: true, Name: f.Name, Description: f.Description, ErrorFlowID: f.ErrorFlowID,
		RunAsID: &runAs,
	}, comment, userID)
}

// freezeVersion stores graphJSON as the next numbered version with the given
// settings, deploys it, and rearms the flow. Deploy freezes the draft; a
// faceplate-level Apply freezes the deployed graph with new values.
func freezeVersion(ctx context.Context, f *models.Flow, g *flow.Graph, graphJSON string, settings models.FlowVersion, comment string, userID uuid.UUID) (int, error) {
	next, err := models.NextFlowVersion(f.ID)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	hasFaceplates := GraphHasFaceplates(g)
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		version := &models.FlowVersion{
			HasSettings: true, Name: settings.Name, Description: settings.Description, ErrorFlowID: settings.ErrorFlowID,
			FlowID:      f.ID,
			Version:     next,
			Graph:       graphJSON,
			Comment:     comment,
			CreatedByID: &userID,
			RunAsID:     settings.RunAsID,
		}
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		f.DeployedVersionID = &version.ID
		f.HasFaceplates = hasFaceplates
		return tx.Model(f).Updates(map[string]any{"deployed_version_id": version.ID, "has_faceplates": hasFaceplates}).Error
	})
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(fmt.Errorf("failed to deploy flow: %w", err))
	}

	// Redeploying suspends runs of the previous version, which leaves their
	// pending waits stored, and rearms the flow against the new deployed
	// graph, which resumes the waits whose nodes it still has.
	if eng != nil {
		eng.SuspendFlowRuns(f.ID)
	}
	if f.Enabled {
		Rearm(f.ID)
	}
	NotifyChanged(f.ID)
	recordFlow(ctx, f, fmt.Sprintf("Deployed %s (version %d)", f.Name, next), map[string]any{"version": next, "comment": comment})
	return next, nil
}

// recordFlow records a flow lifecycle change with whoever made it.
func recordFlow(ctx context.Context, f *models.Flow, message string, details map[string]any) {
	events.Record(events.Entry{
		Category: models.EventFlow,
		Subject:  events.Subject{Kind: "flow", ID: f.ID.String(), Name: f.Name},
		Message:  message,
		Details:  details,
		Actor:    events.ActorFrom(ctx),
	})
}

// SetEnabled enables or disables a flow. Enabling requires a deployed version
// and arms the flow's triggers; disabling disarms them and cancels its active
// runs.
func SetEnabled(ctx context.Context, f *models.Flow, enabled bool) error {
	if enabled && f.DeployedVersionID == nil {
		return apperrors.ErrValidation.WithMessage("flow has no deployed version")
	}

	f.Enabled = enabled
	if err := db.DB.Model(f).Update("enabled", enabled).Error; err != nil {
		return apperrors.ErrInternal.WithError(err)
	}

	if enabled {
		Rearm(f.ID)
	} else {
		Disarm(f.ID)
		if eng != nil {
			// Drop the waits before cancelling their runs, so every one is
			// recorded here rather than released quietly by its node.
			var versionID uint
			if f.DeployedVersionID != nil {
				versionID = *f.DeployedVersionID
			}
			eng.DropFlowWaits(f.ID, versionID, "flow disabled; wait dropped")
			eng.CancelFlowRuns(f.ID)
		}
	}
	NotifyChanged(f.ID)
	if enabled {
		recordFlow(ctx, f, "Enabled "+f.Name, nil)
	} else {
		recordFlow(ctx, f, "Disabled "+f.Name, nil)
	}
	return nil
}

// RestoreVersion copies a deployed version's graph into the flow's editable
// draft.
func RestoreVersion(f *models.Flow, version int) error {
	var v *models.FlowVersion
	var err error
	if version == 0 {
		if f.DeployedVersionID == nil {
			return apperrors.ErrValidation.WithMessage("flow has no deployed version to restore")
		}
		v, err = models.FindFlowVersionByID(*f.DeployedVersionID)
	} else {
		v, err = models.FindFlowVersion(f.ID, version)
	}
	if err != nil {
		return apperrors.ErrNotFound.WithMessage("version not found")
	}
	g, err := ParseGraph(v.Graph)
	if err != nil {
		return err
	}
	return saveDraft(f, g, "", v)
}

// CollectSubgraphClosure returns the set of subgraph IDs transitively
// reachable from the given graph's subflow nodes within one document.
func CollectSubgraphClosure(g *flow.Graph, subs map[string]flow.Graph) map[string]bool {
	reachable := make(map[string]bool)
	var walk func(g *flow.Graph)
	walk = func(g *flow.Graph) {
		for i := range g.Nodes {
			id := g.Nodes[i].SubgraphID
			if id == "" || reachable[id] {
				continue
			}
			reachable[id] = true
			if sub, ok := subs[id]; ok {
				walk(&sub)
			}
		}
	}
	walk(g)
	return reachable
}

// PruneOrphanSubgraphs drops subgraph entries no longer referenced from the
// document's nodes, directly or transitively.
func PruneOrphanSubgraphs(g *flow.Graph) {
	if len(g.Subgraphs) == 0 {
		return
	}
	reachable := CollectSubgraphClosure(g, g.Subgraphs)
	for id := range g.Subgraphs {
		if !reachable[id] {
			delete(g.Subgraphs, id)
		}
	}
	if len(g.Subgraphs) == 0 {
		g.Subgraphs = nil
	}
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
