package models

import (
	"strings"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Setting stores a configuration value that persists across restarts and can
// be changed without editing the config file. Keys use dot notation; plugin
// settings live under plugins.{plugin}.{key}, which is the namespace a plugin
// reads and writes through the host callback channel.
type Setting struct {
	ID        uint      `gorm:"primaryKey" json:"id" description:"Unique identifier."`
	Key       string    `gorm:"uniqueIndex;not null" json:"key" description:"Dot-notation configuration key."`
	Value     string    `gorm:"not null" json:"value" description:"Configuration value."`
	CreatedAt time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time `json:"updated_at" description:"Last update timestamp."`
}

// The column is named "key", which MySQL reserves and Postgres accepts only
// quoted its own way, so every query names it through a clause and lets the
// dialect quote it. A backtick-quoted literal is a syntax error on Postgres.
var settingKey = clause.Column{Name: "key"}

// GetSetting returns a stored setting by key.
func GetSetting(key string) (*Setting, error) {
	var setting Setting
	err := db.DB.Where(clause.Eq{Column: settingKey, Value: key}).First(&setting).Error
	return &setting, err
}

// SetSetting creates or updates a setting by key.
func SetSetting(key, value string) error {
	return SetSettingIn(db.DB, key, value)
}

// SetSettingIn creates or updates a setting inside a transaction, so a
// rewrite of several sealed values lands together or not at all.
func SetSettingIn(tx *gorm.DB, key, value string) error {
	var setting Setting
	if err := tx.Where(clause.Eq{Column: settingKey, Value: key}).First(&setting).Error; err != nil {
		return tx.Create(&Setting{Key: key, Value: value}).Error
	}
	setting.Value = value
	return tx.Save(&setting).Error
}

// DeleteSetting removes a setting by key.
func DeleteSetting(key string) error {
	return db.DB.Where(clause.Eq{Column: settingKey, Value: key}).Delete(&Setting{}).Error
}

// settingPrefix matches the keys that start with prefix. LIKE wildcards in
// the prefix are escaped with !, which every engine reads the same way,
// because plugin names hold underscores and a prefix must not match a
// neighbouring name.
func settingPrefix(prefix string) clause.Expr {
	pattern := strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`).Replace(prefix) + "%"
	return clause.Expr{SQL: "? LIKE ? ESCAPE '!'", Vars: []any{settingKey, pattern}}
}

// ListSettingsByPrefix returns settings whose key starts with the prefix,
// ordered by key. Reading one plugin's whole namespace is what the settings
// API and the plugin settings form both need.
func ListSettingsByPrefix(prefix string) ([]*Setting, error) {
	var settings []*Setting
	err := db.DB.Where(settingPrefix(prefix)).
		Order(clause.OrderByColumn{Column: settingKey}).Find(&settings).Error
	return settings, err
}

// DeleteSettingsByPrefix removes every setting under a prefix, used when a
// plugin disappears from disk.
func DeleteSettingsByPrefix(prefix string) error {
	return db.DB.Where(settingPrefix(prefix)).Delete(&Setting{}).Error
}
