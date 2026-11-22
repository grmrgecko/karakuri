//go:build freebsd || dragonfly

package system

import "github.com/distatus/battery"

// maxBatteries bounds the BSD enumeration. A host with more packs than this is
// not a case worth the risk of an unbounded loop.
const maxBatteries = 16

// getAllBatteries enumerates the host's batteries one index at a time instead of
// calling battery.GetAll. The library's BSD GetAll loops `for i := 0; ; i++` and
// stops only on ENXIO (no battery at that index) or an all-fields-failed
// partial: an ACPI ioctl that returns EPERM — which is what an unprivileged
// agent gets for /dev/acpi — matches neither, so GetAll spins forever appending
// nil batteries and pins a core. battery.Get does a single ioctl with no loop,
// so iterating it ourselves and stopping on the first fatal error reaches the
// same result the loop was meant to and cannot hang.
func getAllBatteries() ([]*battery.Battery, error) {
	var out []*battery.Battery
	for i := 0; i < maxBatteries; i++ {
		b, err := battery.Get(i)
		if _, fatal := err.(battery.ErrFatal); fatal {
			// No battery at this index (ENXIO), or the controller is
			// unreadable (EPERM). Either way enumeration is done; an empty
			// result reads upstream as "no battery present".
			break
		}
		// A nil error is a full read and an ErrPartial is a real battery with
		// some fields missing; both are kept.
		out = append(out, b)
	}
	return out, nil
}
