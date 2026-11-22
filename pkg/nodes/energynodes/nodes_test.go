package energynodes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testCtx is the ExecContext the nodes run under.
type testCtx struct {
	engine.ExecContext
	cfg   string
	clock engine.Clock
	store engine.NodeStore
}

func (c testCtx) Config(out any) error           { return json.Unmarshal([]byte(c.cfg), out) }
func (c testCtx) Logger() *zap.Logger            { return zap.NewNop() }
func (c testCtx) Clock() engine.Clock            { return c.clock }
func (c testCtx) DurableStore() engine.NodeStore { return c.store }
func (c testCtx) Interpolate(s string, msg *engine.Msg) (string, error) {
	return expr.Interpolate(s, expr.BuildEnv(expr.Env{Payload: msg.Payload, Topic: msg.Topic, Meta: msg.Meta}))
}

var chicago, _ = time.LoadLocation("America/Chicago")

func local(date, clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, chicago)
	if err != nil {
		panic(err)
	}
	return t
}

func fp(v float64) *float64 { return &v }

// setup stores a house source with 3 kWh on Monday 2026-11-02 16:00 and 2 kWh
// at the same hour on the four Mondays before, under a peak and off-peak
// tariff with a daily charge.
func setup(t *testing.T) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "America/Chicago", WeekStart: 1}))
	house := &models.EnergySource{Name: "House", SourceKind: models.EnergyKindControl, SourceID: uuid.NewString(),
		Measurement: models.EnergyMeasureEnergy, Unit: "kWh", Role: models.EnergyRoleHouse}
	require.NoError(t, db.DB.Create(house).Error)
	hours := []models.EnergyHour{{SourceID: house.ID, Hour: local("2026-11-02", "16:00").UTC(), LocalDate: "2026-11-02", KWh: 3, Quality: "live"}}
	for _, d := range []string{"2026-10-26", "2026-10-19", "2026-10-12", "2026-10-05"} {
		hours = append(hours, models.EnergyHour{SourceID: house.ID, Hour: local(d, "16:00").UTC(), LocalDate: d, KWh: 2, Quality: "live"})
	}
	require.NoError(t, models.UpsertEnergyHours(db.DB, hours))
	weekdays, weekend := []int{1, 2, 3, 4, 5}, []int{6, 7}
	tariff := &models.EnergyTariff{Name: "TOU", Currency: "USD", EffectiveFrom: "2026-01-01", DailyCharge: 0.5}
	tariff.Import.V = models.EnergyRateSpec{Periods: []models.EnergyPeriod{
		{Name: "off-peak", Days: weekdays, Start: "00:00", End: "16:00", Rate: 0.15},
		{Name: "peak", Days: weekdays, Start: "16:00", End: "21:00", Rate: 0.4},
		{Name: "off-peak", Days: weekdays, Start: "21:00", End: "00:00", Rate: 0.15},
		{Name: "off-peak", Days: weekend, Start: "00:00", End: "00:00", Rate: 0.15},
	}}
	require.NoError(t, energy.ValidateTariff(tariff))
	require.NoError(t, db.DB.Create(tariff).Error)
	_, err := energy.RecomputeRates(local("2026-10-01", "00:00"), local("2026-11-02", "17:00"))
	require.NoError(t, err)
}

func TestUsageNode(t *testing.T) {
	setup(t)
	clock := engine.NewFakeClock(local("2026-11-02", "17:00"))
	out, err := UsageNode{}.Execute(context.Background(),
		testCtx{cfg: `{"target":"house","span":"today","compare_usual":true}`, clock: clock}, map[string]*engine.Msg{})
	require.NoError(t, err)
	p := out["out"][0].Payload.(map[string]any)
	assert.InDelta(t, 3.0, p["kwh"], 1e-9)
	assert.InDelta(t, 3*0.4+0.5, p["cost"], 1e-9, "energy at the peak rate plus the daily charge")
	assert.Equal(t, "USD", p["currency"])
	assert.InDelta(t, 2.0, p["usual_kwh"], 1e-9)
	assert.InDelta(t, 1.5, p["ratio"], 1e-9)

	// One part of a split over several targets keeps its place in the split,
	// or a join after the node has no group to complete.
	group := []engine.GroupFrame{{ID: "targets", Index: 0, Count: 2}}
	out, err = UsageNode{}.Execute(context.Background(),
		testCtx{cfg: `{"target":"{{ payload }}","span":"today"}`, clock: clock}, map[string]*engine.Msg{"in": {Payload: "house", Group: group}})
	require.NoError(t, err)
	assert.Equal(t, group, out["out"][0].Group, "the usage reading must stay in the split it arrived in")

	_, err = UsageNode{}.Execute(context.Background(),
		testCtx{cfg: `{"target":"group:nope"}`, clock: clock}, map[string]*engine.Msg{})
	assert.Error(t, err)
}

func TestPriceNode(t *testing.T) {
	setup(t)
	clock := engine.NewFakeClock(local("2026-11-02", "17:00"))
	out, err := PriceNode{}.Execute(context.Background(), testCtx{cfg: `{"mode":"now"}`, clock: clock}, map[string]*engine.Msg{})
	require.NoError(t, err)
	p := out["out"][0].Payload.(map[string]any)
	assert.Equal(t, 0.4, p["rate"])
	assert.Equal(t, "peak", p["period"])
	assert.Equal(t, local("2026-11-02", "21:00").Format(time.RFC3339), p["ends_at"])

	out, err = PriceNode{}.Execute(context.Background(), testCtx{cfg: `{"mode":"cheapest","hours":3,"from":"now","to":"now+12h"}`, clock: clock}, map[string]*engine.Msg{})
	require.NoError(t, err)
	p = out["out"][0].Payload.(map[string]any)
	assert.Equal(t, local("2026-11-02", "21:00").Format(time.RFC3339), p["start"])
	assert.InDelta(t, 0.15, p["average_rate"], 1e-9)

	// A split over several times asks for the price at each; both answers
	// keep their place in the split so a join can collect them.
	group := []engine.GroupFrame{{ID: "times", Index: 1, Count: 2}}
	in := map[string]*engine.Msg{"in": {Payload: "now+2h", Group: group}}
	for _, cfg := range []string{`{"mode":"at","at":"{{ payload }}"}`, `{"mode":"cheapest","hours":3,"from":"now","to":"{{ payload }}+10h"}`} {
		out, err = PriceNode{}.Execute(context.Background(), testCtx{cfg: cfg, clock: clock}, in)
		require.NoErrorf(t, err, "price lookup must succeed for %s", cfg)
		assert.Equalf(t, group, out["out"][0].Group, "the price must stay in the split it arrived in for %s", cfg)
	}
}

// emitted collects a trigger's messages.
func collect(ch chan *engine.Msg, n int, wait time.Duration) []*engine.Msg {
	var out []*engine.Msg
	deadline := time.After(wait)
	for len(out) < n {
		select {
		case m := <-ch:
			out = append(out, m)
		case <-deadline:
			return out
		}
	}
	return out
}

func TestPricePeriodTrigger(t *testing.T) {
	setup(t)
	store := engine.NewMemDurableStore()
	clock := engine.NewFakeClock(local("2026-11-02", "15:59"))
	emits := make(chan *engine.Msg, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = PricePeriodTrigger{}.Start(ctx, testCtx{cfg: `{}`, clock: clock, store: store}, func(m *engine.Msg) { emits <- m })
		close(done)
	}()
	require.True(t, clock.BlockUntilAfterCalls(1, 2*time.Second))
	clock.Advance(2 * time.Minute)
	got := collect(emits, 1, 2*time.Second)
	require.Len(t, got, 1)
	p := got[0].Payload.(map[string]any)
	assert.Equal(t, "peak", p["period"])
	assert.Equal(t, "off-peak", p["previous_period"])
	assert.Equal(t, 0.4, p["rate"])
	assert.Equal(t, local("2026-11-02", "21:00").Format(time.RFC3339), p["ends_at"])
	cancel()
	<-done

	// Restarted after 21:00 was missed: delivered late, and 16:00 not again.
	clock2 := engine.NewFakeClock(local("2026-11-02", "21:30"))
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() {
		_ = PricePeriodTrigger{}.Start(ctx2, testCtx{cfg: `{"missed_deadline":"deliver"}`, clock: clock2, store: store}, func(m *engine.Msg) { emits <- m })
	}()
	got = collect(emits, 2, 500*time.Millisecond)
	require.Len(t, got, 1)
	assert.Equal(t, "off-peak", got[0].Payload.(map[string]any)["period"])
	assert.Contains(t, got[0].Meta, "late_ms")
}

// TestPricePeriodTriggerSkipsBoundariesBeforeTheTariffExisted locks that a
// trigger armed with no tariff starts from the moment one appears. The cursor
// used to stay at the arming time while there was nothing to read, so a tariff
// saved later fired every boundary since arming at once, each as an on-time run.
func TestPricePeriodTriggerSkipsBoundariesBeforeTheTariffExisted(t *testing.T) {
	setup(t)
	var tariff models.EnergyTariff
	require.NoError(t, db.DB.First(&tariff).Error)
	require.NoError(t, db.DB.Delete(&models.EnergyTariff{}, "id = ?", tariff.ID).Error)

	store := engine.NewMemDurableStore()
	clock := engine.NewFakeClock(local("2026-11-02", "15:00"))
	emits := make(chan *engine.Msg, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = PricePeriodTrigger{}.Start(ctx, testCtx{cfg: `{}`, clock: clock, store: store}, func(m *engine.Msg) { emits <- m })
	}()

	// 16:00 passes with no tariff to say it is a boundary.
	require.True(t, clock.BlockUntilAfterCalls(1, 2*time.Second))
	clock.Advance(2 * time.Hour)
	require.True(t, clock.BlockUntilAfterCalls(2, 2*time.Second))

	require.NoError(t, db.DB.Create(&tariff).Error)
	clock.Advance(time.Minute)
	require.True(t, clock.BlockUntilAfterCalls(3, 2*time.Second))
	assert.Empty(t, collect(emits, 1, 200*time.Millisecond),
		"the 16:00 boundary passed before the tariff existed, so no run starts for it")

	clock.Advance(4 * time.Hour)
	got := collect(emits, 1, 2*time.Second)
	require.Len(t, got, 1, "the next boundary under the new tariff still fires")
	assert.Equal(t, local("2026-11-02", "21:00").Format(time.RFC3339), got[0].Payload.(map[string]any)["starts_at"])
}

func TestRangeSpanStartsWeeksOnTheConfiguredDay(t *testing.T) {
	setup(t)
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "UTC", WeekStart: 7}))
	t.Cleanup(func() { _ = energy.SaveSettings(energy.Settings{WeekStart: 1}) })
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	from, _, err := spanBounds(usageConfig{Span: spanRange, From: "now/w"}, now, time.UTC)
	require.NoError(t, err)
	assert.True(t, from.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)),
		"a range week starts on the configured Sunday, got %s", from)
}
