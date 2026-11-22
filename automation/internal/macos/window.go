//go:build darwin

package macos

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// The window list is built from two native sources: CoreGraphics' CGWindowList
// gives the on-screen applications in front-to-back order and their names, and
// the Accessibility API gives each window's title and is what an action drives.
// A window's title is read from AXTitle rather than CGWindowList's own name,
// which is empty without a screen-recording grant and unreliable even with one.
// A window is identified by its process and its index in that process's
// accessibility window list, which is the same handle an action uses.

// kAXErrorSuccess is the AXError value for a call that worked.
const kAXErrorSuccess = 0

// listWindows reports the on-screen windows, one process at a time in the order
// CoreGraphics stacks them, including floating, modal, and utility panels.
func listWindows(ctx context.Context) ([]autoapi.WindowInfo, error) {
	if err := loadInspection(); err != nil {
		return nil, err
	}
	if err := ensureTrusted(); err != nil {
		return nil, err
	}
	pids, names, err := onScreenApps()
	if err != nil {
		return nil, err
	}

	var windows []autoapi.WindowInfo
	for order, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		app := axUIElementCreateApplication(int32(pid))
		if app == 0 {
			continue
		}
		_ = axSetTimeout(app, 2)
		axWindows, err := axCopyValue(app, "AXWindows")
		if err != nil || axWindows == 0 {
			cfRelease(app)
			continue
		}
		count := cfArrayGetCount(axWindows)
		for i := 0; i < count; i++ {
			win := cfArrayGetValueAtIndex(axWindows, i)
			if win == 0 {
				continue
			}
			title := ""
			if got, err := axCopyValue(win, "AXTitle"); err == nil && got != 0 {
				title = cfStringGo(got)
				cfRelease(got)
			}
			x, y, width, height := axRect(win)
			enabled := axBool(win, "AXEnabled", true)
			windows = append(windows, autoapi.WindowInfo{
				X: x, Y: y, Width: width, Height: height, Image: processImage(pid), Enabled: &enabled, CaptureID: windowCaptureID(pid, title, x, y, width, height),
				ID:     strconv.Itoa(pid) + ":" + strconv.Itoa(i),
				Class:  names[pid],
				Title:  title,
				PID:    pid,
				Active: order == 0 && i == 0, // frontmost app's frontmost window
			})
		}
		cfRelease(axWindows)
		cfRelease(app)
	}
	return windows, nil
}

// onScreenApps reads the processes that own an application window or panel, in
// front-to-back order and without repeats, along with each one's name.
func onScreenApps() ([]int, map[int]string, error) {
	array := cgWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID)
	if array == 0 {
		return nil, nil, fmt.Errorf("macOS returned no window list")
	}
	defer cfRelease(array)

	var order []int
	names := map[int]string{}
	seen := map[int]bool{}
	count := cfArrayGetCount(array)
	for i := 0; i < count; i++ {
		dict := cfArrayGetValueAtIndex(array, i)
		if dict == 0 {
			continue
		}
		// CGWindowLevelKey values are stable; their window levels are queried.
		// Standard Additions puts AppleScript dialogs on the modal panel level.
		layer := int32(cfNumberInt(cfDictionaryGetValue(dict, keyWindowLayer)))
		if layer != cgWindowLevelForKey(4) && layer != cgWindowLevelForKey(5) &&
			layer != cgWindowLevelForKey(10) && layer != cgWindowLevelForKey(17) {
			continue
		}
		pid := cfNumberInt(cfDictionaryGetValue(dict, keyWindowOwnerPID))
		if pid == 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		order = append(order, pid)
		names[pid] = cfStringGo(cfDictionaryGetValue(dict, keyWindowOwnerName))
	}
	return order, names, nil
}

// windowAction acts on one window through the Accessibility interface, located
// by the process and window index its id names.
func windowAction(ctx context.Context, target autoapi.WindowInfo, action string) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	pid, index, err := windowRef(target.ID)
	if err != nil {
		return err
	}

	app := axUIElementCreateApplication(int32(pid))
	if app == 0 {
		return fmt.Errorf("cannot reach process %d through the accessibility interface", pid)
	}
	defer cfRelease(app)

	axWindows, err := axCopyValue(app, "AXWindows")
	if err != nil || axWindows == 0 {
		return fmt.Errorf("cannot read the application's windows; is accessibility granted? %w", err)
	}
	defer cfRelease(axWindows)
	if index < 0 || index >= cfArrayGetCount(axWindows) {
		return fmt.Errorf("process %d has no window at index %d", pid, index)
	}
	window := cfArrayGetValueAtIndex(axWindows, index)
	if window == 0 {
		return fmt.Errorf("process %d has no window at index %d", pid, index)
	}

	switch action {
	case windowActivate:
		// Raising the window and bringing the process forward are separate: one
		// without the other leaves the window behind another application.
		axPerform(window, "AXRaise")
		return axSetTrue(app, "AXFrontmost")
	case windowMinimize:
		return axSetTrue(window, "AXMinimized")
	case windowMaximize:
		return axPressButton(window, "AXZoomButton")
	case windowClose:
		return axPressButton(window, "AXCloseButton")
	default:
		return fmt.Errorf("unknown window action %q", action)
	}
}

// windowRef reads the process and window index back out of a window id.
func windowRef(id string) (int, int, error) {
	pidText, indexText, ok := strings.Cut(strings.TrimSpace(id), ":")
	if !ok {
		return 0, 0, fmt.Errorf("%q is not a macOS window id; they are written pid:index", id)
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a macOS window id; they are written pid:index", id)
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a macOS window id; they are written pid:index", id)
	}
	return pid, index, nil
}

// axCopyValue reads one accessibility attribute as a CFTypeRef the caller owns.
func axCopyValue(element uintptr, attr string) (uintptr, error) {
	key := cfStr(attr)
	defer cfRelease(key)
	var out uintptr
	if rc := axUIElementCopyAttributeValue(element, key, &out); rc != kAXErrorSuccess {
		return 0, fmt.Errorf("accessibility attribute %s is unavailable (error %d)", attr, rc)
	}
	return out, nil
}

// axSetTrue sets a boolean accessibility attribute to true.
func axSetTrue(element uintptr, attr string) error {
	key := cfStr(attr)
	defer cfRelease(key)
	if rc := axUIElementSetAttributeValue(element, key, kCFBooleanTrueRef); rc != kAXErrorSuccess {
		return fmt.Errorf("cannot set %s (error %d)", attr, rc)
	}
	return nil
}

// axPerform performs a named accessibility action, ignoring an unsupported one.
func axPerform(element uintptr, action string) {
	key := cfStr(action)
	defer cfRelease(key)
	_ = axUIElementPerformAction(element, key)
}

// axPressButton finds one of a window's title-bar buttons and presses it.
func axPressButton(window uintptr, buttonAttr string) error {
	button, err := axCopyValue(window, buttonAttr)
	if err != nil {
		return fmt.Errorf("this window has no %s", strings.TrimPrefix(buttonAttr, "AX"))
	}
	defer cfRelease(button)
	key := cfStr("AXPress")
	defer cfRelease(key)
	if rc := axUIElementPerformAction(button, key); rc != kAXErrorSuccess {
		return fmt.Errorf("pressing the window button failed (error %d)", rc)
	}
	return nil
}
