//go:build !freebsd && !dragonfly && !darwin

package system

import "github.com/distatus/battery"

// getAllBatteries reads every battery the host reports. Everywhere but the
// BSDs, whose enumeration has its own file, and macOS, which reads the IO
// registry natively, the library's own enumeration is a straight passthrough.
func getAllBatteries() ([]*battery.Battery, error) {
	return battery.GetAll()
}
