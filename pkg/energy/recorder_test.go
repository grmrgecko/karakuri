package energy

import (
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fixture is a database with energy settings in Chicago time.
func fixture(t *testing.T) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	require.NoError(t, SaveSettings(Settings{Timezone: "America/Chicago", WeekStart: 1}))
	t.Cleanup(func() { apply(Settings{WeekStart: 1}) })
}

func addSource(t *testing.T, name, kind, sourceID, entity, measurement, unit, role string) *models.EnergySource {
	t.Helper()
	s := &models.EnergySource{Name: name, SourceKind: kind, SourceID: sourceID, EntityID: entity,
		Measurement: measurement, Unit: unit, Role: role, State: StateWaiting}
	require.NoError(t, db.DB.Create(s).Error)
	return s
}

// startRecorder runs a recorder whose clock reads the given time.
func startRecorder(t *testing.T, now *time.Time) *Recorder {
	t.Helper()
	r := NewRecorder(zap.NewNop())
	r.now = func() time.Time { return *now }
	r.Start()
	return r
}

func local(day int, clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-11-0"+string(rune('0'+day))+" "+clock, chicago)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRecorderRollupsAndCost(t *testing.T) {
	fixture(t)
	deviceID, controlID, haConn := uuid.New(), uuid.New(), uuid.New()
	plug := addSource(t, "Office plug", models.EnergyKindDevice, deviceID.String(), "", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)
	heat := addSource(t, "Heat pump", models.EnergyKindControl, controlID.String(), "", models.EnergyMeasurePower, "W", models.EnergyRoleConsumer)
	addSource(t, "House", models.EnergyKindHA, haConn.String(), "sensor.house_energy", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleHouse)
	office := &models.EnergyGroup{Name: "Office"}
	require.NoError(t, db.DB.Create(office).Error)
	require.NoError(t, db.DB.Create(&models.EnergyGroupMember{GroupID: office.ID, SourceID: plug.ID, JoinedAt: local(2, "00:00")}).Error)

	now := local(2, "16:59")
	r := startRecorder(t, &now)
	feed := func(c change) { r.push(c) }
	device := func(v float64, clock string) {
		feed(change{ref: deviceRef(deviceID.String()), value: map[string]any{"energy": v}, attrs: map[string]any{"energy": v}, at: local(2, clock)})
	}
	plug.Attribute = "energy"
	require.NoError(t, db.DB.Save(plug).Error)
	r.Reload()

	// Plug: 1 kWh at 16:00-17:00 then a reset.
	device(100.0, "16:00")
	device(100.5, "16:30")
	device(0.2, "16:45") // reset adds 0.2
	device(0.5, "16:58")
	// Heat pump: 2 kW for 30 minutes.
	for _, clock := range []string{"16:00", "16:10", "16:20", "16:30"} {
		feed(change{ref: controlRef(controlID.String()), value: 2000.0, at: local(2, clock)})
	}
	// House meter: 3 kWh in the hour.
	feed(change{ref: haRef(haConn.String(), "sensor.house_energy"), value: "1000.0", at: local(2, "16:00")})
	feed(change{ref: haRef(haConn.String(), "sensor.house_energy"), value: "1003.0", at: local(2, "16:59")})
	r.Flush()

	hour := func(target string, measures ...string) map[string]float64 {
		t.Helper()
		roll, err := Compute(target, measures, local(2, "16:00"), local(2, "17:00"), PeriodHour)
		require.NoError(t, err)
		require.Len(t, roll.Rows, 1)
		return roll.Rows[0].Values
	}
	assert.InDelta(t, 1.0, hour("source:" + plug.ID.String())[MeasureUsage], 0.001, "0.5 then a reset to 0.2 and 0.3 more")
	assert.InDelta(t, 1.0, hour("source:" + heat.ID.String())[MeasureUsage], 0.001)
	assert.InDelta(t, 3.0, hour(TargetHouse)[MeasureUsage], 0.001)
	assert.InDelta(t, 1.0, hour(TargetUntracked)[MeasureUsage], 0.001, "house 3 minus consumers 2")
	assert.InDelta(t, 1.0, hour("group:" + office.ID.String())[MeasureUsage], 0.001)

	// A restart mid-hour loses nothing for a cumulative meter (SC-004).
	r.Stop()
	now = local(2, "17:40")
	r = startRecorder(t, &now)
	feed = func(c change) { r.push(c) }
	device(0.9, "17:30")
	r.Flush()
	roll, err := Compute("source:"+plug.ID.String(), nil, local(2, "16:00"), local(2, "18:00"), PeriodDay)
	require.NoError(t, err)
	assert.InDelta(t, 1.4, roll.Rows[0].Values[MeasureUsage], 0.001, "0.4 more after the restart")

	// A two-period tariff prices each hour, and an edit recomputes cost only.
	tariff := touTariff("2026-11-01", 0.4)
	require.NoError(t, ValidateTariff(tariff))
	require.NoError(t, db.DB.Create(tariff).Error)
	_, err = RecomputeRates(local(1, "00:00"), local(2, "17:40"))
	require.NoError(t, err)
	v := hour(TargetHouse, MeasureUsage, MeasureCost, MeasureDailyCharge, MeasureTotalCost)
	assert.InDelta(t, 1.2, v[MeasureCost], 1e-9, "3 kWh at the 0.40 peak")
	day, err := Compute(TargetHouse, []string{MeasureTotalCost}, local(2, "00:00"), local(3, "00:00"), PeriodDay)
	require.NoError(t, err)
	assert.Equal(t, "USD", day.Currency)
	assert.InDelta(t, 1.2+0.5, day.Rows[0].Values[MeasureTotalCost], 1e-9, "energy plus the daily charge")

	tariff.Import.V.Periods[1].Rate = 0.3
	require.NoError(t, db.DB.Save(tariff).Error)
	start := time.Now()
	_, err = RecomputeRates(local(1, "00:00"), local(2, "17:40"))
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 10*time.Second)
	v = hour(TargetHouse, MeasureUsage, MeasureCost)
	assert.InDelta(t, 0.9, v[MeasureCost], 1e-9)
	assert.InDelta(t, 3.0, v[MeasureUsage], 0.001, "an edit leaves usage alone")
	r.Stop()
}

// TestEditedSourceStartsFromNothing pins that a reload, which writes pending
// meter state before rereading the sources, leaves alone a row edited to read
// something else. A reading taken just before
// the source was edited to read another control used to be written back over
// the row the edit had cleared, and the reload then seeded the new control
// from it, so a power source integrated the old control's 2 kW across the
// first interval of the new one.
func TestEditedSourceStartsFromNothing(t *testing.T) {
	fixture(t)
	oldControl, newControl := uuid.NewString(), uuid.NewString()
	src := addSource(t, "Heater", models.EnergyKindControl, oldControl, "", models.EnergyMeasurePower, "W", models.EnergyRoleConsumer)
	now := time.Now()
	r := NewRecorder(zap.NewNop())
	r.now = func() time.Time { return now }
	r.load()
	r.handle(change{ref: controlRef(oldControl), value: 2000.0, at: now})

	// The edit SaveSource makes when a source reads something else.
	require.NoError(t, db.DB.Model(&models.EnergySource{}).Where("id = ?", src.ID).Updates(map[string]any{
		"source_id": newControl, "last_value": nil, "last_at": nil, "state": StateWaiting, "state_note": "",
	}).Error)
	r.write()
	r.load()

	stored, err := models.FindEnergySource(src.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.LastAt, "the old control's reading must not be written over the edited row")
	assert.Nil(t, r.sources[src.ID].st.Last, "the new control must start without a previous reading")
}

func TestSolarRollup(t *testing.T) {
	fixture(t)
	add := func(name, role string, kwh float64) {
		s := addSource(t, name, models.EnergyKindControl, uuid.NewString(), "", models.EnergyMeasureEnergy, "kWh", role)
		require.NoError(t, models.UpsertEnergyHours(db.DB, []models.EnergyHour{{SourceID: s.ID, Hour: local(2, "12:00").UTC(),
			LocalDate: "2026-11-02", KWh: kwh, Quality: models.EnergyQualityLive}}))
	}
	add("Inverter", models.EnergyRoleProduction, 4)
	add("Grid in", models.EnergyRoleImport, 1)
	add("Grid out", models.EnergyRoleExport, 2.5)
	roll, err := Compute(TargetHouse, []string{MeasureUsage, MeasureSelfConsumption, MeasureSolarShare},
		local(2, "12:00"), local(2, "13:00"), PeriodHour)
	require.NoError(t, err)
	v := roll.Rows[0].Values
	assert.InDelta(t, 2.5, v[MeasureUsage], 1e-9)
	assert.InDelta(t, 1.5, v[MeasureSelfConsumption], 1e-9)
	assert.InDelta(t, 60, v[MeasureSolarShare], 1e-9)
}

func TestRecomputeYearTiming(t *testing.T) {
	fixture(t)
	// A year of 20 sources (SC-003): the recompute touches one price row per
	// hour, so the source count does not matter.
	start := local(2, "00:00").AddDate(-1, 0, 0)
	var rows []models.EnergyHour
	src := addSource(t, "Plug", models.EnergyKindControl, uuid.NewString(), "", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)
	rows = append(rows, models.EnergyHour{SourceID: src.ID, Hour: start.UTC(), LocalDate: "2025-11-02", KWh: 1, Quality: "live"})
	require.NoError(t, models.UpsertEnergyHours(db.DB, rows))
	tariff := touTariff("2025-01-01", 0.4)
	require.NoError(t, db.DB.Create(tariff).Error)
	begin := time.Now()
	n, err := RecomputeRates(start, local(2, "00:00"))
	require.NoError(t, err)
	assert.Greater(t, n, 8700)
	assert.Less(t, time.Since(begin), 10*time.Second)
}

// TestRateControlFollowsTariffTakingEffect locks that a rate-control tariff
// with a start date ahead is followed from that date on. The tracker chose
// its control only when tariffs were reloaded, so a tariff saved ahead of
// time watched no control once it took effect and its hours got no price
// until an edit or a restart.
func TestRateControlFollowsTariffTakingEffect(t *testing.T) {
	fixture(t)
	loc := Location()
	saved := time.Now()
	control := uuid.New()
	flat := &models.EnergyTariff{Name: "Flat", Currency: "USD", EffectiveFrom: "2000-01-01"}
	flat.Import.V = models.EnergyRateSpec{Flat: fp(0.15)}
	dynamic := &models.EnergyTariff{Name: "Dynamic", Currency: "USD", EffectiveFrom: LocalDate(saved.Add(48*time.Hour), loc)}
	dynamic.Import.V = models.EnergyRateSpec{ControlID: control.String()}
	require.NoError(t, db.DB.Create(flat).Error)
	require.NoError(t, db.DB.Create(dynamic).Error)

	tracker := newRateTracker()
	require.NoError(t, tracker.load())
	require.False(t, tracker.watches(controlRef(control.String())), "the flat tariff in force when saved has no rate control")

	// Three days on, the dynamic tariff is in force and nothing was reloaded.
	hour := HourStart(saved.Add(72*time.Hour), loc)
	require.NoError(t, tracker.write(hour.Add(10*time.Minute), hour, loc))
	require.True(t, tracker.watches(controlRef(control.String())), "the tariff now in force takes its rate from the control")
	tracker.sample(0.30, hour.Add(10*time.Minute))
	require.NoError(t, tracker.write(hour.Add(20*time.Minute), hour, loc))

	rates, err := models.EnergyRatesBetween(hour, hour.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, rates, 1, "the hour under the dynamic tariff must be priced")
	assert.InDelta(t, 0.30, rates[0].ImportRate, 1e-9, "the hour is priced at the control's value over it")
}

// TestHoursSpreadOverSilenceArePriced locks that every hour a flush writes
// usage to gets a price. A cumulative meter keeps counting while the server
// is down, and its first reading afterwards spreads the usage over the hours
// of the silence. Only the current hour and the two before it were priced,
// so the older hours showed usage at no cost: 6 kWh from 10:05 to 15:05 is
// 1.2 kWh an hour, and the 11:00 and 12:00 hours went unpriced, giving 0.90
// where 6 x 0.25 = 1.50 was due.
func TestHoursSpreadOverSilenceArePriced(t *testing.T) {
	fixture(t)
	tariff := &models.EnergyTariff{Name: "Flat", Currency: "USD", EffectiveFrom: "2026-11-01"}
	tariff.Import.V = models.EnergyRateSpec{Flat: fp(0.25)}
	require.NoError(t, ValidateTariff(tariff))
	require.NoError(t, db.DB.Create(tariff).Error)
	control := uuid.New()
	plug := addSource(t, "Plug", models.EnergyKindControl, control.String(), "", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)

	now := local(2, "10:06")
	r := startRecorder(t, &now)
	r.push(change{ref: controlRef(control.String()), value: 100.0, at: local(2, "10:05")})
	r.Flush()
	r.Stop()

	// Five hours pass with the server down, inside the meter's 6 hour gap limit.
	now = local(2, "15:20")
	r = startRecorder(t, &now)
	defer r.Stop()
	r.push(change{ref: controlRef(control.String()), value: 106.0, at: local(2, "15:05")})
	r.Flush()

	roll, err := Compute("source:"+plug.ID.String(), []string{MeasureUsage, MeasureCost}, local(2, "10:00"), local(2, "16:00"), PeriodHour)
	require.NoError(t, err)
	require.Len(t, roll.Rows, 6, "10:00 to 16:00 is six hours")
	usage, cost := 0.0, 0.0
	for _, row := range roll.Rows {
		usage += row.Values[MeasureUsage]
		cost += row.Values[MeasureCost]
		assert.InDelta(t, row.Values[MeasureUsage]*0.25, row.Values[MeasureCost], 1e-9,
			"the %s hour is priced at the flat rate", row.Start.In(chicago).Format("15:04"))
	}
	assert.InDelta(t, 6.0, usage, 1e-9, "the 6 kWh counted over the silence is all recorded")
	assert.InDelta(t, 1.5, cost, 1e-9, "6 kWh at 0.25 costs 1.50")
}
