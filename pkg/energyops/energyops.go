// Package energyops is the service layer of energy tracking: sources,
// groups, tariffs, settings, rollups, and prices. REST, MCP, and the settings
// page call it, so they share one set of rules. Callers check permissions:
// reading needs connection read, changing needs connection management.
package energyops

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/energy"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"gorm.io/gorm"
)

// Limits on configuration.
const (
	maxSources = 200
	maxGroups  = 50
	minGapS    = 60
	maxGapS    = 7 * 24 * 3600
)

// SourceInput is a source as an author saves it.
type SourceInput struct {
	Name        string  `json:"name" required:"true" description:"Display name."`
	SourceKind  string  `json:"source_kind" required:"true" enum:"control,device,ha" description:"control, device, or ha."`
	SourceID    string  `json:"source_id" required:"true" description:"Control or device UUID, or the Home Assistant connection UUID."`
	EntityID    string  `json:"entity_id,omitempty" description:"Home Assistant entity, for ha sources."`
	Attribute   string  `json:"attribute,omitempty" description:"Attribute to read; empty is the primary value or the entity state. Devices default to value."`
	Measurement string  `json:"measurement" required:"true" enum:"energy,power" description:"energy for a cumulative meter, power for watts."`
	Unit        string  `json:"unit" required:"true" description:"Wh, kWh, or MWh for energy; W, kW, or MW for power."`
	Role        string  `json:"role" required:"true" enum:"consumer,house,import,export,production" description:"What the source measures."`
	GapLimitS   int     `json:"gap_limit_s,omitempty" description:"Longest silence bridged in seconds, 60 to 604800; default 900 for power, 21600 for energy."`
	MaxKW       float64 `json:"max_kw,omitempty" description:"Plausible maximum draw in kW; default 25."`
}

// SourceView is a source with today's usage.
type SourceView struct {
	*models.EnergySource
	TodayKWh float64                `json:"today_kwh" description:"Usage since local midnight."`
	GroupIDs []uuid.UUID            `json:"group_ids" description:"Groups the source is in now."`
	Import   *energy.ImportProgress `json:"import,omitempty" description:"The latest history import since the server started."`
}

func validation(msg string, args ...any) error {
	return apperrors.ErrValidation.WithMessage(fmt.Sprintf(msg, args...))
}

func reload() {
	if energy.R != nil {
		energy.R.Reload()
	}
}

// ListSources returns the live sources with today's usage.
func ListSources() ([]SourceView, error) {
	if energy.R != nil {
		energy.R.Flush()
	}
	list, err := models.ListEnergySources()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	loc := energy.Location()
	now := time.Now()
	hours, err := models.EnergyHoursBetween(nil, energy.DayStart(now, loc), now.Add(time.Hour))
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	today := map[uuid.UUID]float64{}
	for _, h := range hours {
		today[h.SourceID] += h.KWh
	}
	members, err := models.ListEnergyGroupMembers()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	groups := map[uuid.UUID][]uuid.UUID{}
	for _, m := range members {
		if m.LeftAt == nil {
			groups[m.SourceID] = append(groups[m.SourceID], m.GroupID)
		}
	}
	imports := energy.Imports()
	out := make([]SourceView, 0, len(list))
	for _, s := range list {
		ids := groups[s.ID]
		if ids == nil {
			ids = []uuid.UUID{}
		}
		v := SourceView{EnergySource: s, TodayKWh: today[s.ID], GroupIDs: ids}
		if p, ok := imports[s.ID]; ok {
			v.Import = &p
		}
		out = append(out, v)
	}
	return out, nil
}

// SaveSource creates a source when id is nil, or edits one.
func SaveSource(id uuid.UUID, in SourceInput) (*models.EnergySource, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, validation("name is required")
	}
	switch in.SourceKind {
	case models.EnergyKindControl, models.EnergyKindDevice:
		if _, err := uuid.Parse(in.SourceID); err != nil {
			return nil, validation("source_id must be the %s's UUID", in.SourceKind)
		}
	case models.EnergyKindHA:
		if _, err := uuid.Parse(in.SourceID); err != nil {
			return nil, validation("source_id must be the Home Assistant connection's UUID")
		}
		if !strings.Contains(in.EntityID, ".") {
			return nil, validation("entity_id must name a Home Assistant entity such as sensor.house_energy")
		}
	default:
		return nil, validation("source_kind must be control, device, or ha")
	}
	if in.Measurement != models.EnergyMeasureEnergy && in.Measurement != models.EnergyMeasurePower {
		return nil, validation("measurement must be energy or power")
	}
	if _, err := energy.UnitFactor(in.Measurement, in.Unit); err != nil {
		return nil, validation("%s", err.Error())
	}
	switch in.Role {
	case models.EnergyRoleConsumer, models.EnergyRoleHouse, models.EnergyRoleImport, models.EnergyRoleExport, models.EnergyRoleProduction:
	default:
		return nil, validation("role must be consumer, house, import, export, or production")
	}
	if in.GapLimitS != 0 && (in.GapLimitS < minGapS || in.GapLimitS > maxGapS) {
		return nil, validation("gap_limit_s must be %d to %d seconds", minGapS, maxGapS)
	}
	if in.MaxKW < 0 {
		return nil, validation("max_kw must be positive")
	}
	list, err := models.ListEnergySources()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	for _, s := range list {
		if s.ID == id {
			continue
		}
		if strings.EqualFold(s.Name, in.Name) {
			return nil, apperrors.ErrConflict.WithMessage("another source is named " + in.Name)
		}
		if in.Role == models.EnergyRoleHouse && s.Role == models.EnergyRoleHouse {
			return nil, validation("%s is already the whole-house source; there can be one", s.Name)
		}
	}
	var s *models.EnergySource
	if id == uuid.Nil {
		if len(list) >= maxSources {
			return nil, validation("at most %d energy sources", maxSources)
		}
		s = &models.EnergySource{State: energy.StateWaiting}
	} else {
		s, err = models.FindEnergySource(id)
		if err != nil || s.DeletedAt != nil {
			return nil, apperrors.ErrNotFound.WithMessage("energy source not found")
		}
		if s.SourceKind != in.SourceKind || !strings.EqualFold(s.SourceID, in.SourceID) || s.EntityID != in.EntityID ||
			s.Attribute != in.Attribute || s.Measurement != in.Measurement || !strings.EqualFold(s.Unit, in.Unit) {
			// It reads something else now; its last reading means nothing.
			s.LastValue, s.LastAt, s.State, s.StateNote = nil, nil, energy.StateWaiting, ""
		}
	}
	s.Name, s.SourceKind, s.SourceID, s.EntityID, s.Attribute = in.Name, in.SourceKind, strings.ToLower(in.SourceID), in.EntityID, in.Attribute
	s.Measurement, s.Unit, s.Role, s.GapLimitS, s.MaxKW = in.Measurement, in.Unit, in.Role, in.GapLimitS, in.MaxKW
	if err := db.DB.Save(s).Error; err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	reload()
	return s, nil
}

// DeleteSource marks a source deleted; its hours go 30 days later.
func DeleteSource(id uuid.UUID) error {
	s, err := models.FindEnergySource(id)
	if err != nil || s.DeletedAt != nil {
		return apperrors.ErrNotFound.WithMessage("energy source not found")
	}
	now := time.Now()
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(s).Update("deleted_at", now).Error; err != nil {
			return err
		}
		return tx.Model(&models.EnergyGroupMember{}).Where("source_id = ? AND left_at IS NULL", id).Update("left_at", now).Error
	})
	if err != nil {
		return apperrors.ErrInternal.WithError(err)
	}
	reload()
	return nil
}

// GroupInput is a group as an author saves it.
type GroupInput struct {
	Name      string      `json:"name" required:"true" description:"Display name."`
	SortOrder int         `json:"sort_order,omitempty" description:"Position in lists."`
	SourceIDs []uuid.UUID `json:"source_ids,omitempty" description:"Sources in the group."`
}

// GroupView is a group with its current members.
type GroupView struct {
	*models.EnergyGroup
	SourceIDs []uuid.UUID `json:"source_ids" description:"Sources in the group now."`
}

// ListGroups returns the groups with their members.
func ListGroups() ([]GroupView, error) {
	groups, err := models.ListEnergyGroups()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	members, err := models.ListEnergyGroupMembers()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	out := make([]GroupView, 0, len(groups))
	for _, g := range groups {
		v := GroupView{EnergyGroup: g, SourceIDs: []uuid.UUID{}}
		for _, m := range members {
			if m.GroupID == g.ID && m.LeftAt == nil {
				v.SourceIDs = append(v.SourceIDs, m.SourceID)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// SaveGroup creates or edits a group. A membership change ends or starts a
// membership now, so past hours keep their grouping.
func SaveGroup(id uuid.UUID, in GroupInput) (*GroupView, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, validation("name is required")
	}
	groups, err := models.ListEnergyGroups()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	var g *models.EnergyGroup
	for _, x := range groups {
		if x.ID == id {
			g = x
		} else if strings.EqualFold(x.Name, in.Name) {
			return nil, apperrors.ErrConflict.WithMessage("another group is named " + in.Name)
		}
	}
	if id == uuid.Nil {
		if len(groups) >= maxGroups {
			return nil, validation("at most %d energy groups", maxGroups)
		}
		g = &models.EnergyGroup{}
	} else if g == nil {
		return nil, apperrors.ErrNotFound.WithMessage("energy group not found")
	}
	want := map[uuid.UUID]bool{}
	for _, sid := range in.SourceIDs {
		s, err := models.FindEnergySource(sid)
		if err != nil || s.DeletedAt != nil {
			return nil, validation("no energy source %s", sid)
		}
		want[sid] = true
	}
	g.Name, g.SortOrder = in.Name, in.SortOrder
	now := time.Now()
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(g).Error; err != nil {
			return err
		}
		var current []models.EnergyGroupMember
		if err := tx.Where("group_id = ? AND left_at IS NULL", g.ID).Find(&current).Error; err != nil {
			return err
		}
		have := map[uuid.UUID]bool{}
		for _, m := range current {
			have[m.SourceID] = true
			if !want[m.SourceID] {
				if err := tx.Model(&models.EnergyGroupMember{}).Where("id = ?", m.ID).Update("left_at", now).Error; err != nil {
					return err
				}
			}
		}
		for sid := range want {
			if !have[sid] {
				if err := tx.Create(&models.EnergyGroupMember{GroupID: g.ID, SourceID: sid, JoinedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	ids := make([]uuid.UUID, 0, len(want))
	for sid := range want {
		ids = append(ids, sid)
	}
	return &GroupView{EnergyGroup: g, SourceIDs: ids}, nil
}

// DeleteGroup removes a group and its membership history.
func DeleteGroup(id uuid.UUID) error {
	res := db.DB.Delete(&models.EnergyGroup{}, "id = ?", id)
	if res.Error != nil {
		return apperrors.ErrInternal.WithError(res.Error)
	}
	if res.RowsAffected == 0 {
		return apperrors.ErrNotFound.WithMessage("energy group not found")
	}
	db.DB.Where("group_id = ?", id).Delete(&models.EnergyGroupMember{})
	return nil
}

// TariffResult is a saved tariff and the hours repriced.
type TariffResult struct {
	Tariff          *models.EnergyTariff `json:"tariff" description:"The tariff as saved."`
	HoursRecomputed int                  `json:"hours_recomputed" description:"Recorded hours repriced."`
}

// ListTariffs returns the tariffs by effective date.
func ListTariffs() ([]*models.EnergyTariff, error) {
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	return list, nil
}

// SaveTariff validates and saves a tariff, then reprices the hours it
// affects.
func SaveTariff(id uuid.UUID, t models.EnergyTariff) (*TariffResult, error) {
	if err := energy.ValidateTariff(&t); err != nil {
		return nil, validation("%s", err.Error())
	}
	if c := t.Import.V.ControlID; c != "" {
		if _, ok := energy.ControlValue(c); !ok {
			return nil, validation("rate control %s is not a number control with a value", c)
		}
	}
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	from := t.EffectiveFrom
	var existing *models.EnergyTariff
	for _, x := range list {
		if x.ID == id {
			existing = x
			continue
		}
		if x.EffectiveFrom == t.EffectiveFrom {
			return nil, apperrors.ErrConflict.WithMessage("tariff " + x.Name + " already starts on " + t.EffectiveFrom)
		}
	}
	if id != uuid.Nil {
		if existing == nil {
			return nil, apperrors.ErrNotFound.WithMessage("energy tariff not found")
		}
		if existing.EffectiveFrom < from {
			from = existing.EffectiveFrom
		}
		t.ID, t.CreatedAt = existing.ID, existing.CreatedAt
	} else {
		t.ID = uuid.Nil
	}
	if err := db.DB.Save(&t).Error; err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	n, err := recomputeFrom(from)
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	return &TariffResult{Tariff: &t, HoursRecomputed: n}, nil
}

// DeleteTariff removes a tariff and reprices its hours under whatever
// covers them now.
func DeleteTariff(id uuid.UUID) (int, error) {
	var t models.EnergyTariff
	if err := db.DB.First(&t, "id = ?", id).Error; err != nil {
		return 0, apperrors.ErrNotFound.WithMessage("energy tariff not found")
	}
	if err := db.DB.Delete(&t).Error; err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	n, err := recomputeFrom(t.EffectiveFrom)
	if err != nil {
		return 0, apperrors.ErrInternal.WithError(err)
	}
	return n, nil
}

func recomputeFrom(date string) (int, error) {
	day, err := time.ParseInLocation("2006-01-02", date, energy.Location())
	if err != nil {
		return 0, err
	}
	return energy.RecomputeRates(day, time.Now())
}

// GetSettings returns the energy settings.
func GetSettings() energy.Settings { return energy.CurrentSettings() }

// SaveSettings stores the energy settings.
func SaveSettings(s energy.Settings) (energy.Settings, error) {
	if err := energy.SaveSettings(s); err != nil {
		return energy.Settings{}, validation("%s", err.Error())
	}
	reload()
	return energy.CurrentSettings(), nil
}

// RollupQuery asks for a target's figures.
type RollupQuery struct {
	Target   string   `json:"target,omitempty" query:"target" description:"house, untracked, solar, group:<id>, or source:<id>; default house."`
	Measures []string `json:"measures,omitempty" query:"measures" description:"Measures, default usage: usage, cost, export_credit, daily_charge, total_cost, import, export, production, self_consumption, solar_share."`
	From     string   `json:"from,omitempty" query:"from" description:"Range start: now/d, now-30d, RFC 3339, or Unix milliseconds. Default now/d."`
	To       string   `json:"to,omitempty" query:"to" description:"Range end, default now."`
	Period   string   `json:"period,omitempty" query:"period" description:"hour, day, week, or month; default day."`
}

// Rollups computes a target's figures.
func Rollups(q RollupQuery) (*energy.Rollup, error) {
	if q.Target == "" {
		q.Target = energy.TargetHouse
	}
	if q.Period == "" {
		q.Period = energy.PeriodDay
	}
	if q.From == "" {
		q.From = "now/d"
	}
	if q.To == "" {
		q.To = "now"
	}
	var measures []string
	for _, m := range q.Measures {
		for _, part := range strings.Split(m, ",") {
			if part = strings.TrimSpace(part); part != "" {
				measures = append(measures, part)
			}
		}
	}
	r, err := tsdb.ParseRangeWeek(q.From, q.To, time.Now().In(energy.Location()), energy.CurrentSettings().WeekStart)
	if err != nil {
		return nil, validation("%s", err.Error())
	}
	roll, err := energy.Compute(q.Target, measures, r.From, r.To, q.Period)
	if err != nil {
		return nil, validation("%s", err.Error())
	}
	return roll, nil
}

// PriceView is the price at one time.
type PriceView struct {
	At       time.Time  `json:"at" description:"Time asked about."`
	Rate     float64    `json:"rate" description:"Import rate per kWh."`
	Export   *float64   `json:"export_rate,omitempty" description:"Export credit per kWh."`
	Currency string     `json:"currency" description:"Currency code."`
	Period   string     `json:"period,omitempty" description:"Time-of-use period name."`
	EndsAt   *time.Time `json:"ends_at,omitempty" description:"When the price next changes, within a week."`
}

func tariffs() (energy.Tariffs, error) {
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return nil, apperrors.ErrInternal.WithError(err)
	}
	return energy.SortTariffs(list), nil
}

// Price returns the price at a time, now when at is zero.
func Price(at time.Time) (*PriceView, error) {
	ts, err := tariffs()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if at.IsZero() {
		at = now
	}
	loc := energy.Location()
	p, err := ts.PriceAt(at, loc, energy.ControlValue, at.After(now.Add(time.Minute)))
	if err != nil {
		return nil, priceError(err)
	}
	v := &PriceView{At: at, Rate: p.Import, Export: p.Export, Currency: p.Currency, Period: p.Period}
	if p.Tariff.Import.V.ControlID == "" {
		if next, _, err := ts.NextChange(at, loc); err == nil {
			v.EndsAt = &next
		}
	}
	return v, nil
}

// Cheapest finds the cheapest window of hours within a range.
func Cheapest(length time.Duration, from, to string) (*energy.Window, error) {
	ts, err := tariffs()
	if err != nil {
		return nil, err
	}
	if from == "" {
		from = "now"
	}
	if to == "" {
		to = "now+24h"
	}
	r, err := tsdb.ParseRangeWeek(from, to, time.Now().In(energy.Location()), energy.CurrentSettings().WeekStart)
	if err != nil {
		return nil, validation("%s", err.Error())
	}
	w, err := ts.Cheapest(length, r.From, r.To, energy.Location())
	if err != nil {
		return nil, priceError(err)
	}
	return &w, nil
}

func priceError(err error) error {
	if errors.Is(err, energy.ErrNoTariff) || errors.Is(err, energy.ErrFutureUnknown) {
		return apperrors.ErrUnprocessable.WithMessage(err.Error())
	}
	return validation("%s", err.Error())
}

// Import starts reading up to days of a source's history in the background.
func Import(id uuid.UUID, days int) error {
	s, err := models.FindEnergySource(id)
	if err != nil || s.DeletedAt != nil {
		return apperrors.ErrNotFound.WithMessage("energy source not found")
	}
	if err := energy.StartImport(s, days, nil); err != nil {
		return validation("%s", err.Error())
	}
	return nil
}
