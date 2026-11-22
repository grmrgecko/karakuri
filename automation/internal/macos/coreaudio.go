//go:build darwin

package macos

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CoreAudio owns the output volume, and reading or setting it is a property
// call on the default output device — what "set volume output volume" reached
// through an osascript spawn per adjustment. A device whose volume lives on
// the channels rather than the main element (most of them) is handled by
// falling from element 0 to channels 1 and 2, the same walk the system's own
// volume slider does.

// audioPropertyAddress mirrors AudioObjectPropertyAddress.
type audioPropertyAddress struct {
	Selector uint32
	Scope    uint32
	Element  uint32
}

// CoreAudio four-character constants, big-endian.
const (
	kAudioObjectSystemObject = 1

	kAudioHardwarePropertyDefaultOutputDevice = 0x644F7574 // 'dOut'
	kAudioDevicePropertyVolumeScalar          = 0x766F6C6D // 'volm'
	kAudioDevicePropertyMute                  = 0x6D757465 // 'mute'

	kAudioObjectPropertyScopeGlobal = 0x676C6F62 // 'glob'
	kAudioObjectPropertyScopeOutput = 0x6F757470 // 'outp'
	kAudioObjectPropertyElementMain = 0
)

var (
	audioObjectGetPropertyData func(uint32, unsafe.Pointer, uint32, unsafe.Pointer, *uint32, unsafe.Pointer) int32
	audioObjectSetPropertyData func(uint32, unsafe.Pointer, uint32, unsafe.Pointer, uint32, unsafe.Pointer) int32
	audioObjectHasProperty     func(uint32, unsafe.Pointer) bool
)

var (
	coreAudioOnce sync.Once
	coreAudioErr  error
)

// loadCoreAudio binds the property functions once.
func loadCoreAudio() error {
	coreAudioOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			coreAudioErr = fmt.Errorf("opening CoreAudio: %w", err)
			return
		}
		purego.RegisterLibFunc(&audioObjectGetPropertyData, lib, "AudioObjectGetPropertyData")
		purego.RegisterLibFunc(&audioObjectSetPropertyData, lib, "AudioObjectSetPropertyData")
		purego.RegisterLibFunc(&audioObjectHasProperty, lib, "AudioObjectHasProperty")
	})
	return coreAudioErr
}

// defaultOutputDevice reads the system's default output device id.
func defaultOutputDevice() (uint32, error) {
	if err := loadCoreAudio(); err != nil {
		return 0, err
	}
	address := audioPropertyAddress{
		Selector: kAudioHardwarePropertyDefaultOutputDevice,
		Scope:    kAudioObjectPropertyScopeGlobal,
		Element:  kAudioObjectPropertyElementMain,
	}
	var device uint32
	size := uint32(unsafe.Sizeof(device))
	status := audioObjectGetPropertyData(kAudioObjectSystemObject,
		unsafe.Pointer(&address), 0, nil, &size, unsafe.Pointer(&device))
	if status != 0 || device == 0 {
		return 0, fmt.Errorf("no default output device (status %d)", status)
	}
	return device, nil
}

// volumeElements returns the elements the device's output volume lives on:
// the main element when it has one, otherwise the first two channels.
func volumeElements(device uint32, selector uint32) []uint32 {
	address := audioPropertyAddress{
		Selector: selector,
		Scope:    kAudioObjectPropertyScopeOutput,
		Element:  kAudioObjectPropertyElementMain,
	}
	if audioObjectHasProperty(device, unsafe.Pointer(&address)) {
		return []uint32{kAudioObjectPropertyElementMain}
	}
	return []uint32{1, 2}
}

// macOutputVolume reads the output volume as a 0 to 100 percentage, averaging
// the channels when the device has no main volume.
func macOutputVolume() (float64, error) {
	device, err := defaultOutputDevice()
	if err != nil {
		return 0, err
	}
	elements := volumeElements(device, kAudioDevicePropertyVolumeScalar)
	var total float64
	var read int
	for _, element := range elements {
		address := audioPropertyAddress{
			Selector: kAudioDevicePropertyVolumeScalar,
			Scope:    kAudioObjectPropertyScopeOutput,
			Element:  element,
		}
		var scalar float32
		size := uint32(unsafe.Sizeof(scalar))
		if audioObjectGetPropertyData(device, unsafe.Pointer(&address), 0, nil, &size, unsafe.Pointer(&scalar)) == 0 {
			total += float64(scalar)
			read++
		}
	}
	if read == 0 {
		return 0, fmt.Errorf("this output device reports no volume")
	}
	return total / float64(read) * 100, nil
}

// setMacOutputVolume sets the output volume from a 0 to 100 percentage.
func setMacOutputVolume(percent float64) error {
	device, err := defaultOutputDevice()
	if err != nil {
		return err
	}
	scalar := float32(percent / 100)
	if scalar < 0 {
		scalar = 0
	}
	if scalar > 1 {
		scalar = 1
	}
	var set bool
	for _, element := range volumeElements(device, kAudioDevicePropertyVolumeScalar) {
		address := audioPropertyAddress{
			Selector: kAudioDevicePropertyVolumeScalar,
			Scope:    kAudioObjectPropertyScopeOutput,
			Element:  element,
		}
		size := uint32(unsafe.Sizeof(scalar))
		if audioObjectSetPropertyData(device, unsafe.Pointer(&address), 0, nil, size, unsafe.Pointer(&scalar)) == 0 {
			set = true
		}
	}
	if !set {
		return fmt.Errorf("this output device does not accept a volume")
	}
	return nil
}

// macOutputMuted reads the device's mute switch.
func macOutputMuted() (bool, error) {
	device, err := defaultOutputDevice()
	if err != nil {
		return false, err
	}
	for _, element := range volumeElements(device, kAudioDevicePropertyMute) {
		address := audioPropertyAddress{
			Selector: kAudioDevicePropertyMute,
			Scope:    kAudioObjectPropertyScopeOutput,
			Element:  element,
		}
		var muted uint32
		size := uint32(unsafe.Sizeof(muted))
		if audioObjectGetPropertyData(device, unsafe.Pointer(&address), 0, nil, &size, unsafe.Pointer(&muted)) == 0 {
			return muted != 0, nil
		}
	}
	return false, fmt.Errorf("this output device has no mute switch")
}

// setMacOutputMuted sets the device's mute switch.
func setMacOutputMuted(mute bool) error {
	device, err := defaultOutputDevice()
	if err != nil {
		return err
	}
	var value uint32
	if mute {
		value = 1
	}
	var set bool
	for _, element := range volumeElements(device, kAudioDevicePropertyMute) {
		address := audioPropertyAddress{
			Selector: kAudioDevicePropertyMute,
			Scope:    kAudioObjectPropertyScopeOutput,
			Element:  element,
		}
		size := uint32(unsafe.Sizeof(value))
		if audioObjectSetPropertyData(device, unsafe.Pointer(&address), 0, nil, size, unsafe.Pointer(&value)) == 0 {
			set = true
		}
	}
	if !set {
		return fmt.Errorf("this output device has no mute switch")
	}
	return nil
}
