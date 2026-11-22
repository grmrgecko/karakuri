package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NodeStoreEntry is one persisted key of a node's durable store: the state
// nodes that reason across messages (dedupe history, filter baselines, rate
// limit schedules) keep so a restart or a redeploy does not make every value
// look new again. Rows outlive deploys on purpose and are removed only when
// their flow is deleted.
type NodeStoreEntry struct {
	ID        uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	FlowID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_node_store_key" json:"flow_id" description:"Owning flow ID."`
	NodeID    string    `gorm:"not null;uniqueIndex:idx_node_store_key" json:"node_id" description:"Graph node ID the value belongs to."`
	Key       string    `gorm:"not null;uniqueIndex:idx_node_store_key" json:"key" description:"Store key."`
	Value     string    `gorm:"not null" json:"value" description:"JSON-encoded value."`
	CreatedAt time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time `json:"updated_at" description:"Last update timestamp."`
}

// ListNodeStoreEntries returns every stored entry, for loading the cache at boot.
func ListNodeStoreEntries() ([]*NodeStoreEntry, error) {
	var entries []*NodeStoreEntry
	err := db.DB.Find(&entries).Error
	return entries, err
}

// UpsertNodeStoreEntry writes an entry, replacing any existing value for the
// same flow, node, and key.
func UpsertNodeStoreEntry(flowID uuid.UUID, nodeID, key, value string) error {
	e := &NodeStoreEntry{FlowID: flowID, NodeID: nodeID, Key: key, Value: value}
	return db.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "flow_id"}, {Name: "node_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(e).Error
}

// DeleteNodeStoreEntry removes one entry.
func DeleteNodeStoreEntry(flowID uuid.UUID, nodeID, key string) error {
	// MySQL reserves "key", so the column is named through a map and quoted by
	// the dialect rather than written into the SQL.
	return db.DB.Where(map[string]any{"flow_id": flowID, "node_id": nodeID, "key": key}).
		Delete(&NodeStoreEntry{}).Error
}

// DeleteFlowNodeStore removes every durable store entry belonging to a flow,
// so deleting a flow does not leave its node state behind.
func DeleteFlowNodeStore(tx *gorm.DB, flowID uuid.UUID) error {
	return tx.Where("flow_id = ?", flowID).Delete(&NodeStoreEntry{}).Error
}
