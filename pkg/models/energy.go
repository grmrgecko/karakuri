package models

import (
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Energy source kinds: what a source reads.
const (
	EnergyKindControl = "control"
	EnergyKindDevice  = "device"
	EnergyKindHA      = "ha"
)

// Energy measurements.
const (
	EnergyMeasureEnergy = "energy"
	EnergyMeasurePower  = "power"
)

// Energy source roles.
const (
	EnergyRoleConsumer   = "consumer"
	EnergyRoleHouse      = "house"
	EnergyRoleImport     = "import"
	EnergyRoleExport     = "export"
	EnergyRoleProduction = "production"
)

// Hour qualities.
const (
	EnergyQualityLive    = "live"
	EnergyQualityPartial = "partial"
	EnergyQualityFilled  = "filled"
	EnergyQualityCarried = "carried"
)

// EnergySource is one meter or plug whose readings become hourly usage.
type EnergySource struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name        string     `gorm:"not null" json:"name" description:"Display name."`
	SourceKind  string     `gorm:"not null" json:"source_kind" description:"control, device, or ha."`
	SourceID    string     `gorm:"not null;index" json:"source_id" description:"Control or device UUID, or the Home Assistant connection UUID."`
	EntityID    string     `json:"entity_id,omitempty" description:"Home Assistant entity, for ha sources."`
	Attribute   string     `json:"attribute,omitempty" description:"Attribute read; empty is the primary value or the entity state."`
	Measurement string     `gorm:"not null" json:"measurement" description:"energy (a cumulative meter) or power."`
	Unit        string     `gorm:"not null" json:"unit" description:"Wh, kWh, or MWh for energy; W or kW for power."`
	Role        string     `gorm:"not null" json:"role" description:"consumer, house, import, export, or production."`
	GapLimitS   int        `json:"gap_limit_s" description:"Longest silence bridged, in seconds."`
	MaxKW       float64    `json:"max_kw" description:"Plausible maximum draw in kW; faster growth is rejected."`
	LastValue   *float64   `json:"last_value" description:"Last accepted reading in the source's unit."`
	LastAt      *time.Time `json:"last_at" description:"When the last reading was taken."`
	State       string     `json:"state" description:"ok, gap, reset, rejected, unavailable, or waiting."`
	StateNote   string     `json:"state_note,omitempty" description:"The last rejected reading or the reason for the state."`
	DeletedAt   *time.Time `gorm:"index" json:"deleted_at,omitempty" description:"When the source was deleted; its hours are purged 30 days later."`
	CreatedAt   time.Time  `json:"created_at" description:"Creation timestamp."`
	UpdatedAt   time.Time  `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (s *EnergySource) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

// EnergyGroup is a named set of sources, such as a room or circuit.
type EnergyGroup struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name      string    `gorm:"not null" json:"name" description:"Display name."`
	SortOrder int       `json:"sort_order" description:"Position in lists."`
	CreatedAt time.Time `json:"created_at" description:"Creation timestamp."`
	UpdatedAt time.Time `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (g *EnergyGroup) BeforeCreate(tx *gorm.DB) error {
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	return nil
}

// EnergyGroupMember is one stretch of a source's membership of a group. A
// group's hour sums the sources that were members then, so moving a source
// changes only later hours.
type EnergyGroupMember struct {
	ID       uint       `gorm:"primaryKey" json:"id"`
	GroupID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"group_id"`
	SourceID uuid.UUID  `gorm:"type:uuid;not null;index" json:"source_id"`
	JoinedAt time.Time  `gorm:"not null" json:"joined_at"`
	LeftAt   *time.Time `json:"left_at,omitempty"`
}

// EnergyRateSpec is a tariff's rate: a flat rate, periods by weekday and
// time, or a number control a flow keeps current.
type EnergyRateSpec struct {
	Flat      *float64       `json:"flat,omitempty" description:"Rate per kWh at every hour."`
	Periods   []EnergyPeriod `json:"periods,omitempty" description:"Time-of-use periods covering the whole week."`
	ControlID string         `json:"control_id,omitempty" description:"Number control holding the rate per kWh."`
}

// EnergyPeriod is one time-of-use period.
type EnergyPeriod struct {
	Name  string  `json:"name" description:"Period name, such as peak."`
	Days  []int   `json:"days" description:"ISO weekdays, 1 Monday to 7 Sunday."`
	Start string  `json:"start" description:"Local start time HH:MM."`
	End   string  `json:"end" description:"Local end time HH:MM; 00:00 is midnight at the end of the day."`
	Rate  float64 `json:"rate" description:"Rate per kWh."`
}

// EnergyTariff is the house's price from its effective date until the next
// tariff's.
type EnergyTariff struct {
	ID            uuid.UUID                   `gorm:"type:uuid;primaryKey" json:"id" description:"Unique identifier."`
	Name          string                      `gorm:"not null" json:"name" description:"Display name."`
	Currency      string                      `gorm:"not null" json:"currency" description:"ISO 4217 currency code."`
	EffectiveFrom string                      `gorm:"not null;uniqueIndex" json:"effective_from" description:"Local date YYYY-MM-DD the tariff starts."`
	Import        JSONColumn[EnergyRateSpec]  `gorm:"type:text" json:"import" description:"Import rate."`
	Export        JSONColumn[*EnergyRateSpec] `gorm:"type:text" json:"export" description:"Export credit, optional."`
	DailyCharge   float64                     `json:"daily_charge" description:"Fixed charge per day."`
	CreatedAt     time.Time                   `json:"created_at" description:"Creation timestamp."`
	UpdatedAt     time.Time                   `json:"updated_at" description:"Last update timestamp."`
}

// BeforeCreate assigns a UUID when none is set.
func (t *EnergyTariff) BeforeCreate(tx *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

// EnergyHour is one source's usage in one hour.
type EnergyHour struct {
	SourceID  uuid.UUID `gorm:"type:uuid;primaryKey" json:"source_id"`
	Hour      time.Time `gorm:"primaryKey" json:"hour"`
	LocalDate string    `gorm:"not null;index" json:"local_date"`
	KWh       float64   `gorm:"column:kwh" json:"kwh"`
	Quality   string    `json:"quality"`
}

// EnergyRate is the house's price in one hour.
type EnergyRate struct {
	Hour       time.Time  `gorm:"primaryKey" json:"hour"`
	LocalDate  string     `gorm:"not null;index" json:"local_date"`
	TariffID   *uuid.UUID `gorm:"type:uuid" json:"tariff_id"`
	Currency   string     `json:"currency"`
	ImportRate float64    `json:"import_rate"`
	ExportRate *float64   `json:"export_rate"`
	Period     string     `json:"period"`
}

// ListEnergySources returns the live sources by name.
func ListEnergySources() ([]*EnergySource, error) {
	var out []*EnergySource
	err := db.DB.Where("deleted_at IS NULL").Order("name").Find(&out).Error
	return out, err
}

// FindEnergySource loads a source, deleted or not.
func FindEnergySource(id uuid.UUID) (*EnergySource, error) {
	var s EnergySource
	err := db.DB.First(&s, "id = ?", id).Error
	return &s, err
}

// ListEnergyGroups returns the groups in order.
func ListEnergyGroups() ([]*EnergyGroup, error) {
	var out []*EnergyGroup
	err := db.DB.Order("sort_order, name").Find(&out).Error
	return out, err
}

// ListEnergyGroupMembers returns every membership stretch.
func ListEnergyGroupMembers() ([]*EnergyGroupMember, error) {
	var out []*EnergyGroupMember
	err := db.DB.Order("id").Find(&out).Error
	return out, err
}

// ListEnergyTariffs returns the tariffs by effective date.
func ListEnergyTariffs() ([]*EnergyTariff, error) {
	var out []*EnergyTariff
	err := db.DB.Order("effective_from").Find(&out).Error
	return out, err
}

// UpsertEnergyHours writes hours, replacing a source's earlier figure for
// the same hour.
func UpsertEnergyHours(tx *gorm.DB, hours []EnergyHour) error {
	if len(hours) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_id"}, {Name: "hour"}},
		DoUpdates: clause.AssignmentColumns([]string{"local_date", "kwh", "quality"}),
	}).Create(&hours).Error
}

// UpsertEnergyRates writes hourly prices.
func UpsertEnergyRates(tx *gorm.DB, rates []EnergyRate) error {
	if len(rates) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "hour"}},
		DoUpdates: clause.AssignmentColumns([]string{"local_date", "tariff_id", "currency", "import_rate", "export_rate", "period"}),
	}).CreateInBatches(&rates, 500).Error
}

// EnergyHoursBetween returns hours of the given sources in [from, to).
// No sources means every source.
func EnergyHoursBetween(sources []uuid.UUID, from, to time.Time) ([]EnergyHour, error) {
	var out []EnergyHour
	q := db.DB.Where("hour >= ? AND hour < ?", from.UTC(), to.UTC())
	if len(sources) > 0 {
		q = q.Where("source_id IN ?", sources)
	}
	err := q.Order("hour").Find(&out).Error
	return out, err
}

// EnergyRatesBetween returns hourly prices in [from, to).
func EnergyRatesBetween(from, to time.Time) ([]EnergyRate, error) {
	var out []EnergyRate
	err := db.DB.Where("hour >= ? AND hour < ?", from.UTC(), to.UTC()).Order("hour").Find(&out).Error
	return out, err
}

// EarliestEnergyHour returns the first recorded hour, false when none.
func EarliestEnergyHour() (time.Time, bool) {
	var h EnergyHour
	if err := db.DB.Order("hour").First(&h).Error; err != nil {
		return time.Time{}, false
	}
	return h.Hour, true
}

// PurgeDeletedEnergySources removes sources deleted before cutoff and their
// hours and memberships.
func PurgeDeletedEnergySources(cutoff time.Time) (int, error) {
	var ids []uuid.UUID
	if err := db.DB.Model(&EnergySource{}).Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_id IN ?", ids).Delete(&EnergyHour{}).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id IN ?", ids).Delete(&EnergyGroupMember{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&EnergySource{}).Error
	})
	return len(ids), err
}
