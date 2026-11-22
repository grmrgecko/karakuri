package linuxdesk

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Bare ALSA — a Linux host running no sound server — is driven through the
// control ioctls on /dev/snd/controlC*, the calls amixer makes. The kernel
// looks an element up by interface and name when the numeric id is zero, so
// no enumeration is needed: this asks for the Master (or PCM) playback
// element by name, exactly as `amixer set Master` resolves it. The structs
// mirror sound/asound.h; C long is Go int on every Linux ABI the agent
// ships (LP64 and 32-bit ARM), which is what keeps one layout correct on all
// of them.

// alsaElemID is struct snd_ctl_elem_id: 64 bytes on every Linux ABI.
type alsaElemID struct {
	NumID     uint32
	Iface     uint32
	Device    uint32
	Subdevice uint32
	Name      [44]byte
	Index     uint32
}

// alsaElemInfo is struct snd_ctl_elem_info. The value union is read as the
// integer case: min, max, step as C long.
type alsaElemInfo struct {
	ID       alsaElemID
	Type     uint32
	Access   uint32
	Count    uint32
	Owner    int32
	Min      int
	Max      int
	Step     int
	_        [128 - 3*unsafe.Sizeof(int(0))]byte // rest of the 128-byte union
	Reserved [64]byte
}

// alsaElemValue is struct snd_ctl_elem_value; Value is the union's long[128].
type alsaElemValue struct {
	ID       alsaElemID
	Indirect uint32
	_        uint32 // pad to the union's 8-byte alignment
	Value    [128]int
	Reserved [128]byte
}

// alsaIoc builds a _IOWR('U', nr, size) ioctl number.
func alsaIoc(nr, size uintptr) uintptr {
	const iocRead, iocWrite = 2, 1
	return (iocRead|iocWrite)<<30 | size<<16 | 'U'<<8 | nr
}

var (
	alsaElemInfoIoctl  = alsaIoc(0x11, unsafe.Sizeof(alsaElemInfo{}))
	alsaElemReadIoctl  = alsaIoc(0x12, unsafe.Sizeof(alsaElemValue{}))
	alsaElemWriteIoctl = alsaIoc(0x13, unsafe.Sizeof(alsaElemValue{}))
)

const (
	alsaIfaceMixer  = 2
	alsaTypeBoolean = 1
	alsaTypeInteger = 2
)

// alsaControl is one opened control device plus the resolved playback element.
type alsaControl struct {
	fd     int
	volume alsaElemInfo
	// switchInfo is the matching mute switch, count zero when the card has none.
	switchInfo alsaElemInfo
}

// alsaIoctl performs one control ioctl.
func alsaIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// alsaElemByName asks the kernel for a mixer element's info by name.
func alsaElemByName(fd int, name string) (alsaElemInfo, error) {
	var info alsaElemInfo
	info.ID.Iface = alsaIfaceMixer
	copy(info.ID.Name[:], name)
	if err := alsaIoctl(fd, alsaElemInfoIoctl, unsafe.Pointer(&info)); err != nil {
		return info, err
	}
	return info, nil
}

// openAlsaControl finds the first card exposing a playback volume, Master
// before PCM, the order amixer's own defaults resolve. The caller closes fd.
func openAlsaControl() (*alsaControl, bool) {
	for card := 0; card < 32; card++ {
		fd, err := unix.Open(fmt.Sprintf("/dev/snd/controlC%d", card), unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		for _, name := range []string{"Master Playback Volume", "PCM Playback Volume"} {
			info, err := alsaElemByName(fd, name)
			if err != nil || info.Type != alsaTypeInteger || info.Count == 0 || info.Max <= info.Min {
				continue
			}
			ctl := &alsaControl{fd: fd, volume: info}
			switchName := strings.Replace(name, "Volume", "Switch", 1)
			if sw, err := alsaElemByName(fd, switchName); err == nil && sw.Type == alsaTypeBoolean && sw.Count > 0 {
				ctl.switchInfo = sw
			}
			return ctl, true
		}
		_ = unix.Close(fd)
	}
	return nil, false
}

// readPercent reads the element and reports the loudest channel as a
// percentage of its range, the number the step actions adjust from.
func (c *alsaControl) readPercent() (float64, error) {
	var value alsaElemValue
	value.ID = c.volume.ID
	if err := alsaIoctl(c.fd, alsaElemReadIoctl, unsafe.Pointer(&value)); err != nil {
		return 0, fmt.Errorf("reading the ALSA volume: %w", err)
	}
	raw := value.Value[0]
	for i := 1; i < int(c.volume.Count) && i < len(value.Value); i++ {
		if value.Value[i] > raw {
			raw = value.Value[i]
		}
	}
	span := c.volume.Max - c.volume.Min
	return float64(raw-c.volume.Min) * 100 / float64(span), nil
}

// writePercent sets every channel to one level.
func (c *alsaControl) writePercent(percent float64) error {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	span := float64(c.volume.Max - c.volume.Min)
	raw := c.volume.Min + int(percent*span/100+0.5)

	var value alsaElemValue
	value.ID = c.volume.ID
	for i := 0; i < int(c.volume.Count) && i < len(value.Value); i++ {
		value.Value[i] = raw
	}
	if err := alsaIoctl(c.fd, alsaElemWriteIoctl, unsafe.Pointer(&value)); err != nil {
		return fmt.Errorf("the ALSA mixer refused the volume: %w", err)
	}
	return nil
}

// setSwitch drives the mute switch: on is audible, matching ALSA's sense.
func (c *alsaControl) setSwitch(mode string) error {
	if c.switchInfo.Count == 0 {
		return fmt.Errorf("this card has no mute switch; set the volume instead")
	}
	var value alsaElemValue
	value.ID = c.switchInfo.ID
	if err := alsaIoctl(c.fd, alsaElemReadIoctl, unsafe.Pointer(&value)); err != nil {
		return fmt.Errorf("reading the ALSA mute switch: %w", err)
	}
	for i := 0; i < int(c.switchInfo.Count) && i < len(value.Value); i++ {
		switch mode {
		case mediaMute:
			value.Value[i] = 0
		case mediaUnmute:
			value.Value[i] = 1
		case mediaMuteToggle:
			value.Value[i] = 1 - (value.Value[i] & 1)
		}
	}
	if err := alsaIoctl(c.fd, alsaElemWriteIoctl, unsafe.Pointer(&value)); err != nil {
		return fmt.Errorf("the ALSA mixer refused the mute switch: %w", err)
	}
	return nil
}

// alsaVolume performs one volume action against the first card's playback
// element. It reports unhandled only when no control device offers one, which
// sends the caller to the tool chain.
func alsaVolume(action string, volume float64) (bool, error) {
	ctl, ok := openAlsaControl()
	if !ok {
		return false, nil
	}
	defer unix.Close(ctl.fd)

	switch action {
	case mediaVolumeSet:
		return true, ctl.writePercent(volume)
	case mediaVolumeUp, mediaVolumeDown:
		current, err := ctl.readPercent()
		if err != nil {
			return true, err
		}
		step := float64(volumeStep)
		if action == mediaVolumeDown {
			step = -step
		}
		return true, ctl.writePercent(current + step)
	case mediaMute, mediaUnmute, mediaMuteToggle:
		return true, ctl.setSwitch(action)
	default:
		return true, fmt.Errorf("unknown volume action %q", action)
	}
}
