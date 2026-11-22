package energy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImportChunksKeepWholeHours pins that a history import read a day at a
// time from an unaligned start loses nothing at the chunk boundaries. The
// meter climbs 1 kWh an hour, read every minute, and each chunk ends at :37.
// The first chunk used to write the boundary hour from its first 37 minutes
// (0.617 kWh) as filled, and the second kept it, so every hour must hold
// the whole 1 kWh, including the hour each chunk ends before.
func TestImportChunksKeepWholeHours(t *testing.T) {
	fixture(t)
	conn := &models.Connection{Name: "Prometheus", Kind: "prometheus"}
	require.NoError(t, db.DB.Create(conn).Error)
	controlID := uuid.New()
	require.NoError(t, db.DB.Create(&models.HistoryForward{
		Name: "Meters", Enabled: true, SourceType: models.ForwardSourceControl,
		SourceIDs:    models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{controlID}},
		Destinations: models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{conn.ID}},
	}).Error)
	src := addSource(t, "Meter", models.EnergyKindControl, controlID.String(), "", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)

	orig := QueryHistory
	t.Cleanup(func() { QueryHistory = orig })
	QueryHistory = func(_ context.Context, _ string, q tsdb.Query) (*tsdb.Result, error) {
		from, err := time.Parse(time.RFC3339, q.From)
		require.NoError(t, err)
		to, err := time.Parse(time.RFC3339, q.To)
		require.NoError(t, err)
		var times []int64
		var values []any
		for at := from.Truncate(time.Minute); at.Before(to); at = at.Add(time.Minute) {
			times = append(times, at.UnixMilli())
			values = append(values, float64(at.Unix())/3600)
		}
		return &tsdb.Result{Series: []tsdb.Series{tsdb.TimeSeries("meter", nil, times, values)}}, nil
	}

	loc := Location()
	start := HourStart(time.Now(), loc).Add(-72*time.Hour + 37*time.Minute)
	boundary := start.Add(24 * time.Hour)
	_, err := Fill(context.Background(), src, start, boundary)
	require.NoError(t, err)
	_, err = Fill(context.Background(), src, boundary, boundary.Add(24*time.Hour))
	require.NoError(t, err)

	first, last := HourStart(start, loc), HourStart(boundary.Add(24*time.Hour), loc)
	rows, err := models.EnergyHoursBetween([]uuid.UUID{src.ID}, first, last.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 49, "%s through %s is 49 hours, each written", first, last)
	for _, row := range rows {
		assert.InDeltaf(t, 1.0, row.KWh, 0.001, "the hour at %s must hold the whole hour's usage", row.Hour)
	}
}

// TestSilenceEdgeHoursAreFilled pins that a silence filled from history
// loses nothing in the two hours it starts and ends in. The meter climbs
// 1 kWh an hour and is heard at :00 and :20, then not again until 4:40 and
// 4:50. Those edge hours hold live usage for 20 and 10 minutes only, and
// used to be kept as recorded live, so the fill skipped them and 40 and 50
// minutes of usage were gone. Every hour must hold the whole 1 kWh.
func TestSilenceEdgeHoursAreFilled(t *testing.T) {
	fixture(t)
	conn := &models.Connection{Name: "Prometheus", Kind: "prometheus"}
	require.NoError(t, db.DB.Create(conn).Error)
	controlID := uuid.New()
	require.NoError(t, db.DB.Create(&models.HistoryForward{
		Name: "Meters", Enabled: true, SourceType: models.ForwardSourceControl,
		SourceIDs:    models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{controlID}},
		Destinations: models.JSONColumn[[]uuid.UUID]{V: []uuid.UUID{conn.ID}},
	}).Error)
	src := addSource(t, "Meter", models.EnergyKindControl, controlID.String(), "", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)
	src.GapLimitS = 3600
	require.NoError(t, db.DB.Save(src).Error)

	meter := func(at time.Time) float64 { return float64(at.Unix()) / 3600 }
	orig := QueryHistory
	t.Cleanup(func() { QueryHistory = orig })
	QueryHistory = func(_ context.Context, _ string, q tsdb.Query) (*tsdb.Result, error) {
		from, err := time.Parse(time.RFC3339, q.From)
		require.NoError(t, err)
		to, err := time.Parse(time.RFC3339, q.To)
		require.NoError(t, err)
		var times []int64
		var values []any
		for at := from.Truncate(time.Minute); at.Before(to); at = at.Add(time.Minute) {
			times = append(times, at.UnixMilli())
			values = append(values, meter(at))
		}
		return &tsdb.Result{Series: []tsdb.Series{tsdb.TimeSeries("meter", nil, times, values)}}, nil
	}

	loc := Location()
	base := HourStart(time.Now(), loc).Add(-10 * time.Hour)
	now := base.Add(4*time.Hour + 55*time.Minute)
	r := startRecorder(t, &now)
	for _, after := range []time.Duration{0, 20 * time.Minute, 4*time.Hour + 40*time.Minute, 4*time.Hour + 50*time.Minute} {
		at := base.Add(after)
		r.push(change{ref: controlRef(controlID.String()), value: meter(at), at: at})
	}
	r.Flush()
	r.Stop()
	require.Len(t, r.gaps, 1, "the silence waits to be filled from history")

	gap := r.gaps[0]
	_, err := Fill(context.Background(), src, gap.from, gap.to.Add(time.Hour))
	require.NoError(t, err)

	rows, err := models.EnergyHoursBetween([]uuid.UUID{src.ID}, base, base.Add(5*time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 5, "the five hours from the last reading before the silence to the first after it")
	for _, row := range rows {
		assert.InDeltaf(t, 1.0, row.KWh, 0.001, "the hour at %s must hold the whole hour's usage", row.Hour)
	}
}

// TestAttributeSourceCarriesSilence pins that a Home Assistant source reading
// an attribute carries its usage across a silence. Home Assistant keeps
// statistics for an entity's state only, never its attributes, so there is
// no history to fill from. The meter reads 100 kWh at 08:00 and 104 kWh at
// 15:30, past the 6 hour gap limit; the 104 - 100 = 4 kWh must land carried
// in the 15:00 hour rather than wait for a fill that reads nothing.
func TestAttributeSourceCarriesSilence(t *testing.T) {
	fixture(t)
	haConn := uuid.New()
	src := addSource(t, "Plug", models.EnergyKindHA, haConn.String(), "switch.plug", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)
	src.Attribute = "total_energy_kwh"
	require.NoError(t, db.DB.Save(src).Error)

	now := local(2, "15:31")
	r := startRecorder(t, &now)
	for _, rd := range []struct {
		kwh   float64
		clock string
	}{{100, "08:00"}, {104, "15:30"}} {
		r.push(change{ref: haRef(haConn.String(), "switch.plug"), value: "on",
			attrs: map[string]any{"total_energy_kwh": rd.kwh}, at: local(2, rd.clock)})
	}
	r.Flush()
	r.Stop()
	assert.Empty(t, r.gaps, "an attribute has no statistics to fill the silence from")

	rows, err := models.EnergyHoursBetween([]uuid.UUID{src.ID}, local(2, "15:00"), local(2, "16:00"))
	require.NoError(t, err)
	require.Len(t, rows, 1, "the reading's hour is recorded")
	assert.InDelta(t, 4.0, rows[0].KWh, 0.001, "the usage across the silence lands in the reading's hour")
	assert.Equal(t, models.EnergyQualityCarried, rows[0].Quality, "usage bridged over a silence is marked carried")

	_, err = Fill(context.Background(), src, local(1, "00:00"), local(2, "00:00"))
	assert.ErrorIs(t, err, ErrNoHistory, "a fill must not read the entity's state statistics for an attribute")
}
