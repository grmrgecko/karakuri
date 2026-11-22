//go:build !windows && !darwin && !linux

package linuxdesk

// alsaVolume is the Linux kernel mixer; the other Unixes report unhandled so
// the caller walks on — FreeBSD's own kernel mixer sits in oss_freebsd.go.
func alsaVolume(action string, volume float64) (bool, error) { return false, nil }
