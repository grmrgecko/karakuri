package energy

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// rateHoursBack is how many past hours each flush rewrites, so an hour that
// closed between flushes gets its final price.
const rateHoursBack = 2

// rateAcc is a rate control's time-weighted sum within one hour.
type rateAcc struct {
	sum  float64
	secs float64
}

// rateTracker writes the house's price for recent hours and averages a rate
// control over each hour.
type rateTracker struct {
	tariffs   Tariffs
	controlID string
	last      *float64
	lastAt    time.Time
	acc       map[time.Time]*rateAcc
}

func newRateTracker() *rateTracker {
	return &rateTracker{acc: map[time.Time]*rateAcc{}}
}

// ControlValue reads a number control for a rate control.
func ControlValue(id string) (float64, bool) {
	if controls.S == nil {
		return 0, false
	}
	cid, err := uuid.Parse(id)
	if err != nil {
		return 0, false
	}
	snap, ok := controls.S.Get(cid)
	if !ok {
		return 0, false
	}
	return toNumber(snap.Value)
}

// load rereads the tariffs and follows the rate control of the tariff in
// force now, if it has one.
func (t *rateTracker) load() error {
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return err
	}
	t.tariffs = SortTariffs(list)
	t.follow(time.Now())
	return nil
}

// follow tracks the rate control of the tariff in force at now. A tariff
// takes effect on its start date without anything being reloaded, so every
// write asks again.
func (t *rateTracker) follow(now time.Time) {
	id := ""
	if tr := t.tariffs.At(now, Location()); tr != nil {
		id = tr.Import.V.ControlID
	}
	if id == t.controlID {
		return
	}
	t.controlID, t.last, t.acc = id, nil, map[time.Time]*rateAcc{}
	if id != "" {
		if v, ok := ControlValue(id); ok {
			t.last, t.lastAt = &v, now
		}
	}
}

// watches reports whether a change stream is the rate control.
func (t *rateTracker) watches(ref string) bool {
	return t.controlID != "" && ref == controlRef(strings.ToLower(t.controlID))
}

// sample records a new control value at `at`.
func (t *rateTracker) sample(v float64, at time.Time) {
	t.advance(at)
	t.last, t.lastAt = &v, at
}

// advance credits the last value up to `at`, hour by hour.
func (t *rateTracker) advance(at time.Time) {
	if t.last == nil || !at.After(t.lastAt) {
		return
	}
	loc := Location()
	for start := HourStart(t.lastAt, loc); start.Before(at); start = start.Add(time.Hour) {
		lo, hi := start, start.Add(time.Hour)
		if t.lastAt.After(lo) {
			lo = t.lastAt
		}
		if at.Before(hi) {
			hi = at
		}
		if !hi.After(lo) {
			continue
		}
		a := t.acc[start]
		if a == nil {
			a = &rateAcc{}
			t.acc[start] = a
		}
		secs := hi.Sub(lo).Seconds()
		a.sum += *t.last * secs
		a.secs += secs
	}
	t.lastAt = at
}

// write upserts the price of the current hour and the hours just before it.
// It reaches back to earliest, the oldest hour the flush wrote usage to: a
// cumulative meter's first reading after a silence spreads its usage over
// every hour of the silence, and those hours need a price too. An hour that
// old under a rate control has no recorded mean and stays unpriced.
func (t *rateTracker) write(now, earliest time.Time, loc *time.Location) error {
	t.follow(now)
	t.advance(now)
	cur := HourStart(now, loc)
	first := cur.Add(-rateHoursBack * time.Hour)
	if earliest.Before(first) {
		first = HourStart(earliest, loc)
	}
	var rows []models.EnergyRate
	for h := first; !h.After(cur); h = h.Add(time.Hour) {
		row, ok := t.rateFor(h, loc)
		if ok {
			rows = append(rows, row)
		}
	}
	for h := range t.acc {
		if h.Before(cur.Add(-rateHoursBack * time.Hour)) {
			delete(t.acc, h)
		}
	}
	return db.DB.Transaction(func(tx *gorm.DB) error { return models.UpsertEnergyRates(tx, rows) })
}

// rateFor prices one hour; a rate control's hour uses the time-weighted mean
// recorded live, and an hour without one is left alone.
func (t *rateTracker) rateFor(h time.Time, loc *time.Location) (models.EnergyRate, bool) {
	tr := t.tariffs.At(h, loc)
	if tr == nil {
		return models.EnergyRate{}, false
	}
	if tr.Import.V.ControlID != "" {
		a := t.acc[h]
		if a == nil || a.secs == 0 || tr.Import.V.ControlID != t.controlID {
			return models.EnergyRate{}, false
		}
		mean := a.sum / a.secs
		row := models.EnergyRate{Hour: h, LocalDate: LocalDate(h, loc), TariffID: &tr.ID, Currency: tr.Currency, ImportRate: mean}
		if tr.Export.V != nil {
			if x, _, err := specRate(*tr.Export.V, h, loc, ControlValue, false); err == nil {
				row.ExportRate = &x
			}
		}
		return row, true
	}
	return fixedRate(tr, h, loc)
}

// fixedRate prices an hour under a tariff without a rate control.
func fixedRate(tr *models.EnergyTariff, h time.Time, loc *time.Location) (models.EnergyRate, bool) {
	rate, period, err := specRate(tr.Import.V, h, loc, nil, false)
	if err != nil {
		return models.EnergyRate{}, false
	}
	row := models.EnergyRate{Hour: h, LocalDate: LocalDate(h, loc), TariffID: &tr.ID, Currency: tr.Currency, ImportRate: rate, Period: period}
	if tr.Export.V != nil {
		if x, _, err := specRate(*tr.Export.V, h, loc, nil, false); err == nil {
			row.ExportRate = &x
		}
	}
	return row, true
}

// RecomputeRates reprices every recorded hour from `from` to now under the
// current tariffs, in one transaction, and returns how many hours it wrote.
// Hours under a rate control keep the price recorded for them; hours no
// tariff covers lose theirs.
func RecomputeRates(from, now time.Time) (int, error) {
	loc := Location()
	list, err := models.ListEnergyTariffs()
	if err != nil {
		return 0, err
	}
	ts := SortTariffs(list)
	earliest, ok := models.EarliestEnergyHour()
	if !ok {
		return 0, nil
	}
	if from.Before(earliest) {
		from = earliest
	}
	start := HourStart(from, loc)
	end := HourStart(now, loc)
	written := 0
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		var rows []models.EnergyRate
		var uncovered []time.Time
		for h := start; !h.After(end); h = h.Add(time.Hour) {
			tr := ts.At(h, loc)
			switch {
			case tr == nil:
				uncovered = append(uncovered, h)
			case tr.Import.V.ControlID != "":
				// The control's past values are only in the recorded rows.
				if err := tx.Model(&models.EnergyRate{}).Where("hour = ? AND (tariff_id IS NULL OR tariff_id <> ?)", h, tr.ID).
					Update("tariff_id", tr.ID).Error; err != nil {
					return err
				}
			default:
				if row, ok := fixedRate(tr, h, loc); ok {
					rows = append(rows, row)
				}
			}
		}
		if len(uncovered) > 0 {
			if err := tx.Where("hour IN ?", uncovered).Delete(&models.EnergyRate{}).Error; err != nil {
				return err
			}
		}
		written = len(rows)
		return models.UpsertEnergyRates(tx, rows)
	})
	if R != nil {
		R.Reload()
	}
	return written, err
}
