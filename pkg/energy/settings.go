// Package energy turns energy and power readings into hourly usage per
// source, prices each hour under the house's tariff, and answers totals by
// source, group, and house for flows, charts, REST, and MCP.
package energy

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
)

// settingsKey is the settings row holding the time zone and week start.
const settingsKey = "energy"

// Settings are the energy feature's house-wide settings.
type Settings struct {
	Timezone  string `json:"timezone" description:"IANA time zone hours and tariffs follow; empty is the server's local zone."`
	WeekStart int    `json:"week_start" description:"First day of the week, ISO 1 Monday to 7 Sunday."`
}

var (
	settingsMu sync.RWMutex
	current    = Settings{WeekStart: 1}
	location   = time.Local
)

// Validate checks settings before they are saved.
func (s Settings) Validate() error {
	if s.WeekStart < 1 || s.WeekStart > 7 {
		return fmt.Errorf("week_start must be 1 (Monday) to 7 (Sunday)")
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", s.Timezone)
		}
	}
	return nil
}

// LoadSettings reads the stored settings, keeping the defaults when none
// are stored.
func LoadSettings() error {
	row, err := models.GetSetting(settingsKey)
	if err != nil {
		return nil
	}
	var s Settings
	if err := json.Unmarshal([]byte(row.Value), &s); err != nil {
		return fmt.Errorf("reading energy settings: %w", err)
	}
	if s.WeekStart == 0 {
		s.WeekStart = 1
	}
	apply(s)
	return nil
}

// SaveSettings validates, stores, and applies settings.
func SaveSettings(s Settings) error {
	if s.WeekStart == 0 {
		s.WeekStart = 1
	}
	if err := s.Validate(); err != nil {
		return err
	}
	raw, _ := json.Marshal(s)
	if err := models.SetSetting(settingsKey, string(raw)); err != nil {
		return err
	}
	apply(s)
	return nil
}

func apply(s Settings) {
	loc := time.Local
	if s.Timezone != "" {
		if l, err := time.LoadLocation(s.Timezone); err == nil {
			loc = l
		}
	}
	settingsMu.Lock()
	current, location = s, loc
	settingsMu.Unlock()
}

// CurrentSettings returns the settings in force.
func CurrentSettings() Settings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return current
}

// Location returns the time zone hours and tariffs follow.
func Location() *time.Location {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return location
}

// HourStart returns the start of the local hour holding t. It subtracts the
// local minutes and seconds rather than rebuilding the time from its fields,
// so the repeated hour of a daylight saving change keeps both of its
// instances and half-hour zones start hours on their own half hour.
func HourStart(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)
	return lt.Add(-time.Duration(lt.Minute())*time.Minute - time.Duration(lt.Second())*time.Second -
		time.Duration(lt.Nanosecond())).UTC()
}

// LocalDate is the local calendar date of t, YYYY-MM-DD.
func LocalDate(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}

// DayStart returns the start of the local day holding t.
func DayStart(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
}

// AddDays moves a local day start by n days, keeping local midnight across
// daylight saving changes.
func AddDays(day time.Time, n int, loc *time.Location) time.Time {
	lt := day.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day()+n, 0, 0, 0, 0, loc)
}

// WeekStartOf returns the start of the local week holding t.
func WeekStartOf(t time.Time, loc *time.Location, weekStart int) time.Time {
	day := DayStart(t, loc)
	iso := int(day.Weekday())
	if iso == 0 {
		iso = 7
	}
	back := (iso - weekStart + 7) % 7
	return AddDays(day, -back, loc)
}

// MonthStartOf returns the start of the local month holding t.
func MonthStartOf(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)
	return time.Date(lt.Year(), lt.Month(), 1, 0, 0, 0, 0, loc)
}
