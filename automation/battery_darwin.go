//go:build darwin

package automation

import (
	"math"

	"github.com/grmrgecko/karakuri/automation/internal/macos"
)

// ReadBatteries reports the host's battery packs straight from the IO
// registry, the entries ioreg dumps. A desktop Mac answers an empty list. The
// unit math and the state derivation mirror what distatus/battery reads out of
// the same properties, so a caller migrating from it sees the same numbers.
func ReadBatteries() ([]BatteryPack, error) {
	readings, err := macos.ReadBatteries()
	if err != nil {
		return nil, err
	}
	packs := make([]BatteryPack, len(readings))
	for i, r := range readings {
		volts := float64(r.VoltageMV) / 1000
		packs[i] = BatteryPack{
			State:        batteryState(r),
			CurrentMWh:   float64(r.CurrentMAh) * volts,
			FullMWh:      float64(r.MaxMAh) * volts,
			DesignMWh:    float64(r.DesignMAh) * volts,
			ChargeRateMW: math.Abs(float64(r.AmperageMA)) * volts,
			Voltage:      volts,
		}
	}
	return packs, nil
}

// batteryState reduces the controller's flags to one state, in the order the
// flags override each other: unplugged means discharging whatever else the
// controller still reports.
func batteryState(r macos.BatteryReading) string {
	switch {
	case !r.ExternalConnected:
		return BatteryDischarging
	case r.IsCharging:
		return BatteryCharging
	case r.CurrentMAh == 0:
		return BatteryEmpty
	case !r.FullyCharged:
		return BatteryIdle
	default:
		return BatteryFull
	}
}
