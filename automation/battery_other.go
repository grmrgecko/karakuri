//go:build !darwin

package automation

import (
	"fmt"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// ReadBatteries has a native reader only on macOS, where the alternative is
// spawning ioreg; the other platforms' library paths are already native
// syscalls, so a caller keeps its own reader there.
func ReadBatteries() ([]BatteryPack, error) {
	return nil, fmt.Errorf("%w: no native battery reader on this platform", autoapi.ErrUnsupported)
}
