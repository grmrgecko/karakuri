package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/apiutil"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/swaggest/usecase"
	"gorm.io/gorm"
)

// FlowPromoteInput identifies a local subgraph to promote into a shared
// subflow flow.
type FlowPromoteInput struct {
	ID         string `path:"id" format:"uuid" description:"Flow UUID."`
	SubgraphID string `json:"subgraph_id" required:"true" minLength:"1" description:"Local subgraph ID to promote."`
	Name       string `json:"name" required:"true" minLength:"1" description:"Name of the new shared subflow."`
}

// FlowPromoteSubgraphHandler returns the interactor that promotes a local
// subgraph into a shared subflow flow: the subgraph becomes a new flow with
// IsSubflow set, deployed as version 1 so its interface is immediately
// usable, and every node referencing the subgraph is rewritten to reference
// the new flow.
func FlowPromoteSubgraphHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowPromoteInput, out *apiutil.Success[*flowops.FlowDetail]) error {
		// Verify authentication.
		uc := middleware.GetUser(ctx)
		if uc == nil {
			return apperrors.ErrUnauthorized.WithMessage("unauthorized")
		}

		// Load flow, draft, and the subgraph to promote.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}
		draft, err := flowops.LoadDraft(f)
		if err != nil {
			return err
		}
		g, err := flowops.ParseGraph(draft.Graph)
		if err != nil {
			return err
		}
		sg, ok := g.Subgraphs[in.SubgraphID]
		if !ok {
			return apperrors.ErrValidation.WithMessage("subgraph does not exist in this flow")
		}

		// Refuse a name the caller can already see in use; a clash with a flow
		// they cannot see takes a suffixed slug instead.
		if flowops.NameTaken(ctx, uc.UserID, in.Name) {
			return apperrors.ErrConflict.WithMessage("a flow with this name already exists")
		}

		// Build the new subflow document: the subgraph plus any local
		// subgraphs it references transitively.
		newDoc := flow.Graph{Nodes: sg.Nodes, Edges: sg.Edges, Viewport: sg.Viewport}
		for id := range flowops.CollectSubgraphClosure(&sg, g.Subgraphs) {
			if nested, ok := g.Subgraphs[id]; ok {
				if newDoc.Subgraphs == nil {
					newDoc.Subgraphs = make(map[string]flow.Graph)
				}
				newDoc.Subgraphs[id] = nested
			}
		}
		raw, err := json.Marshal(&newDoc)
		if err != nil {
			return apperrors.ErrInternal.WithError(err)
		}

		// Validate the promoted document as a standalone subflow definition.
		newID := uuid.New()
		// Version 1 is deployed below without going through Deploy, which is
		// what otherwise records whether the deployed graph has faceplates.
		newFlow := &models.Flow{ID: newID, Name: in.Name, IsSubflow: true, OwnerID: uc.UserID,
			HasFaceplates: flowops.GraphHasFaceplates(&newDoc)}
		if err := flowops.Validate(&newDoc, newID, true, true); err != nil {
			return err
		}

		// Create the subflow with its draft and auto-deployed version 1.
		err = db.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(newFlow).Error; err != nil {
				return err
			}
			draftV := &models.FlowVersion{FlowID: newID, Version: 0, Graph: string(raw), CreatedByID: &uc.UserID}
			if err := tx.Create(draftV).Error; err != nil {
				return err
			}
			deployedV := &models.FlowVersion{FlowID: newID, Version: 1, Graph: string(raw), Comment: "Promoted from " + f.Name, CreatedByID: &uc.UserID,
				HasSettings: true, Name: newFlow.Name, Description: newFlow.Description, ErrorFlowID: newFlow.ErrorFlowID}
			if err := tx.Create(deployedV).Error; err != nil {
				return err
			}
			newFlow.DraftVersionID = &draftV.ID
			newFlow.DeployedVersionID = &deployedV.ID
			if err := tx.Model(newFlow).Updates(map[string]any{
				"draft_version_id":    draftV.ID,
				"deployed_version_id": deployedV.ID,
			}).Error; err != nil {
				return err
			}
			// The parent's rows are rebuilt without what moved into the new
			// flow, so the new flow has to record those references itself.
			deps := []models.FlowDependency{}
			for _, dep := range flow.Dependencies(&newDoc, engine.Default()) {
				deps = append(deps, models.FlowDependency{FlowID: newID, Kind: dep.Kind, RefID: dep.RefID})
			}
			if len(deps) == 0 {
				return nil
			}
			return tx.Create(&deps).Error
		})
		if err != nil {
			return apperrors.ErrInternal.WithError(fmt.Errorf("failed to promote subgraph: %w", err))
		}

		// Rewrite this flow's draft: subgraph references become shared
		// references and the promoted subgraph entry is dropped.
		for i := range g.Nodes {
			if g.Nodes[i].SubgraphID == in.SubgraphID {
				id := newID
				g.Nodes[i].SubflowID = &id
				g.Nodes[i].SubgraphID = ""
			}
		}
		for sgID := range g.Subgraphs {
			sub := g.Subgraphs[sgID]
			changed := false
			for i := range sub.Nodes {
				if sub.Nodes[i].SubgraphID == in.SubgraphID {
					id := newID
					sub.Nodes[i].SubflowID = &id
					sub.Nodes[i].SubgraphID = ""
					changed = true
				}
			}
			if changed {
				g.Subgraphs[sgID] = sub
			}
		}
		flowops.PruneOrphanSubgraphs(g)
		if err := flowops.SaveDraft(f, g); err != nil {
			return err
		}

		// Build response: the new shared subflow.
		detail, err := flowDetail(ctx, newFlow)
		if err != nil {
			return err
		}
		out.Data = detail
		out.Message = "Subgraph promoted to shared subflow"
		return nil
	})
	u.SetTitle("Promote Subgraph")
	u.SetDescription("Promotes a local subgraph into a shared subflow flow and rewires references in this flow's draft.")
	u.SetTags("Flows")
	return u
}

// FlowImportInput identifies a shared subflow to copy into a flow's draft as
// a local subgraph.
type FlowImportInput struct {
	ID        string `path:"id" format:"uuid" description:"Flow UUID."`
	SubflowID string `json:"subflow_id" required:"true" format:"uuid" description:"Shared subflow UUID to import."`
}

// SubgraphCreated is the response payload of an import: the fresh local
// subgraph ID the frontend can reference from a subflow node.
type SubgraphCreated struct {
	SubgraphID string `json:"subgraph_id" description:"ID of the created local subgraph."`
}

// FlowImportSubflowHandler returns the interactor that copies a shared
// subflow's deployed graph into this flow's draft as a local subgraph.
func FlowImportSubflowHandler() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in FlowImportInput, out *apiutil.Success[SubgraphCreated]) error {
		// Load flow and draft.
		f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
		if err != nil {
			return err
		}
		draft, err := flowops.LoadDraft(f)
		if err != nil {
			return err
		}
		g, err := flowops.ParseGraph(draft.Graph)
		if err != nil {
			return err
		}

		// Resolve the shared subflow's deployed graph.
		subflowID, err := uuid.Parse(in.SubflowID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage("invalid subflow ID")
		}
		if _, err := flowops.LoadByIDFor(ctx, callerID(ctx), subflowID, models.FlowAccessView); err != nil {
			return apperrors.ErrValidation.WithMessage("shared subflow not found")
		}
		shared, err := engine.ResolveSharedSubflowFromDB(subflowID)
		if err != nil {
			return apperrors.ErrValidation.WithMessage(err.Error())
		}

		// Copy the shared document in: its root becomes a fresh local
		// subgraph, and its own local subgraphs move to this document under
		// fresh IDs with references rewritten.
		if g.Subgraphs == nil {
			g.Subgraphs = make(map[string]flow.Graph)
		}
		renames := make(map[string]string, len(shared.Subgraphs))
		for id := range shared.Subgraphs {
			renames[id] = uuid.New().String()
		}
		renameRefs := func(gr *flow.Graph) {
			for i := range gr.Nodes {
				if to, ok := renames[gr.Nodes[i].SubgraphID]; ok {
					gr.Nodes[i].SubgraphID = to
				}
			}
		}
		for id, nested := range shared.Subgraphs {
			renameRefs(&nested)
			g.Subgraphs[renames[id]] = nested
		}
		root := flow.Graph{Nodes: shared.Nodes, Edges: shared.Edges, Viewport: shared.Viewport}
		renameRefs(&root)
		freshID := uuid.New().String()
		g.Subgraphs[freshID] = root

		// The copy carries the shared subflow's own calls to other shared
		// subflows, so it is checked like any other draft save: view on the
		// one imported does not reach the ones it calls.
		if err := flowops.CheckSubflowReferences(ctx, callerID(ctx), f, g); err != nil {
			return err
		}

		// Persist the draft.
		if err := flowops.SaveDraft(f, g); err != nil {
			return err
		}

		// Build response.
		out.Data = SubgraphCreated{SubgraphID: freshID}
		out.Message = "Subflow imported as local subgraph"
		return nil
	})
	u.SetTitle("Import Subflow")
	u.SetDescription("Copies a shared subflow's deployed graph into this flow's draft as a local subgraph.")
	u.SetTags("Flows")
	return u
}
