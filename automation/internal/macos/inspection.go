//go:build darwin

package macos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/grmrgecko/karakuri/automation/autoapi"
)

type Inspection struct{}

var inspectOnce sync.Once
var inspectErr error
var axValueCreate func(int32, unsafe.Pointer) uintptr
var kCFBooleanFalseRef uintptr
var axValueGetValue func(uintptr, int32, unsafe.Pointer) bool
var axCopyActions func(uintptr, *uintptr) int32
var axSetTimeout func(uintptr, float32) int32
var cfGetTypeID func(uintptr) uintptr
var cfStringGetTypeID func() uintptr
var inspectionBooleanValue func(uintptr) bool
var cgDisplayCopyMode func(uint32) uintptr
var cgDisplayCopyModes func(uint32, uintptr) uintptr
var cgModeWidth, cgModeHeight func(uintptr) uint
var cgModeRefresh func(uintptr) float64
var cgModeEncoding func(uintptr) uintptr
var cgSetMode func(uint32, uintptr, uintptr) int32
var cgMainDisplay func() uint32
var inspectionWindowBounds uintptr
var cgRectFromDictionary func(uintptr, unsafe.Pointer) bool
var procPIDPath func(int32, unsafe.Pointer, uint32) int32

type cgRect struct{ Origin, Size cgPoint }

var cgDisplayBounds func(uint32) cgRect
var cgWindowLevelForKey func(int32) int32

func loadInspection() error {
	if err := loadCapture(); err != nil {
		return err
	}
	inspectOnce.Do(func() {
		app, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inspectErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inspectErr = err
			return
		}
		purego.RegisterLibFunc(&axValueCreate, app, "AXValueCreate")
		kCFBooleanFalseRef = derefStringSym(cf, "kCFBooleanFalse")
		purego.RegisterLibFunc(&axValueGetValue, app, "AXValueGetValue")
		purego.RegisterLibFunc(&axCopyActions, app, "AXUIElementCopyActionNames")
		purego.RegisterLibFunc(&axSetTimeout, app, "AXUIElementSetMessagingTimeout")
		purego.RegisterLibFunc(&cfGetTypeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&cfStringGetTypeID, cf, "CFStringGetTypeID")
		purego.RegisterLibFunc(&inspectionBooleanValue, cf, "CFBooleanGetValue")
		purego.RegisterLibFunc(&cgDisplayCopyMode, app, "CGDisplayCopyDisplayMode")
		purego.RegisterLibFunc(&cgDisplayCopyModes, app, "CGDisplayCopyAllDisplayModes")
		purego.RegisterLibFunc(&cgModeWidth, app, "CGDisplayModeGetWidth")
		purego.RegisterLibFunc(&cgModeHeight, app, "CGDisplayModeGetHeight")
		purego.RegisterLibFunc(&cgModeRefresh, app, "CGDisplayModeGetRefreshRate")
		purego.RegisterLibFunc(&cgModeEncoding, app, "CGDisplayModeCopyPixelEncoding")
		purego.RegisterLibFunc(&cgSetMode, app, "CGDisplaySetDisplayMode")
		purego.RegisterLibFunc(&cgMainDisplay, app, "CGMainDisplayID")
		purego.RegisterLibFunc(&cgRectFromDictionary, app, "CGRectMakeWithDictionaryRepresentation")
		inspectionWindowBounds = derefStringSym(app, "kCGWindowBounds")
		purego.RegisterLibFunc(&cgWindowLevelForKey, app, "CGWindowLevelForKey")
		lib, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inspectErr = err
			return
		}
		purego.RegisterLibFunc(&procPIDPath, lib, "proc_pidpath")
	})
	return inspectErr
}
func axText(ref uintptr, key string) string {
	value, err := axCopyValue(ref, key)
	if err != nil || value == 0 {
		return ""
	}
	defer cfRelease(value)
	if cfGetTypeID(value) != cfStringGetTypeID() {
		return ""
	}
	return cfStringGo(value)
}
func axBool(ref uintptr, key string, fallback bool) bool {
	value, err := axCopyValue(ref, key)
	if err != nil || value == 0 {
		return fallback
	}
	defer cfRelease(value)
	return inspectionBooleanValue(value)
}
func axRect(ref uintptr) (x, y, width, height int) {
	for _, attr := range []struct {
		Key  string
		Kind int32
	}{{"AXPosition", 1}, {"AXSize", 2}} {
		value, err := axCopyValue(ref, attr.Key)
		if err != nil || value == 0 {
			continue
		}
		var point cgPoint
		ok := axValueGetValue(value, attr.Kind, unsafe.Pointer(&point))
		cfRelease(value)
		if ok {
			if attr.Kind == 1 {
				x = int(point.X)
				y = int(point.Y)
			} else {
				width = int(point.X)
				height = int(point.Y)
			}
		}
	}
	return
}
func withAXWindow(w autoapi.WindowInfo, fn func(uintptr, uintptr) error) error {
	if err := loadInspection(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	pid, index, err := windowRef(w.ID)
	if err != nil {
		return err
	}
	app := axUIElementCreateApplication(int32(pid))
	if app == 0 {
		return fmt.Errorf("application accessibility root unavailable")
	}
	defer cfRelease(app)
	_ = axSetTimeout(app, 2)
	wins, err := axCopyValue(app, "AXWindows")
	if err != nil {
		return err
	}
	defer cfRelease(wins)
	if index < 0 || index >= cfArrayGetCount(wins) {
		return fmt.Errorf("window no longer exists")
	}
	ref := cfArrayGetValueAtIndex(wins, index)
	if w.Title != "" && axText(ref, "AXTitle") != w.Title {
		return fmt.Errorf("window order changed; list windows again")
	}
	return fn(app, ref)
}
func (Inspection) Elements(ctx context.Context, w autoapi.WindowInfo) ([]autoapi.Element, error) {
	out := make([]autoapi.Element, 0)
	err := withAXWindow(w, func(app, window uintptr) error {
		var walk func(uintptr, string, string, string, int) error
		walk = func(ref uintptr, id, parent, menu string, depth int) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(out) >= 4096 || depth > 64 {
				return fmt.Errorf("accessibility tree exceeds 4096 elements or 64 levels")
			}
			e := autoapi.Element{ID: id, Parent: parent, Role: axText(ref, "AXRole"), Class: axText(ref, "AXSubrole"), Name: axText(ref, "AXTitle"), Enabled: axBool(ref, "AXEnabled", true), Visible: !axBool(ref, "AXHidden", false)}
			if e.Name == "" {
				e.Name = axText(ref, "AXDescription")
			}
			e.Protected = e.Class == "AXSecureTextField"
			if !e.Protected {
				e.Text = axText(ref, "AXValue")
			}
			e.X, e.Y, e.Width, e.Height = axRect(ref)
			if e.Role == "AXMenuBarItem" || e.Role == "AXMenuItem" {
				if menu != "" {
					menu += " > "
				}
				menu += e.Name
				e.Path = menu
			}
			var actions uintptr
			if axCopyActions(ref, &actions) == 0 && actions != 0 {
				for i := 0; i < cfArrayGetCount(actions); i++ {
					e.Actions = append(e.Actions, cfStringGo(cfArrayGetValueAtIndex(actions, i)))
				}
				cfRelease(actions)
			}
			out = append(out, e)
			children, err := axCopyValue(ref, "AXChildren")
			if err != nil || children == 0 {
				return nil
			}
			defer cfRelease(children)
			for i := 0; i < cfArrayGetCount(children); i++ {
				if err := walk(cfArrayGetValueAtIndex(children, i), id+"/"+strconv.Itoa(i), id, menu, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(window, "window", "", "", 0); err != nil {
			return err
		}
		menu, err := axCopyValue(app, "AXMenuBar")
		if err == nil && menu != 0 {
			defer cfRelease(menu)
			return walk(menu, "menu", "", "", 0)
		}
		return nil
	})
	return out, err
}
func (Inspection) Invoke(ctx context.Context, w autoapi.WindowInfo, id string) error {
	return withAXWindow(w, func(app, window uintptr) error {
		parts := strings.Split(id, "/")
		ref := window
		if parts[0] == "menu" {
			var err error
			ref, err = axCopyValue(app, "AXMenuBar")
			if err != nil {
				return err
			}
			defer cfRelease(ref)
		} else if parts[0] != "window" {
			return fmt.Errorf("invalid accessibility element id")
		}
		for _, part := range parts[1:] {
			if err := ctx.Err(); err != nil {
				return err
			}
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 {
				return fmt.Errorf("invalid accessibility child index")
			}
			children, err := axCopyValue(ref, "AXChildren")
			if err != nil {
				return err
			}
			defer cfRelease(children)
			if i >= cfArrayGetCount(children) {
				return fmt.Errorf("control no longer exists")
			}
			ref = cfArrayGetValueAtIndex(children, i)
		}
		if !axBool(ref, "AXEnabled", true) {
			return fmt.Errorf("control is disabled")
		}
		action := cfStr("AXPress")
		defer cfRelease(action)
		if rc := axUIElementPerformAction(ref, action); rc != 0 {
			return fmt.Errorf("accessibility press rejected (error %d)", rc)
		}
		return nil
	})
}
func displayMode(id uint32, mode uintptr) autoapi.DisplayInfo {
	encoding := cgModeEncoding(mode)
	defer cfRelease(encoding)
	text := cfStringGo(encoding)
	depth := 0
	if strings.Contains(text, "R") && strings.Contains(text, "G") && strings.Contains(text, "B") {
		depth = len(text)
	}
	for _, n := range []int{16, 30, 32, 64} {
		if strings.Contains(text, strconv.Itoa(n)) {
			depth = n
			break
		}
	}
	bounds := cgDisplayBounds(id)
	return autoapi.DisplayInfo{X: int(bounds.Origin.X), Y: int(bounds.Origin.Y), ID: strconv.FormatUint(uint64(id), 10), Width: int(cgModeWidth(mode)), Height: int(cgModeHeight(mode)), Depth: depth, Refresh: cgModeRefresh(mode), Primary: id == cgMainDisplay()}
}
func (Inspection) Displays(ctx context.Context) ([]autoapi.DisplayInfo, error) {
	if err := loadInspection(); err != nil {
		return nil, err
	}
	ids, err := activeDisplays()
	if err != nil {
		return nil, err
	}
	out := make([]autoapi.DisplayInfo, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mode := cgDisplayCopyMode(id)
		if mode == 0 {
			return nil, fmt.Errorf("display %d has no mode", id)
		}
		out = append(out, displayMode(id, mode))
		cfRelease(mode)
	}
	return out, nil
}
func (Inspection) SetDisplay(ctx context.Context, m autoapi.DisplayMode) error {
	if m.Width <= 0 || m.Height <= 0 || m.Depth <= 0 {
		return fmt.Errorf("display dimensions and depth must be positive")
	}
	if err := loadInspection(); err != nil {
		return err
	}
	id := cgMainDisplay()
	modes := cgDisplayCopyModes(id, 0)
	if modes == 0 {
		return fmt.Errorf("display modes unavailable")
	}
	defer cfRelease(modes)
	for i := 0; i < cfArrayGetCount(modes); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode := cfArrayGetValueAtIndex(modes, i)
		v := displayMode(id, mode)
		if v.Width == m.Width && v.Height == m.Height && v.Depth == m.Depth {
			if rc := cgSetMode(id, mode, 0); rc != 0 {
				return fmt.Errorf("display mode rejected (error %d)", rc)
			}
			return nil
		}
	}
	return fmt.Errorf("display does not advertise %dx%dx%d", m.Width, m.Height, m.Depth)
}

func windowCaptureID(pid int, title string, x, y, width, height int) string {
	array := cgWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID)
	if array == 0 {
		return ""
	}
	defer cfRelease(array)
	key := inspectionWindowBounds
	matches := []string{}
	for i := 0; i < cfArrayGetCount(array); i++ {
		dict := cfArrayGetValueAtIndex(array, i)
		if cfNumberInt(cfDictionaryGetValue(dict, keyWindowOwnerPID)) != pid {
			continue
		}
		var r cgRect
		if !cgRectFromDictionary(cfDictionaryGetValue(dict, key), unsafe.Pointer(&r)) {
			continue
		}
		if int(r.Origin.X) == x && int(r.Origin.Y) == y && int(r.Size.X) == width && int(r.Size.Y) == height {
			matches = append(matches, strconv.Itoa(cfNumberInt(cfDictionaryGetValue(dict, keyWindowNumber))))
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}
func processImage(pid int) string {
	buf := make([]byte, 4096)
	n := procPIDPath(int32(pid), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}
