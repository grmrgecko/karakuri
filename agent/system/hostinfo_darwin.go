//go:build darwin

package system

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The library's host.Info runs two programs on macOS: sw_vers for the product
// version, and ioreg for a host id this node never reports. Every field the
// node does report is a sysctl or a uname away, so the reading is assembled
// here instead — one syscall each, nothing spawned. The values match what the
// library returns for the same fields, lowercasing included, so the payload
// does not change shape between platforms.

// hostIdentity reads the host's identity natively.
func hostIdentity(ctx context.Context) (hostFacts, error) {
	facts := hostFacts{OS: runtime.GOOS}

	if name, err := os.Hostname(); err == nil {
		facts.Hostname = name
	}
	if ostype, err := unix.Sysctl("kern.ostype"); err == nil {
		facts.Platform = strings.ToLower(ostype)
	}
	// kern.osproductversion is the marketing version sw_vers prints, on every
	// macOS the agent supports.
	if version, err := unix.Sysctl("kern.osproductversion"); err == nil {
		facts.PlatformVersion = strings.ToLower(strings.TrimSpace(version))
	}
	if release, err := unix.Sysctl("kern.osrelease"); err == nil {
		facts.KernelVersion = strings.ToLower(release)
	}
	var uname unix.Utsname
	if err := unix.Uname(&uname); err == nil {
		facts.KernelArch = unix.ByteSliceToString(uname.Machine[:])
	}
	// kern.boottime is when the kernel started; uptime is the distance from it.
	if boot, err := unix.SysctlTimeval("kern.boottime"); err == nil && boot.Sec > 0 {
		facts.BootTime = time.Unix(int64(boot.Sec), 0)
		facts.Uptime = uint64(time.Since(facts.BootTime).Seconds())
	}
	return facts, nil
}
