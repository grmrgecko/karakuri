//go:build darwin

package macos

import (
	"context"
	"fmt"
	"unsafe"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Place submits the requested bounds to the native window manager.
func (window) Place(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return withAXWindow(w, func(_ uintptr, ref uintptr) error {
		if p.Restore {
			for _, attr := range []string{"AXFullScreen", "AXZoomed"} {
				if axBool(ref, attr, false) {
					if err := axWrite(ref, attr, kCFBooleanFalseRef); err != nil {
						return err
					}
				}
			}
			if err := axWrite(ref, "AXMinimized", kCFBooleanFalseRef); err != nil {
				return err
			}
		}
		if p.Move {
			point := cgPoint{X: float64(p.X), Y: float64(p.Y)}
			value := axValueCreate(1, unsafe.Pointer(&point))
			if value == 0 {
				return fmt.Errorf("cannot create window position")
			}
			err := axWrite(ref, "AXPosition", value)
			cfRelease(value)
			if err != nil {
				return err
			}
		}
		if p.Resize {
			size := cgPoint{X: float64(p.Width), Y: float64(p.Height)}
			value := axValueCreate(2, unsafe.Pointer(&size))
			if value == 0 {
				return fmt.Errorf("cannot create window size")
			}
			err := axWrite(ref, "AXSize", value)
			cfRelease(value)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
func axWrite(ref uintptr, attr string, value uintptr) error {
	key := cfStr(attr)
	defer cfRelease(key)
	if rc := axUIElementSetAttributeValue(ref, key, value); rc != kAXErrorSuccess {
		return fmt.Errorf("cannot set %s (accessibility error %d)", attr, rc)
	}
	return nil
}
