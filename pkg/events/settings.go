package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// settingKey is where the log's settings are stored.
const settingKey = "events"

// Retention defaults and limits.
const (
	DefaultRetentionDays  = 30
	SecurityRetentionDays = 365
	MaxRetentionDays      = 3650
	DefaultMaxRows        = 1_000_000
	MinMaxRows            = 10_000
	MaxMaxRows            = 10_000_000
	MaxHAEntities         = 500
	sweepBatch            = 10_000
	sweepInterval         = 6 * time.Hour
)

// HAEntity is one Home Assistant entity whose changes are logged.
type HAEntity struct {
	ConnectionID string `json:"connection_id" description:"Home Assistant connection UUID."`
	EntityID     string `json:"entity_id" description:"Entity ID, such as binary_sensor.front_door."`
}

// Settings are the log's retention and what it includes.
type Settings struct {
	RetentionDays    map[string]int `json:"retention_days" description:"Days kept per category."`
	MaxRows          int            `json:"max_rows" description:"Most entries kept; the oldest non-security entries go first."`
	ExcludedControls []string       `json:"excluded_controls" description:"Control UUIDs never logged."`
	HAEntities       []HAEntity     `json:"ha_entities" description:"Home Assistant entities logged on state change."`
}

// Defaults returns the settings of a new installation.
func Defaults() Settings {
	days := map[string]int{}
	for _, c := range models.EventCategories {
		days[c] = DefaultRetentionDays
	}
	days[models.EventSecurity] = SecurityRetentionDays
	return Settings{RetentionDays: days, MaxRows: DefaultMaxRows, ExcludedControls: []string{}, HAEntities: []HAEntity{}}
}

var (
	settingsMu sync.RWMutex
	current    = Defaults()
	// onChange is called after the settings change, so sources re-subscribe.
	onChange []func(Settings)
)

// OnSettingsChange registers fn to run after the settings change.
func OnSettingsChange(fn func(Settings)) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	onChange = append(onChange, fn)
}

// Current returns the settings in force.
func Current() Settings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return current
}

// LoadSettings reads the stored settings, filling defaults.
func LoadSettings() (Settings, error) {
	s := Defaults()
	row, err := models.GetSetting(settingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		apply(s)
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(row.Value), &s); err != nil {
		return Defaults(), fmt.Errorf("reading event log settings: %w", err)
	}
	s = withDefaults(s)
	apply(s)
	return s, nil
}

func withDefaults(s Settings) Settings {
	d := Defaults()
	if s.RetentionDays == nil {
		s.RetentionDays = map[string]int{}
	}
	for c, days := range d.RetentionDays {
		if s.RetentionDays[c] == 0 {
			s.RetentionDays[c] = days
		}
	}
	if s.MaxRows == 0 {
		s.MaxRows = d.MaxRows
	}
	if s.ExcludedControls == nil {
		s.ExcludedControls = []string{}
	}
	if s.HAEntities == nil {
		s.HAEntities = []HAEntity{}
	}
	return s
}

// Validate refuses settings outside their limits.
func (s Settings) Validate() error {
	for c, days := range s.RetentionDays {
		known := false
		for _, k := range models.EventCategories {
			known = known || k == c
		}
		if !known {
			return fmt.Errorf("unknown category %q", c)
		}
		if days < 1 || days > MaxRetentionDays {
			return fmt.Errorf("%s retention must be between 1 and %d days", c, MaxRetentionDays)
		}
	}
	if s.MaxRows < MinMaxRows || s.MaxRows > MaxMaxRows {
		return fmt.Errorf("the entry limit must be between %d and %d", MinMaxRows, MaxMaxRows)
	}
	if len(s.HAEntities) > MaxHAEntities {
		return fmt.Errorf("at most %d Home Assistant entities can be logged", MaxHAEntities)
	}
	for _, e := range s.HAEntities {
		if e.ConnectionID == "" || e.EntityID == "" {
			return errors.New("a logged Home Assistant entity needs a connection and an entity ID")
		}
	}
	return nil
}

// SaveSettings validates and stores settings and applies them.
func SaveSettings(s Settings) (Settings, error) {
	s = withDefaults(s)
	if err := s.Validate(); err != nil {
		return s, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	if err := models.SetSetting(settingKey, string(raw)); err != nil {
		return s, err
	}
	apply(s)
	return s, nil
}

func apply(s Settings) {
	settingsMu.Lock()
	current = s
	fns := append([]func(Settings){}, onChange...)
	settingsMu.Unlock()
	for _, fn := range fns {
		fn(s)
	}
}

// Excluded reports whether a control is excluded from the log.
func Excluded(controlID string) bool {
	s := Current()
	for _, id := range s.ExcludedControls {
		if id == controlID {
			return true
		}
	}
	return false
}

// Sweep removes entries past their category's retention, then the oldest
// non-security entries while the log holds more than the limit. Security
// entries are removed only by age, so a burst of house activity never pushes
// out the audit trail.
func Sweep(now time.Time) (int64, error) {
	s := Current()
	var removed int64
	for category, days := range s.RetentionDays {
		res := db.DB.Where("category = ? AND at < ?", category, now.Add(-time.Duration(days)*24*time.Hour).UTC()).Delete(&models.Event{})
		if res.Error != nil {
			return removed, res.Error
		}
		removed += res.RowsAffected
	}
	for {
		var total int64
		if err := db.DB.Model(&models.Event{}).Count(&total).Error; err != nil {
			return removed, err
		}
		over := total - int64(s.MaxRows)
		if over <= 0 {
			return removed, nil
		}
		n := over
		if n > sweepBatch {
			n = sweepBatch
		}
		var ids []uint
		if err := db.DB.Model(&models.Event{}).Where("category <> ?", models.EventSecurity).
			Order("id ASC").Limit(int(n)).Pluck("id", &ids).Error; err != nil {
			return removed, err
		}
		if len(ids) == 0 {
			return removed, nil
		}
		res := db.DB.Where("id IN ?", ids).Delete(&models.Event{})
		if res.Error != nil {
			return removed, res.Error
		}
		removed += res.RowsAffected
	}
}

// RunSweeper sweeps at start and every six hours until stop closes.
func RunSweeper(stop <-chan struct{}, log *zap.Logger) {
	sweep := func() {
		n, err := Sweep(time.Now())
		if err != nil {
			log.Error("removing old event log entries", zap.Error(err))
			return
		}
		if n > 0 {
			log.Debug("removed old event log entries", zap.Int64("count", n))
		}
	}
	sweep()
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			sweep()
		}
	}
}
