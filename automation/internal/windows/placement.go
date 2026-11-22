//go:build windows

package windows

import (
	"context"
	"fmt"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/lxn/win"
)

// Place submits the requested bounds to the native window manager.
func (window) Place(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hwnd, err := windowsHandle(w.ID)
	if err != nil {
		return err
	}
	if valid, _, _ := user32.NewProc("IsWindow").Call(uintptr(hwnd)); valid == 0 {
		return fmt.Errorf("window no longer exists")
	}
	if p.Restore {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	if !p.Move && !p.Resize {
		return nil
	}
	flags := uint32(win.SWP_NOZORDER | win.SWP_NOACTIVATE)
	if !p.Move {
		flags |= win.SWP_NOMOVE
	}
	if !p.Resize {
		flags |= win.SWP_NOSIZE
	}
	if !win.SetWindowPos(hwnd, 0, int32(p.X), int32(p.Y), int32(p.Width), int32(p.Height), flags) {
		return fmt.Errorf("windows refused window placement")
	}
	return nil
}
