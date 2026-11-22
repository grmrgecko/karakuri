package linuxdesk

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// FreeBSD's kernel owns the mixer and the sleep request, so both are one ioctl
// on a device node — the calls the mixer and acpiconf tools make. Making them
// directly means a host with no extra tools installed still answers; the tool
// chains stay behind these for a host that denies the device instead.

// OSS ioctls, from sys/soundcard.h: MIXER_READ(SOUND_MIXER_VOLUME) and
// MIXER_WRITE(SOUND_MIXER_VOLUME). The value packs the left channel in the low
// byte and the right in the next, each 0-100.
const (
	ossMixerReadVolume  = 0x40044d00
	ossMixerWriteVolume = 0xc0044d00
)

// acpiReqSleepState is ACPIIO_REQSLPSTATE from dev/acpica/acpiio.h: request
// the sleep state the argument names, S3 being suspend-to-RAM.
const (
	acpiReqSleepState = 0x80045004
	acpiSleepS3       = 3
)

// ossVolume performs one volume action against the kernel mixer. It reports
// unhandled only when /dev/mixer cannot be opened, which sends the caller to
// the tool chain; an error from a device that answered is the answer.
func ossVolume(action string, volume float64) (bool, error) {
	fd, err := unix.Open("/dev/mixer", unix.O_RDWR, 0)
	if err != nil {
		return false, nil
	}
	defer unix.Close(fd)

	setBoth := func(level float64) error {
		v := int(level)
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		if err := unix.IoctlSetPointerInt(fd, ossMixerWriteVolume, v|v<<8); err != nil {
			return fmt.Errorf("the kernel mixer refused the volume: %w", err)
		}
		return nil
	}
	read := func() (float64, error) {
		v, err := unix.IoctlGetInt(fd, ossMixerReadVolume)
		if err != nil {
			return 0, fmt.Errorf("the kernel mixer would not report the volume: %w", err)
		}
		// The louder channel stands for the pair, as the step actions rebalance
		// both to one level anyway.
		left, right := v&0xff, (v>>8)&0xff
		return float64(max(left, right)), nil
	}

	switch action {
	case mediaVolumeSet:
		return true, setBoth(volume)
	case mediaVolumeUp:
		current, err := read()
		if err != nil {
			return true, err
		}
		return true, setBoth(current + volumeStep)
	case mediaVolumeDown:
		current, err := read()
		if err != nil {
			return true, err
		}
		return true, setBoth(current - volumeStep)
	case mediaMute:
		// The OSS mixer keeps no mute state, so muting means zero — the same
		// answer the mixer tool gives.
		return true, setBoth(0)
	default:
		return true, fmt.Errorf("the kernel mixer keeps no mute state, so it cannot %s; set the volume instead", action)
	}
}

// initSignalPower asks init for an orderly restart or poweroff — the signals
// reboot(8) and poweroff(8) deliver, so rc's shutdown sequence still runs.
// Only root may signal init; anyone else reports unhandled and walks on to
// the tools, which will name their own refusal.
func initSignalPower(action string) (bool, error) {
	var sig unix.Signal
	switch action {
	case powerRestart:
		sig = unix.SIGINT
	case powerShutdown:
		sig = unix.SIGUSR2
	default:
		return false, nil
	}
	if err := unix.Kill(1, sig); err != nil {
		if err == unix.EPERM {
			return false, nil
		}
		return true, fmt.Errorf("signaling init: %w", err)
	}
	return true, nil
}

// acpiSuspend requests suspend-to-RAM through /dev/acpi, the ioctl acpiconf -s
// 3 makes. It reports unhandled when the device cannot be opened — a host
// without ACPI, or an agent running as a user outside the operator group —
// which sends the caller to the tools.
func acpiSuspend() (bool, error) {
	fd, err := unix.Open("/dev/acpi", unix.O_RDWR, 0)
	if err != nil {
		return false, nil
	}
	defer unix.Close(fd)
	if err := unix.IoctlSetPointerInt(fd, acpiReqSleepState, acpiSleepS3); err != nil {
		return true, fmt.Errorf("the kernel refused the sleep request: %w", err)
	}
	return true, nil
}
