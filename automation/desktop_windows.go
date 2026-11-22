//go:build windows

package automation

import (
	"context"
	"fmt"
	"image"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var autoUser = windows.NewLazySystemDLL("user32.dll")
var sendMessageTimeout = autoUser.NewProc("SendMessageTimeoutW")
var getControlID = autoUser.NewProc("GetDlgCtrlID")
var getMenu = autoUser.NewProc("GetMenu")
var printWindowProc = autoUser.NewProc("PrintWindow")

func windowHandle(id string) (win.HWND, error) {
	v, err := strconv.ParseUint(strings.TrimPrefix(id, "0x"), 16, 64)
	return win.HWND(v), err
}

func timedMessage(hwnd win.HWND, msg uint32, wparam, lparam uintptr) (uintptr, error) {
	var result uintptr
	ok, _, err := sendMessageTimeout.Call(uintptr(hwnd), uintptr(msg), wparam, lparam, 3, 300, uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		return 0, fmt.Errorf("window message 0x%x timed out or failed: %v", msg, err)
	}
	return result, nil
}

func listControls(ctx context.Context, parent win.HWND) ([]Control, error) {
	controls := make([]Control, 0)
	var failure error
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if ctx.Err() != nil {
			failure = ctx.Err()
			return 0
		}
		if len(controls) >= 4096 {
			failure = fmt.Errorf("control limit of 4096 reached")
			return 0
		}
		h := win.HWND(hwnd)
		c := Control{ID: fmt.Sprintf("0x%x", hwnd), Parent: fmt.Sprintf("0x%x", win.GetParent(h)), Enabled: win.IsWindowEnabled(h), Visible: win.IsWindowVisible(h)}
		class := make([]uint16, 256)
		if n, err := win.GetClassName(h, &class[0], len(class)); err == nil {
			c.Class = windows.UTF16ToString(class[:n])
		}
		id, _, _ := getControlID.Call(hwnd)
		c.ControlID = int32(id)
		var rect win.RECT
		if win.GetWindowRect(h, &rect) {
			c.X, c.Y, c.Width, c.Height = int(rect.Left), int(rect.Top), int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
		}
		c.Protected = (strings.EqualFold(c.Class, "Edit") || strings.Contains(strings.ToUpper(c.Class), ".EDIT.")) && win.GetWindowLong(h, win.GWL_STYLE)&0x20 != 0
		if !c.Protected {
			length, err := timedMessage(h, win.WM_GETTEXTLENGTH, 0, 0)
			if err == nil {
				if length > 32767 {
					length = 32767
				}
				text := make([]uint16, int(length)+1)
				_, err = timedMessage(h, win.WM_GETTEXT, uintptr(len(text)), uintptr(unsafe.Pointer(&text[0])))
				if err == nil {
					c.Text = windows.UTF16ToString(text)
				}
			}
			if err != nil {
				c.Error = err.Error()
			}
		}
		controls = append(controls, c)
		return 1
	})
	win.EnumChildWindows(parent, callback, 0)
	return controls, failure
}

func labelText(text string) string {
	text = strings.SplitN(text, "\t", 2)[0]
	text = strings.ReplaceAll(text, "&&", "\x00")
	text = strings.ReplaceAll(text, "&", "")
	return strings.TrimSpace(strings.ReplaceAll(text, "\x00", "&"))
}

func nativeClickControl(ctx context.Context, sess *Session, w WindowInfo, target ControlTarget) error {
	text := target.Text
	if text == "" && target.ID == "" {
		return fmt.Errorf("control label cannot be empty")
	}
	parent, err := windowHandle(w.ID)
	if err != nil {
		return err
	}
	controls, err := listControls(ctx, parent)
	if err != nil {
		return err
	}
	var matches []Control
	for _, exact := range controlMatchPasses(target) {
		for _, c := range controls {
			if !c.Enabled || !c.Visible || (target.Role != "" && !strings.EqualFold(target.Role, c.Class)) || (target.Parent != "" && target.Parent != c.Parent) {
				continue
			}
			if target.ID != "" {
				if strings.EqualFold(c.ID, target.ID) {
					matches = append(matches, c)
				}
			} else if controlLabelMatches(c.Name, labelText(c.Text), c.Protected, target, exact) {
				matches = append(matches, c)
			}
		}
		if len(matches) > 0 {
			break
		}
	}
	if len(matches) != 1 {
		return fmt.Errorf("%d enabled visible controls match %q; choose a unique label", len(matches), text)
	}
	c := matches[0]
	h, _ := windowHandle(c.ID)
	dialogCode, _ := timedMessage(h, 0x0087, 0, 0)
	if dialogCode&0x2000 != 0 {
		// BM_CLICK targets standard buttons even when another window is in front.
		if win.PostMessage(h, 0x00f5, 0, 0) == 0 {
			return fmt.Errorf("posting button click failed")
		}
	} else {
		if c.Width <= 0 || c.Height <= 0 {
			return fmt.Errorf("control has no usable rectangle")
		}
		x, y := c.X+c.Width/2, c.Y+c.Height/2
		if win.WindowFromPoint(win.POINT{X: int32(x), Y: int32(y)}) != h {
			return fmt.Errorf("control is obscured; bring its window forward before clicking")
		}
		if err = sess.Mouse.Move(ctx, x, y, false); err != nil {
			return err
		}
		if err = sess.Mouse.Click(ctx, autoapi.ButtonLeft, 1); err != nil {
			return err
		}
	}
	return nil
}

func listMenu(hwnd win.HWND) ([]MenuItem, error) {
	handle, _, _ := getMenu.Call(uintptr(hwnd))
	if handle == 0 {
		return []MenuItem{}, nil
	}
	remaining := 4096
	var walk func(win.HMENU, string, int, bool) ([]MenuItem, error)
	walk = func(menu win.HMENU, parent string, depth int, enabled bool) ([]MenuItem, error) {
		if depth > 16 {
			return nil, fmt.Errorf("menu depth exceeds 16")
		}
		out := make([]MenuItem, 0)
		count := win.GetMenuItemCount(menu)
		if count < 0 {
			return nil, fmt.Errorf("reading menu item count failed")
		}
		for i := int32(0); i < count; i++ {
			remaining--
			if remaining < 0 {
				return nil, fmt.Errorf("menu item limit exceeded")
			}
			text := make([]uint16, 32768)
			info := win.MENUITEMINFO{FMask: win.MIIM_STRING | win.MIIM_ID | win.MIIM_STATE | win.MIIM_SUBMENU | win.MIIM_FTYPE, DwTypeData: &text[0], Cch: uint32(len(text))}
			info.CbSize = uint32(unsafe.Sizeof(info))
			if !win.GetMenuItemInfo(menu, uint32(i), win.TRUE, &info) {
				return nil, fmt.Errorf("reading menu item %d failed", i)
			}
			if info.FType&win.MFT_SEPARATOR != 0 {
				continue
			}
			checked := info.FState&win.MFS_CHECKED != 0
			item := MenuItem{Text: windows.UTF16ToString(text), ID: strconv.FormatUint(uint64(info.WID), 10), Enabled: enabled && info.FState&win.MFS_DISABLED == 0, Checked: &checked}
			item.Path = labelText(item.Text)
			if parent != "" {
				item.Path = parent + " > " + item.Path
			}
			if info.HSubMenu != 0 {
				var err error
				item.Children, err = walk(info.HSubMenu, item.Path, depth+1, item.Enabled)
				if err != nil {
					return nil, err
				}
			}
			out = append(out, item)
		}
		return out, nil
	}
	return walk(win.HMENU(handle), "", 0, true)
}

func invokeMenu(hwnd win.HWND, items []MenuItem, target string) error {
	id, idErr := strconv.ParseUint(target, 0, 32)
	parts := strings.Split(target, ">")
	for i := range parts {
		parts[i] = labelText(parts[i])
	}
	target = strings.Join(parts, " > ")
	var found []MenuItem
	var visit func([]MenuItem)
	visit = func(items []MenuItem) {
		for _, item := range items {
			if len(item.Children) == 0 && (idErr == nil && item.ID == strconv.FormatUint(id, 10) || strings.EqualFold(item.Path, target)) {
				found = append(found, item)
			}
			visit(item.Children)
		}
	}
	visit(items)
	if len(found) != 1 {
		return fmt.Errorf("%d menu items match %q", len(found), target)
	}
	item := found[0]
	if !item.Enabled {
		return fmt.Errorf("menu item %q is disabled", item.Path)
	}
	commandID, err := strconv.ParseUint(item.ID, 10, 16)
	if err != nil {
		return fmt.Errorf("menu item has no invokable command ID")
	}
	if win.PostMessage(hwnd, win.WM_COMMAND, uintptr(commandID), 0) == 0 {
		return fmt.Errorf("posting menu command failed")
	}
	return nil
}

func captureSelectedWindow(ctx context.Context, w WindowInfo) (image.Image, error) {
	type result struct {
		image image.Image
		err   error
	}
	reply := make(chan result, 1)
	go func() { img, err := printWindowImage(w); reply <- result{img, err} }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case r := <-reply:
		return r.image, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("PrintWindow timed out")
	}
}

func printWindowImage(w WindowInfo) (image.Image, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if w.Width <= 0 || w.Height <= 0 || int64(w.Width)*int64(w.Height) > 16*1024*1024 {
		return nil, fmt.Errorf("window dimensions exceed capture limit")
	}
	hwnd, err := windowHandle(w.ID)
	if err != nil {
		return nil, err
	}
	dc := win.GetDC(0)
	if dc == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer win.ReleaseDC(0, dc)
	memory := win.CreateCompatibleDC(dc)
	if memory == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer win.DeleteDC(memory)
	header := win.BITMAPINFOHEADER{BiWidth: int32(w.Width), BiHeight: -int32(w.Height), BiPlanes: 1, BiBitCount: 32, BiCompression: win.BI_RGB}
	header.BiSize = uint32(unsafe.Sizeof(header))
	var bits unsafe.Pointer
	bitmap := win.CreateDIBSection(dc, &header, win.DIB_RGB_COLORS, &bits, 0, 0)
	if bitmap == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection failed")
	}
	defer win.DeleteObject(win.HGDIOBJ(bitmap))
	old := win.SelectObject(memory, win.HGDIOBJ(bitmap))
	if old == 0 {
		return nil, fmt.Errorf("SelectObject failed")
	}
	defer win.SelectObject(memory, old)
	flags := uintptr(0)
	version := windows.RtlGetVersion()
	if version.MajorVersion > 6 || version.MajorVersion == 6 && version.MinorVersion >= 3 {
		flags = 2
	}
	ok, _, _ := printWindowProc.Call(uintptr(hwnd), uintptr(memory), flags)
	if ok == 0 && flags != 0 {
		ok, _, _ = printWindowProc.Call(uintptr(hwnd), uintptr(memory), 0)
	}
	if ok == 0 {
		return nil, fmt.Errorf("PrintWindow failed")
	}
	win.GdiFlush()
	source := unsafe.Slice((*byte)(bits), w.Width*w.Height*4)
	img := image.NewRGBA(image.Rect(0, 0, w.Width, w.Height))
	for i := 0; i < len(source); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = source[i+2], source[i+1], source[i], 255
	}
	return img, nil
}

func desktopControls(ctx context.Context, s *Session, w WindowInfo) ([]Control, error) {
	h, err := windowHandle(w.ID)
	if err != nil {
		return nil, err
	}
	return listControls(ctx, h)
}
func desktopMenus(ctx context.Context, s *Session, w WindowInfo) ([]MenuItem, error) {
	h, err := windowHandle(w.ID)
	if err != nil {
		return nil, err
	}
	return listMenu(h)
}
func desktopInvokeMenu(ctx context.Context, s *Session, w WindowInfo, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h, err := windowHandle(w.ID)
	if err != nil {
		return err
	}
	items, err := listMenu(h)
	if err != nil {
		return err
	}
	return invokeMenu(h, items, target)
}
func desktopClickControl(ctx context.Context, s *Session, w WindowInfo, target ControlTarget) error {
	if target.ID != "" && !strings.HasPrefix(target.ID, "0x") {
		return invokeElement(ctx, s, w, target)
	}
	return nativeClickControl(ctx, s, w, target)
}
func isNativeDialog(w WindowInfo) bool { return w.Class == "#32770" }
