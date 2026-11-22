package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Variable is a persisted author-facing variable in the flow or global scope.
// Run and message scopes are not stored here: they live only as long as the
// run or the message that carries them.
//
// FlowID is uuid.Nil for the global scope rather than NULL, so the uniqueness
// index actually constrains global keys: SQLite treats NULLs as distinct and
// would happily accept duplicates.
type Variable struct {
	ID        uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Scope     string    `gorm:"not null;uniqueIndex:idx_variable_key" json:"scope" description:"Variable scope: flow or global."`
	FlowID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_variable_key" json:"flow_id" description:"Owning flow ID; the nil UUID for global variables."`
	Key       string    `gorm:"not null;uniqueIndex:idx_variable_key" json:"key" description:"Variable name."`
	Value     string    `gorm:"not null" json:"value" description:"JSON-encoded value."`
	CreatedAt time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time `json:"updated_at" description:"Last update timestamp."`
}

// ListVariables returns every stored variable, for loading the cache at boot.
func ListVariables() ([]*Variable, error) {
	var vars []*Variable
	err := db.DB.Find(&vars).Error
	return vars, err
}

// UpsertVariable writes a variable, replacing any existing value for the same
// scope, flow, and key.
func UpsertVariable(scope string, flowID uuid.UUID, key, value string) error {
	v := &Variable{Scope: scope, FlowID: flowID, Key: key, Value: value}
	return db.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "scope"}, {Name: "flow_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(v).Error
}

// DeleteVariable removes one variable.
func DeleteVariable(scope string, flowID uuid.UUID, key string) error {
	// MySQL reserves "key", so the column is named through a map and quoted by
	// the dialect rather than written into the SQL.
	return db.DB.Where(map[string]any{"scope": scope, "flow_id": flowID, "key": key}).
		Delete(&Variable{}).Error
}

// DeleteFlowVariables removes every variable belonging to a flow, so deleting
// a flow does not leave its variables behind.
func DeleteFlowVariables(tx *gorm.DB, flowID uuid.UUID) error {
	return tx.Where("scope = ? AND flow_id = ?", "flow", flowID).Delete(&Variable{}).Error
}
