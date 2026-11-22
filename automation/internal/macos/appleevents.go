//go:build darwin

package macos

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Logout, restart, shutdown, and sleep are AppleEvents delivered to the
// system process — the mechanism Apple documents for exactly this (Technical
// Q&A QA1134), and what the osascript "tell System Events" lines asked for
// with a process in between. AECreateDesc, AECreateAppleEvent, and
// AESendMessage are public CoreServices C functions, so the event is built
// and sent in-process; osascript stays behind it for a host that refuses the
// direct send.

// aeDesc mirrors AEDesc: a four-char type code and an opaque storage handle.
type aeDesc struct {
	DescriptorType uint32
	_              uint32
	DataHandle     uintptr
}

// Four-char codes and send options, from AERegistry.h and AEDataModel.h.
const (
	aeCoreEventClass    = 0x61657674 // 'aevt'
	aeEventRestart      = 0x72657374 // 'rest'
	aeEventShutDown     = 0x73687574 // 'shut'
	aeEventSleep        = 0x736c6570 // 'slep'
	aeEventReallyLogOut = 0x726c676f // 'rlgo', the logout that skips the dialog
	aeTypePSN           = 0x70736e20 // 'psn '

	aeNoReply              = 1
	aeDefaultTimeout       = -1
	aeAutoGenerateReturnID = -1
	aeSystemProcess        = 1
)

var (
	aeCreateDesc       func(uint32, unsafe.Pointer, int, unsafe.Pointer) int16
	aeCreateAppleEvent func(uint32, uint32, unsafe.Pointer, int16, int32, unsafe.Pointer) int16
	aeSendMessage      func(unsafe.Pointer, unsafe.Pointer, int32, int) int32
	aeDisposeDesc      func(unsafe.Pointer) int16

	aeOnce sync.Once
	aeErr  error
)

// loadAppleEvents binds the AE functions once, from CoreServices.
func loadAppleEvents() error {
	aeOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			aeErr = fmt.Errorf("opening CoreServices: %w", err)
			return
		}
		purego.RegisterLibFunc(&aeCreateDesc, lib, "AECreateDesc")
		purego.RegisterLibFunc(&aeCreateAppleEvent, lib, "AECreateAppleEvent")
		purego.RegisterLibFunc(&aeSendMessage, lib, "AESendMessage")
		purego.RegisterLibFunc(&aeDisposeDesc, lib, "AEDisposeDesc")
	})
	return aeErr
}

// sendSystemAppleEvent builds one core event addressed to the system process
// and sends it without waiting for a reply.
func sendSystemAppleEvent(eventID uint32) error {
	if err := loadAppleEvents(); err != nil {
		return err
	}

	psn := [2]uint32{0, aeSystemProcess}
	var target aeDesc
	if rc := aeCreateDesc(aeTypePSN, unsafe.Pointer(&psn), int(unsafe.Sizeof(psn)), unsafe.Pointer(&target)); rc != 0 {
		return fmt.Errorf("addressing the system process failed (AECreateDesc %d)", rc)
	}
	defer aeDisposeDesc(unsafe.Pointer(&target))

	var event aeDesc
	if rc := aeCreateAppleEvent(aeCoreEventClass, eventID, unsafe.Pointer(&target),
		aeAutoGenerateReturnID, 0, unsafe.Pointer(&event)); rc != 0 {
		return fmt.Errorf("building the event failed (AECreateAppleEvent %d)", rc)
	}
	defer aeDisposeDesc(unsafe.Pointer(&event))

	if rc := aeSendMessage(unsafe.Pointer(&event), nil, aeNoReply, aeDefaultTimeout); rc != 0 {
		return fmt.Errorf("the system declined the event (AESendMessage %d)", rc)
	}
	return nil
}
