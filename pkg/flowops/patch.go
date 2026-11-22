package flowops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// GraphOperation targets an item by ID in the root graph or a local subgraph.
type GraphOperation struct {
	Op           string         `json:"op" jsonschema:"add_node, update_node, remove_node, add_edge, update_edge, or remove_edge."`
	ID           string         `json:"id" jsonschema:"Node or edge ID in the target graph."`
	SubgraphPath []string       `json:"subgraph_path,omitempty" jsonschema:"Local subgraph keys from root to target; omit for the root graph."`
	Value        map[string]any `json:"value,omitempty" jsonschema:"Fields for add or update. Updates merge objects recursively; null removes a field and arrays replace. The item ID cannot change."`
}

// PatchGraph applies a batch to a private draft copy and saves once after
// validation. by is the user making the change, who may only add references
// to shared subflows they can read.
func PatchGraph(ctx context.Context, f *models.Flow, by uuid.UUID, expected string, operations []GraphOperation) error {
	if expected == "" {
		return fmt.Errorf("expected_revision is required; read get_flow first")
	}
	if len(operations) == 0 || len(operations) > 200 {
		return fmt.Errorf("provide between 1 and 200 operations")
	}
	draft, err := LoadDraft(f)
	if err != nil {
		return err
	}
	if DraftRevision(draft.Graph) != expected {
		return fmt.Errorf("draft changed; read get_flow and reapply the patch")
	}
	g, err := ParseGraph(draft.Graph)
	if err != nil {
		return err
	}
	for i, op := range operations {
		if err := patchGraphAt(g, op.SubgraphPath, op); err != nil {
			return fmt.Errorf("operation %d: %w", i, err)
		}
	}
	if err := ValidateDraft(f, g); err != nil {
		return err
	}
	if err := CheckSubflowReferences(ctx, by, f, g); err != nil {
		return err
	}
	return SaveDraftChecked(f, g, expected)
}

func patchGraphAt(g *flow.Graph, path []string, op GraphOperation) error {
	if len(path) > 0 {
		sub, ok := g.Subgraphs[path[0]]
		if !ok {
			return fmt.Errorf("subgraph %q not found", path[0])
		}
		if err := patchGraphAt(&sub, path[1:], op); err != nil {
			return err
		}
		g.Subgraphs[path[0]] = sub
		return nil
	}
	if op.ID == "" {
		return fmt.Errorf("item id is required")
	}
	action, kind, ok := strings.Cut(op.Op, "_")
	if !ok || (kind != "node" && kind != "edge") || (action != "add" && action != "update" && action != "remove") {
		return fmt.Errorf("unknown operation %q", op.Op)
	}
	if kind == "node" {
		nodes, err := patchItems(g.Nodes, op, func(n flow.Node) string { return n.ID })
		if err != nil {
			return err
		}
		g.Nodes = nodes
		if action == "remove" {
			edges := g.Edges[:0]
			for _, e := range g.Edges {
				if e.Source != op.ID && e.Target != op.ID {
					edges = append(edges, e)
				}
			}
			g.Edges = edges
		}
	} else {
		edges, err := patchItems(g.Edges, op, func(e flow.Edge) string { return e.ID })
		if err != nil {
			return err
		}
		g.Edges = edges
	}
	return nil
}

func patchItems[T any](items []T, op GraphOperation, id func(T) string) ([]T, error) {
	index := -1
	for i, item := range items {
		if id(item) == op.ID {
			index = i
			break
		}
	}
	action, _, _ := strings.Cut(op.Op, "_")
	if action == "add" && index >= 0 {
		return nil, fmt.Errorf("item %q already exists", op.ID)
	}
	if action != "add" && index < 0 {
		return nil, fmt.Errorf("item %q not found", op.ID)
	}
	if action == "remove" {
		return append(items[:index], items[index+1:]...), nil
	}
	if op.Value == nil {
		return nil, fmt.Errorf("value is required for %s", op.Op)
	}
	if v, ok := op.Value["id"]; ok && v != op.ID {
		return nil, fmt.Errorf("item id cannot change")
	}
	value := map[string]any{}
	if index >= 0 {
		raw, err := json.Marshal(items[index])
		if err != nil {
			return nil, err
		}
		// Numbers stay as written: decoding them as floats would round every
		// integer past 2^53 in fields the patch never named.
		existing := json.NewDecoder(bytes.NewReader(raw))
		existing.UseNumber()
		if err := existing.Decode(&value); err != nil {
			return nil, err
		}
	}
	mergeGraphFields(value, op.Value)
	value["id"] = op.ID
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var item T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil {
		return nil, err
	}
	if index >= 0 {
		items[index] = item
	} else {
		items = append(items, item)
	}
	return items, nil
}

func mergeGraphFields(dst, patch map[string]any) {
	for key, value := range patch {
		if value == nil {
			delete(dst, key)
			continue
		}
		if object, ok := value.(map[string]any); ok {
			child, _ := dst[key].(map[string]any)
			if child == nil {
				child = map[string]any{}
			}
			mergeGraphFields(child, object)
			dst[key] = child
		} else {
			dst[key] = value
		}
	}
}
