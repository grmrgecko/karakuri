//go:build darwin

package macos

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// The battery controller publishes itself in the IO registry as
// AppleSmartBattery, the same entries ioreg dumps. Reading the properties
// directly replaces spawning ioreg and parsing its plist, and a host with no
// battery — every desktop Mac — simply matches no service.

// BatteryReading is one controller's raw report: capacities in mAh, voltage
// in mV, and the momentary current in mA, negative while discharging.
type BatteryReading struct {
	CurrentMAh        int
	MaxMAh            int
	DesignMAh         int
	VoltageMV         int
	AmperageMA        int
	IsCharging        bool
	ExternalConnected bool
	FullyCharged      bool
}

var (
	ioServiceGetMatchingServices func(uint32, uintptr, *uint32) int32
	ioIteratorNext               func(uint32) uint32
)

var (
	batteryOnce sync.Once
	batteryErr  error
)

// loadBattery binds the iteration functions once, on top of the IOKit loader.
func loadBattery() error {
	if err := loadIOKit(); err != nil {
		return err
	}
	batteryOnce.Do(func() {
		iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			batteryErr = fmt.Errorf("opening IOKit: %w", err)
			return
		}
		purego.RegisterLibFunc(&ioServiceGetMatchingServices, iokit, "IOServiceGetMatchingServices")
		purego.RegisterLibFunc(&ioIteratorNext, iokit, "IOIteratorNext")
	})
	return batteryErr
}

// ReadBatteries reports every AppleSmartBattery controller in the IO registry.
// A host with none answers an empty list, which is a desktop rather than a
// failure.
func ReadBatteries() ([]BatteryReading, error) {
	if err := loadBattery(); err != nil {
		return nil, err
	}

	// The matching dictionary is consumed by the lookup; the iterator and each
	// service handle need releasing.
	var iterator uint32
	if rc := ioServiceGetMatchingServices(0, ioServiceMatching("AppleSmartBattery"), &iterator); rc != 0 {
		return nil, fmt.Errorf("the IO registry refused the battery lookup (%#x)", rc)
	}
	defer ioObjectRelease(iterator)

	var readings []BatteryReading
	for {
		service := ioIteratorNext(iterator)
		if service == 0 {
			break
		}
		readings = append(readings, readBatteryService(service))
		ioObjectRelease(service)
	}
	return readings, nil
}

// readBatteryService reads one controller's properties. A key the controller
// does not publish reads as zero, the same answer an absent plist key gave.
func readBatteryService(service uint32) BatteryReading {
	return BatteryReading{
		CurrentMAh:        batteryNumber(service, "AppleRawCurrentCapacity"),
		MaxMAh:            batteryNumber(service, "AppleRawMaxCapacity"),
		DesignMAh:         batteryNumber(service, "DesignCapacity"),
		VoltageMV:         batteryNumber(service, "Voltage"),
		AmperageMA:        batteryNumber(service, "Amperage"),
		IsCharging:        batteryBool(service, "IsCharging"),
		ExternalConnected: batteryBool(service, "ExternalConnected"),
		FullyCharged:      batteryBool(service, "FullyCharged"),
	}
}

// batteryNumber reads one CFNumber property off a registry entry.
func batteryNumber(service uint32, name string) int {
	key := cfStr(name)
	defer cfRelease(key)
	value := ioRegistryEntryCreateProperty(service, key, 0, 0)
	if value == 0 {
		return 0
	}
	defer cfRelease(value)
	return cfNumberInt(value)
}

// batteryBool reads one CFBoolean property off a registry entry.
func batteryBool(service uint32, name string) bool {
	key := cfStr(name)
	defer cfRelease(key)
	value := ioRegistryEntryCreateProperty(service, key, 0, 0)
	if value == 0 {
		return false
	}
	defer cfRelease(value)
	return value == kCFBooleanTrueRef
}
