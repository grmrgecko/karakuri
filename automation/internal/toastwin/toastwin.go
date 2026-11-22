//go:build windows

// Package toastwin shows Windows toast notifications through the Windows
// Runtime, speaking COM directly: combase.dll's activation functions, HSTRING
// marshalling, and the five vtable calls a display-only toast crosses. Nothing
// is spawned and nothing outside golang.org/x/sys is imported. The interface
// identifiers and vtable layouts come from the Windows SDK headers
// (windows.ui.notifications.h, windows.data.xml.dom.h).
package toastwin

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// combase.dll carries the Windows Runtime's C entry points.
var (
	combase                 = windows.NewLazySystemDLL("combase.dll")
	procRoInitialize        = combase.NewProc("RoInitialize")
	procRoActivateInstance  = combase.NewProc("RoActivateInstance")
	procRoGetActivationFty  = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString = combase.NewProc("WindowsDeleteString")
)

// The WinRT interfaces a toast crosses, from the SDK headers.
var (
	iidToastManagerStatics5 = windows.GUID{Data1: 0xd6f5f569, Data2: 0xd40d, Data3: 0x407c, Data4: [8]byte{0x89, 0x89, 0x88, 0xca, 0xb4, 0x2c, 0xfd, 0x14}}
	iidToastManagerForUser  = windows.GUID{Data1: 0x79ab57f6, Data2: 0x43fe, Data3: 0x487b, Data4: [8]byte{0x8a, 0x7f, 0x99, 0x56, 0x72, 0x00, 0xae, 0x94}}
	iidToastNotifier        = windows.GUID{Data1: 0x75927b93, Data2: 0x03f3, Data3: 0x41ec, Data4: [8]byte{0x91, 0xd3, 0x6e, 0x5b, 0xac, 0x1b, 0x38, 0xe7}}
	iidToastFactory         = windows.GUID{Data1: 0x04124b20, Data2: 0x82c6, Data3: 0x4229, Data4: [8]byte{0xb1, 0x09, 0xfd, 0x9e, 0xd4, 0x66, 0x2b, 0x53}}
	iidXmlDocumentIO        = windows.GUID{Data1: 0x6cd0e74e, Data2: 0xee65, Data3: 0x4489, Data4: [8]byte{0x9e, 0xbf, 0xca, 0x43, 0xe8, 0x7b, 0xa6, 0x37}}
)

// hresultError turns a failing HRESULT into an error naming the call.
func hresultError(op string, hr uintptr) error {
	return fmt.Errorf("%s: HRESULT 0x%08X", op, uint32(hr))
}

// failed reports whether an HRESULT carries the failure bit; S_FALSE does not.
func failed(hr uintptr) bool {
	return hr&0x80000000 != 0
}

// hstring is WinRT's string handle. An empty string is a null handle, which
// the runtime treats as the empty string.
type hstring uintptr

func newHString(s string) (hstring, error) {
	utf16, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h hstring
	hr, _, _ := procWindowsCreateString.Call(
		uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1),
		uintptr(unsafe.Pointer(&h)))
	if failed(hr) {
		return 0, hresultError("WindowsCreateString", hr)
	}
	return h, nil
}

func (h hstring) delete() {
	if h != 0 {
		_, _, _ = procWindowsDeleteString.Call(uintptr(h))
	}
}

// comObject is any COM interface pointer: a pointer to a vtable pointer. The
// concrete vtable is read at whichever layout the call site knows it by.
type comObject struct {
	vtbl *unknownVtbl
}

// unknownVtbl is IUnknown, the head of every COM vtable.
type unknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

// inspectableVtbl is IInspectable, the head of every WinRT vtable: IUnknown
// plus three slots this package never calls but must count past.
type inspectableVtbl struct {
	unknownVtbl
	GetIids             uintptr
	GetRuntimeClassName uintptr
	GetTrustLevel       uintptr
}

// query asks the object for another of its interfaces.
func (o *comObject) query(iid *windows.GUID, op string) (*comObject, error) {
	var out *comObject
	hr, _, _ := syscall.SyscallN(o.vtbl.QueryInterface,
		uintptr(unsafe.Pointer(o)), uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&out)))
	if failed(hr) {
		return nil, hresultError(op, hr)
	}
	return out, nil
}

func (o *comObject) release() {
	if o != nil {
		_, _, _ = syscall.SyscallN(o.vtbl.Release, uintptr(unsafe.Pointer(o)))
	}
}

// Method slots past IInspectable, one struct per interface.
type (
	managerStatics5Vtbl struct {
		inspectableVtbl
		GetDefault uintptr
	}
	managerForUserVtbl struct {
		inspectableVtbl
		CreateToastNotifier       uintptr
		CreateToastNotifierWithId uintptr
		GetHistory                uintptr
		GetUser                   uintptr
	}
	notifierVtbl struct {
		inspectableVtbl
		Show uintptr
	}
	toastFactoryVtbl struct {
		inspectableVtbl
		CreateToastNotification uintptr
	}
	xmlDocumentIOVtbl struct {
		inspectableVtbl
		LoadXml uintptr
	}
)

// initialized gates RoInitialize to one call: reinitializing an already
// initialized runtime answers with an error rather than a no-op.
var initialized atomic.Bool

func initialize() error {
	// Windows 7 has no WinRT DLL. Resolve the optional entry points before
	// calling them so the caller can fall back to a tray balloon.
	for _, proc := range []*windows.LazyProc{
		procRoInitialize, procRoActivateInstance, procRoGetActivationFty,
		procWindowsCreateString, procWindowsDeleteString,
	} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("windows runtime unavailable: %w", err)
		}
	}
	if initialized.CompareAndSwap(false, true) {
		const roInitTypeMultithreaded = 1
		hr, _, _ := procRoInitialize.Call(roInitTypeMultithreaded)
		if failed(hr) {
			initialized.Store(false)
			return hresultError("RoInitialize", hr)
		}
	}
	return nil
}

// activateInstance creates a WinRT class instance by its runtime class name.
func activateInstance(className string) (*comObject, error) {
	name, err := newHString(className)
	if err != nil {
		return nil, err
	}
	defer name.delete()
	var out *comObject
	hr, _, _ := procRoActivateInstance.Call(uintptr(name), uintptr(unsafe.Pointer(&out)))
	if failed(hr) {
		return nil, hresultError("RoActivateInstance("+className+")", hr)
	}
	return out, nil
}

// activationFactory fetches a runtime class's factory or statics interface.
func activationFactory(className string, iid *windows.GUID) (*comObject, error) {
	name, err := newHString(className)
	if err != nil {
		return nil, err
	}
	defer name.delete()
	var out *comObject
	hr, _, _ := procRoGetActivationFty.Call(uintptr(name),
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if failed(hr) {
		return nil, hresultError("RoGetActivationFactory("+className+")", hr)
	}
	return out, nil
}

// call invokes one vtable slot on an object.
func call(op string, slot uintptr, args ...uintptr) error {
	hr, _, _ := syscall.SyscallN(slot, args...)
	if failed(hr) {
		return hresultError(op, hr)
	}
	return nil
}

// RegisterAppID records the application id under HKCU so the notification
// platform accepts toasts from it — an unregistered id is dropped without an
// error. Only the display name is written: a CustomActivator entry naming a
// COM class nothing serves is worse than none.
func RegisterAppID(appID string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER,
		`SOFTWARE\Classes\AppUserModelId\`+appID, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("registering the application id: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue("DisplayName", appID); err != nil {
		return fmt.Errorf("registering the application id: %w", err)
	}
	return nil
}

// Show hands the toast XML to the notification platform under the given
// application id. The XML is the toast schema's <toast> document; the caller
// owns escaping its text.
func Show(appID, xml string) error {
	if err := initialize(); err != nil {
		return err
	}

	// Parse the XML into a WinRT XmlDocument.
	doc, err := activateInstance("Windows.Data.Xml.Dom.XmlDocument")
	if err != nil {
		return err
	}
	defer doc.release()
	docIO, err := doc.query(&iidXmlDocumentIO, "querying IXmlDocumentIO")
	if err != nil {
		return err
	}
	defer docIO.release()
	xmlH, err := newHString(xml)
	if err != nil {
		return err
	}
	defer xmlH.delete()
	if err := call("IXmlDocumentIO.LoadXml",
		(*xmlDocumentIOVtbl)(unsafe.Pointer(docIO.vtbl)).LoadXml,
		uintptr(unsafe.Pointer(docIO)), uintptr(xmlH)); err != nil {
		return err
	}

	// A notifier for the application id, from the per-user manager.
	statics, err := activationFactory(
		"Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics5)
	if err != nil {
		return err
	}
	defer statics.release()
	var manager *comObject
	if err := call("IToastNotificationManagerStatics5.GetDefault",
		(*managerStatics5Vtbl)(unsafe.Pointer(statics.vtbl)).GetDefault,
		uintptr(unsafe.Pointer(statics)), uintptr(unsafe.Pointer(&manager))); err != nil {
		return err
	}
	defer manager.release()
	managerItf, err := manager.query(&iidToastManagerForUser, "querying IToastNotificationManagerForUser")
	if err != nil {
		return err
	}
	defer managerItf.release()
	appIDH, err := newHString(appID)
	if err != nil {
		return err
	}
	defer appIDH.delete()
	var notifier *comObject
	if err := call("IToastNotificationManagerForUser.CreateToastNotifierWithId",
		(*managerForUserVtbl)(unsafe.Pointer(managerItf.vtbl)).CreateToastNotifierWithId,
		uintptr(unsafe.Pointer(managerItf)), uintptr(appIDH),
		uintptr(unsafe.Pointer(&notifier))); err != nil {
		return err
	}
	defer notifier.release()

	// The toast itself, from the document.
	factory, err := activationFactory(
		"Windows.UI.Notifications.ToastNotification", &iidToastFactory)
	if err != nil {
		return err
	}
	defer factory.release()
	var toast *comObject
	if err := call("IToastNotificationFactory.CreateToastNotification",
		(*toastFactoryVtbl)(unsafe.Pointer(factory.vtbl)).CreateToastNotification,
		uintptr(unsafe.Pointer(factory)), uintptr(unsafe.Pointer(doc)),
		uintptr(unsafe.Pointer(&toast))); err != nil {
		return err
	}
	defer toast.release()

	// Show it.
	notifierItf, err := notifier.query(&iidToastNotifier, "querying IToastNotifier")
	if err != nil {
		return err
	}
	defer notifierItf.release()
	return call("IToastNotifier.Show",
		(*notifierVtbl)(unsafe.Pointer(notifierItf.vtbl)).Show,
		uintptr(unsafe.Pointer(notifierItf)), uintptr(unsafe.Pointer(toast)))
}
