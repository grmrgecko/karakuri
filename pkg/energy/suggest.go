package energy

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
)

// Candidate is a reading that could become an energy source.
type Candidate struct {
	Name        string   `json:"name" description:"Suggested source name."`
	SourceKind  string   `json:"source_kind" description:"control, device, or ha."`
	SourceID    string   `json:"source_id" description:"Control or device UUID, or the Home Assistant connection UUID."`
	EntityID    string   `json:"entity_id,omitempty" description:"Home Assistant entity."`
	Attribute   string   `json:"attribute,omitempty" description:"Attribute read."`
	Measurement string   `json:"measurement" description:"energy or power."`
	Unit        string   `json:"unit" description:"Unit of the reading."`
	Value       *float64 `json:"value,omitempty" description:"Current reading."`
	SourceName  string   `json:"source_name" description:"Name of the device, control, or connection."`
	Added       bool     `json:"added" description:"Whether a source already reads it."`
}

// unitMeasurement says whether a unit is energy or power.
func unitMeasurement(unit string) (string, bool) {
	if milliUnit(unit) {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "wh", "kwh", "mwh":
		return models.EnergyMeasureEnergy, true
	case "w", "kw", "mw":
		return models.EnergyMeasurePower, true
	}
	return "", false
}

// deviceKeys are state keys Zigbee2MQTT and Tasmota-style devices use for
// energy and power, with the unit they report in.
var deviceKeys = []struct{ key, measurement, unit string }{
	{"energy", models.EnergyMeasureEnergy, "kWh"},
	{"energy_kwh", models.EnergyMeasureEnergy, "kWh"},
	{"total_energy", models.EnergyMeasureEnergy, "kWh"},
	{"power", models.EnergyMeasurePower, "W"},
	{"active_power", models.EnergyMeasurePower, "W"},
	{"power_w", models.EnergyMeasurePower, "W"},
}

// Candidates lists devices, controls, and Home Assistant entities that read
// energy or power.
func Candidates(ctx context.Context) ([]Candidate, error) {
	existing, err := models.ListEnergySources()
	if err != nil {
		return nil, err
	}
	added := map[string]bool{}
	for _, s := range existing {
		added[candidateKey(s.SourceKind, s.SourceID, s.EntityID, s.Attribute)] = true
	}
	var out []Candidate
	push := func(c Candidate) {
		c.Added = added[candidateKey(c.SourceKind, c.SourceID, c.EntityID, c.Attribute)]
		out = append(out, c)
	}

	devices, err := models.ListMQTTDevices()
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		var state map[string]any
		if mqttrelay.D != nil {
			state, _ = mqttrelay.D.State(d.ID)
		}
		if unit, _ := d.OptionMap()["unit"].(string); d.Control == controls.KindNumber {
			if m, ok := unitMeasurement(unit); ok {
				push(Candidate{Name: d.Name, SourceKind: models.EnergyKindDevice, SourceID: d.ID.String(), Attribute: "value",
					Measurement: m, Unit: unit, Value: number(state["value"]), SourceName: d.Name})
			}
		}
		for _, k := range deviceKeys {
			if _, ok := state[k.key]; !ok {
				continue
			}
			push(Candidate{Name: d.Name + " " + k.key, SourceKind: models.EnergyKindDevice, SourceID: d.ID.String(),
				Attribute: k.key, Measurement: k.measurement, Unit: k.unit, Value: number(state[k.key]), SourceName: d.Name})
		}
	}

	if controls.S != nil {
		for _, c := range controls.S.List() {
			if c.Kind != controls.KindNumber {
				continue
			}
			unit, _ := c.Options["unit"].(string)
			if m, ok := unitMeasurement(unit); ok {
				push(Candidate{Name: c.Name, SourceKind: models.EnergyKindControl, SourceID: c.ID.String(),
					Measurement: m, Unit: unit, Value: number(c.Value), SourceName: c.Name})
			}
		}
	}

	conns, err := models.ListConnections()
	if err != nil {
		return nil, err
	}
	for _, conn := range conns {
		if conn.Kind != homeassistant.Kind {
			continue
		}
		driver, err := connops.HADriver(conn.ID.String())
		if err != nil {
			continue
		}
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		states, err := driver.StatesCached(hctx)
		cancel()
		if err != nil {
			continue
		}
		for _, st := range states {
			if !strings.HasPrefix(st.EntityID, "sensor.") {
				continue
			}
			unit, _ := st.Attributes["unit_of_measurement"].(string)
			m, ok := unitMeasurement(unit)
			if !ok {
				continue
			}
			name, _ := st.Attributes["friendly_name"].(string)
			if name == "" {
				name = st.EntityID
			}
			push(Candidate{Name: name, SourceKind: models.EnergyKindHA, SourceID: conn.ID.String(), EntityID: st.EntityID,
				Measurement: m, Unit: unit, Value: number(st.State), SourceName: conn.Name})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Measurement != out[j].Measurement {
			return out[i].Measurement == models.EnergyMeasureEnergy
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func candidateKey(kind, id, entity, attr string) string {
	return kind + "|" + strings.ToLower(id) + "|" + entity + "|" + attr
}

func number(v any) *float64 {
	f, ok := toNumber(v)
	if !ok {
		return nil
	}
	return &f
}
