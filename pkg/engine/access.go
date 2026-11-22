package engine

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// RunAsFor returns the user runs of a deployed version act as: the user the
// version records, then whoever created the version, then the flow's owner,
// for versions frozen before run users were recorded.
func RunAsFor(f *models.Flow, v *models.FlowVersion) uuid.UUID {
	if v != nil && v.RunAsID != nil && *v.RunAsID != uuid.Nil {
		return *v.RunAsID
	}
	if v != nil && v.CreatedByID != nil && *v.CreatedByID != uuid.Nil {
		return *v.CreatedByID
	}
	if f != nil {
		return f.OwnerID
	}
	return uuid.Nil
}

// connectionFieldIDs returns the connection IDs a node's resolved config names
// in its connection-typed fields.
func connectionFieldIDs(meta NodeMeta, cfg json.RawMessage) []string {
	if len(cfg) == 0 {
		return nil
	}
	var fields []string
	for _, spec := range meta.ConfigSpec {
		if spec.Type == flow.FieldConnection {
			fields = append(fields, spec.ID)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal(cfg, &values); err != nil {
		return nil
	}
	var ids []string
	for _, field := range fields {
		if id, ok := values[field].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// checkConnection refuses a connection the run's user cannot use. Answers are
// kept for the life of the run, so a node that loops over many messages asks
// the database once.
func (r *Run) checkConnection(id string) error {
	if id == "" {
		return nil
	}
	r.mu.Lock()
	if r.access == nil {
		r.access = make(map[string]error)
	}
	if err, ok := r.access[id]; ok {
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()

	err := access.CheckRunConnection(r.runAs, id)
	r.mu.Lock()
	r.access[id] = err
	r.mu.Unlock()
	return err
}

// checkConfigConnections refuses a node whose resolved config names a
// connection the run's user cannot use, before the node reads it.
func (r *Run) checkConfigConnections(meta NodeMeta, cfg json.RawMessage) error {
	for _, id := range connectionFieldIDs(meta, cfg) {
		if err := r.checkConnection(id); err != nil {
			return err
		}
	}
	return nil
}

// checkTriggerConnections is checkConfigConnections for an armed trigger,
// which has no run.
func checkTriggerConnections(runAs uuid.UUID, meta NodeMeta, cfg json.RawMessage) error {
	for _, id := range connectionFieldIDs(meta, cfg) {
		if err := access.CheckRunConnection(runAs, id); err != nil {
			return fmt.Errorf("cannot arm: %w", err)
		}
	}
	return nil
}
