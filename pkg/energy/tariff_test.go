package energy

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var weekdays = []int{1, 2, 3, 4, 5}
var everyDay = []int{1, 2, 3, 4, 5, 6, 7}

// touTariff is peak 0.40 on weekdays 16:00 to 21:00 and off-peak 0.15
// otherwise, with a daily charge and export credit.
func touTariff(from string, peak float64) *models.EnergyTariff {
	t := &models.EnergyTariff{Name: "TOU", Currency: "usd", EffectiveFrom: from, DailyCharge: 0.5}
	t.Import.V = models.EnergyRateSpec{Periods: []models.EnergyPeriod{
		{Name: "off-peak", Days: weekdays, Start: "00:00", End: "16:00", Rate: 0.15},
		{Name: "peak", Days: weekdays, Start: "16:00", End: "21:00", Rate: peak},
		{Name: "off-peak", Days: weekdays, Start: "21:00", End: "00:00", Rate: 0.15},
		{Name: "off-peak", Days: []int{6, 7}, Start: "00:00", End: "00:00", Rate: 0.15},
	}}
	t.Export.V = &models.EnergyRateSpec{Flat: fp(0.08)}
	return t
}

func TestValidateTariff(t *testing.T) {
	require.NoError(t, ValidateTariff(touTariff("2026-11-01", 0.4)))

	gap := touTariff("2026-11-01", 0.4)
	gap.Import.V.Periods[3] = models.EnergyPeriod{Name: "off-peak", Days: []int{6, 7}, Start: "07:00", End: "00:00", Rate: 0.15}
	err := ValidateTariff(gap)
	require.Error(t, err)
	assert.Equal(t, "import Saturday 00:00 to 07:00 has no rate", err.Error())

	overlap := touTariff("2026-11-01", 0.4)
	overlap.Import.V.Periods = append(overlap.Import.V.Periods, models.EnergyPeriod{Name: "shoulder", Days: []int{1}, Start: "15:00", End: "17:00", Rate: 0.2})
	err = ValidateTariff(overlap)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `Monday 15:00 is in both "off-peak" and "shoulder"`)

	both := touTariff("2026-11-01", 0.4)
	both.Import.V.Flat = fp(0.1)
	assert.Error(t, ValidateTariff(both))

	bad := touTariff("2026-11-01", 0.4)
	bad.Currency = "dollars"
	assert.Error(t, ValidateTariff(bad))
}

func TestPriceAndEffectiveDates(t *testing.T) {
	ts := SortTariffs([]*models.EnergyTariff{touTariff("2026-12-01", 0.5), touTariff("2026-11-01", 0.4)})
	// 2026-11-30 is a Monday, 2026-12-01 a Tuesday.
	nov := time.Date(2026, 11, 30, 17, 0, 0, 0, chicago)
	dec := time.Date(2026, 12, 1, 17, 0, 0, 0, chicago)
	p, err := ts.PriceAt(nov, chicago, nil, false)
	require.NoError(t, err)
	assert.Equal(t, 0.4, p.Import)
	assert.Equal(t, "peak", p.Period)
	assert.Equal(t, 0.08, *p.Export)
	p, err = ts.PriceAt(dec, chicago, nil, false)
	require.NoError(t, err)
	assert.Equal(t, 0.5, p.Import)
	_, err = ts.PriceAt(time.Date(2026, 10, 1, 12, 0, 0, 0, chicago), chicago, nil, false)
	assert.ErrorIs(t, err, ErrNoTariff)
}

func TestNextChange(t *testing.T) {
	ts := SortTariffs([]*models.EnergyTariff{touTariff("2026-11-01", 0.4)})
	at, p, err := ts.NextChange(time.Date(2026, 11, 2, 15, 30, 0, 0, chicago), chicago)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 2, 16, 0, 0, 0, chicago), at)
	assert.Equal(t, "peak", p.Period)
	// Friday 21:00 to Monday 16:00 is one off-peak stretch.
	at, _, err = ts.NextChange(time.Date(2026, 11, 6, 22, 0, 0, 0, chicago), chicago)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 9, 16, 0, 0, 0, chicago), at)
}

func TestCheapest(t *testing.T) {
	five := &models.EnergyTariff{Name: "five", Currency: "EUR", EffectiveFrom: "2026-01-01"}
	five.Import.V = models.EnergyRateSpec{Periods: []models.EnergyPeriod{
		{Name: "night", Days: everyDay, Start: "00:00", End: "05:00", Rate: 0.12},
		{Name: "super", Days: everyDay, Start: "05:00", End: "07:00", Rate: 0.05},
		{Name: "day", Days: everyDay, Start: "07:00", End: "16:00", Rate: 0.2},
		{Name: "peak", Days: everyDay, Start: "16:00", End: "20:00", Rate: 0.45},
		{Name: "evening", Days: everyDay, Start: "20:00", End: "00:00", Rate: 0.25},
	}}
	require.NoError(t, ValidateTariff(five))
	ts := Tariffs{five}
	from := time.Date(2026, 11, 2, 18, 7, 0, 0, chicago)
	to := time.Date(2026, 11, 3, 10, 0, 0, 0, chicago)
	start := time.Now()
	w, err := ts.Cheapest(3*time.Hour, from, to, chicago)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "SC-006")
	assert.Equal(t, time.Date(2026, 11, 3, 4, 0, 0, 0, chicago), w.Start)
	assert.InDelta(t, (0.12+0.05+0.05)/3, w.Average, 1e-9)
	assert.Equal(t, "EUR", w.Currency)

	// A start with seconds on a quarter-hour minute must not be answered with
	// that quarter hour, which began before the earliest allowed start.
	flat := &models.EnergyTariff{Name: "flat", Currency: "EUR", EffectiveFrom: "2026-01-01"}
	flat.Import.V = models.EnergyRateSpec{Periods: []models.EnergyPeriod{
		{Name: "all", Days: everyDay, Start: "00:00", End: "00:00", Rate: 0.2},
	}}
	require.NoError(t, ValidateTariff(flat))
	late := time.Date(2026, 11, 2, 10, 15, 30, 0, chicago)
	w, err = Tariffs{flat}.Cheapest(time.Hour, late, late.Add(24*time.Hour), chicago)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 2, 10, 30, 0, 0, chicago), w.Start,
		"the first quarter hour at or after 10:15:30 is 10:30")

	control := &models.EnergyTariff{Name: "dynamic", Currency: "EUR", EffectiveFrom: "2026-01-01"}
	control.Import.V = models.EnergyRateSpec{ControlID: "c1"}
	_, err = Tariffs{control}.Cheapest(time.Hour, from, to, chicago)
	assert.ErrorIs(t, err, ErrFutureUnknown)
	p, err := Tariffs{control}.PriceAt(from, chicago, func(string) (float64, bool) { return 0.31, true }, false)
	require.NoError(t, err)
	assert.Equal(t, 0.31, p.Import)
}
