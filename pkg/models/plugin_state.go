package models

import (
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
)

// Plugin load states. A plugin is unloaded until its contributions are
// applied, loaded once they are, and errored when applying them failed or a
// plugin process exited on its own.
const (
	PluginStatusUnloaded = "unloaded"
	PluginStatusLoaded   = "loaded"
	PluginStatusError    = "error"
)

// PluginState is what the host remembers about a plugin between restarts: the
// operator's enable choice, plus the last known load result and the manifest
// metadata, so the settings panel can describe a plugin that is not running.
type PluginState struct {
	ID   uint   `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Name string `gorm:"uniqueIndex;not null" json:"name" description:"Plugin name from its manifest."`
	Path string `json:"path" description:"Plugin directory on disk."`
	// Enabled carries no GORM default: a default tag makes Create drop an
	// explicit false, which is how a defaultDisabled plugin seeds its row.
	Enabled     bool      `json:"enabled" description:"Whether the plugin loads at startup."`
	Status      string    `json:"status" description:"Last known load state."`
	ErrorMsg    string    `json:"error_message,omitempty" description:"Why the last load failed."`
	Version     string    `json:"version,omitempty" description:"Plugin version."`
	Description string    `json:"description,omitempty" description:"What the plugin contributes."`
	Author      string    `json:"author,omitempty" description:"Plugin author."`
	CreatedAt   time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time `json:"updated_at" description:"Last update timestamp."`
}

// GetPluginState returns a plugin's stored state by name.
func GetPluginState(name string) (*PluginState, error) {
	var state PluginState
	err := db.DB.Where("name = ?", name).First(&state).Error
	return &state, err
}

// ListPluginStates returns every stored plugin state ordered by name.
func ListPluginStates() ([]*PluginState, error) {
	var states []*PluginState
	err := db.DB.Order("name ASC").Find(&states).Error
	return states, err
}

// SavePluginState writes a plugin state row.
func SavePluginState(state *PluginState) error {
	return db.DB.Save(state).Error
}

// DeletePluginState removes a plugin's stored state.
func DeletePluginState(name string) error {
	return db.DB.Where("name = ?", name).Delete(&PluginState{}).Error
}
