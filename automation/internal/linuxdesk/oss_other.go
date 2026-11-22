//go:build !windows && !darwin && !freebsd

package linuxdesk

// ossVolume is FreeBSD's kernel mixer; everywhere else it reports unhandled so
// the caller walks the tool chain.
func ossVolume(action string, volume float64) (bool, error) { return false, nil }

// acpiSuspend is FreeBSD's sleep request; everywhere else logind or the tools
// answer instead.
func acpiSuspend() (bool, error) { return false, nil }

// initSignalPower is FreeBSD's orderly init signal; on Linux logind owns the
// verb and a sysvinit host keeps its shutdown tool.
func initSignalPower(action string) (bool, error) { return false, nil }
