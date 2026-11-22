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

func TestQuerier(t *testing.T) {
	fixture(t)
	plug := addSource(t, "Plug", models.EnergyKindControl, uuid.NewString(), "", models.EnergyMeasureEnergy, "kWh", models.EnergyRoleConsumer)
	require.NoError(t, models.UpsertEnergyHours(db.DB, []models.EnergyHour{
		{SourceID: plug.ID, Hour: local(2, "10:00").UTC(), LocalDate: "2026-11-02", KWh: 1, Quality: "live"},
		{SourceID: plug.ID, Hour: local(3, "10:00").UTC(), LocalDate: "2026-11-03", KWh: 2, Quality: "live"},
	}))
	req := tsdb.Request{Range: tsdb.Range{From: local(2, "00:00"), To: local(4, "00:00")}, Interval: 6 * time.Hour}
	req.Energy = &tsdb.EnergyQuery{Targets: []string{"house", "source:" + plug.ID.String()}, Measure: "usage", Period: "auto"}

	res, err := Querier{}.Query(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, res.Series, 2)
	assert.Equal(t, "House", res.Series[0].Name)
	assert.Equal(t, "kwatth", res.Series[0].Unit)
	assert.Equal(t, []any{1.0, 2.0}, res.Series[1].Fields[1].Values, "an interval of six hours rolls up by day")

	req.Format = tsdb.FormatTable
	res, err = Querier{}.Query(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, res.Series, 1)
	assert.Equal(t, []any{"House", "Plug"}, res.Series[0].Fields[0].Values)
	assert.Equal(t, []any{3.0, 3.0}, res.Series[0].Fields[1].Values)

	req.Energy.Targets = []string{"group:nope"}
	_, err = Querier{}.Query(context.Background(), req)
	assert.Error(t, err)

	targets, err := Querier{}.Metadata(context.Background(), tsdb.MetaRequest{Kind: tsdb.MetaTargets})
	require.NoError(t, err)
	assert.Contains(t, targets, "source:"+plug.ID.String())
}

// TestTableSolarShareCoversTheAskedRange locks that a table's solar share is
// worked out over the hours the query asked for. It was taken over the whole
// periods the range touched, so a range starting at noon with a day period
// counted the morning's production: the total said 30% while the range's own
// hours used no solar at all.
func TestTableSolarShareCoversTheAskedRange(t *testing.T) {
	fixture(t)
	add := func(name, role string, hours map[string]float64) {
		s := addSource(t, name, models.EnergyKindControl, uuid.NewString(), "", models.EnergyMeasureEnergy, "kWh", role)
		for clock, kwh := range hours {
			require.NoError(t, models.UpsertEnergyHours(db.DB, []models.EnergyHour{{SourceID: s.ID, Hour: local(2, clock).UTC(),
				LocalDate: "2026-11-02", KWh: kwh, Quality: models.EnergyQualityLive}}))
		}
	}
	// 10:00 uses 3 kWh of solar in a house total of 6; 20:00 is all grid.
	add("Inverter", models.EnergyRoleProduction, map[string]float64{"10:00": 4})
	add("Grid out", models.EnergyRoleExport, map[string]float64{"10:00": 1})
	add("Grid in", models.EnergyRoleImport, map[string]float64{"10:00": 3, "20:00": 4})

	share := func(from, to time.Time) any {
		req := tsdb.Request{Range: tsdb.Range{From: from, To: to}, Interval: time.Hour}
		req.Format = tsdb.FormatTable
		req.Energy = &tsdb.EnergyQuery{Targets: []string{TargetHouse}, Measure: MeasureSolarShare, Period: PeriodDay}
		res, err := Querier{}.Query(context.Background(), req)
		require.NoError(t, err)
		require.Len(t, res.Series, 1)
		return res.Series[0].Fields[1].Values[0]
	}
	assert.InDelta(t, 30, share(local(2, "00:00"), local(3, "00:00")), 1e-9, "the whole day used 3 kWh of solar in 10")
	assert.InDelta(t, 0, share(local(2, "12:00"), local(3, "12:00")), 1e-9, "the hours from noon used no solar, whatever the morning did")
}
