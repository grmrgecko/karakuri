package automation

// BatteryPack is one battery as ReadBatteries reports it: energies in mWh,
// the charge rate in mW, and the voltage in volts, the units the platform
// libraries normalize to.
type BatteryPack struct {
	// State is one of "charging", "discharging", "full", "idle", "empty", or
	// "undefined" when the controller's flags describe none of them.
	State        string
	CurrentMWh   float64
	FullMWh      float64
	DesignMWh    float64
	ChargeRateMW float64
	Voltage      float64
}

// Battery pack states.
const (
	BatteryCharging    = "charging"
	BatteryDischarging = "discharging"
	BatteryFull        = "full"
	BatteryIdle        = "idle"
	BatteryEmpty       = "empty"
	BatteryUndefined   = "undefined"
)
