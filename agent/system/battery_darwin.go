//go:build darwin

package system

import (
	"github.com/distatus/battery"

	"github.com/grmrgecko/karakuri/automation"
)

// getAllBatteries reads the battery controllers straight from the IO registry
// through the automation module, where the macOS bindings live, instead of
// letting the library spawn ioreg and parse its plist. The results are shaped
// into the library's types, which stay the node's vocabulary everywhere.
func getAllBatteries() ([]*battery.Battery, error) {
	packs, err := automation.ReadBatteries()
	if err != nil {
		return nil, battery.ErrFatal{Err: err}
	}
	batteries := make([]*battery.Battery, len(packs))
	for i, p := range packs {
		batteries[i] = &battery.Battery{
			State:         battery.State{Raw: batteryAgnosticState(p.State)},
			Current:       p.CurrentMWh,
			Full:          p.FullMWh,
			Design:        p.DesignMWh,
			ChargeRate:    p.ChargeRateMW,
			Voltage:       p.Voltage,
			DesignVoltage: p.Voltage,
		}
	}
	return batteries, nil
}

// batteryAgnosticState maps the automation module's state strings onto the
// library's enumeration.
func batteryAgnosticState(state string) battery.AgnosticState {
	switch state {
	case automation.BatteryCharging:
		return battery.Charging
	case automation.BatteryDischarging:
		return battery.Discharging
	case automation.BatteryFull:
		return battery.Full
	case automation.BatteryIdle:
		return battery.Idle
	case automation.BatteryEmpty:
		return battery.Empty
	default:
		return battery.Undefined
	}
}
