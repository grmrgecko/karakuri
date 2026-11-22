//go:build !windows && !darwin

package linuxdesk

import (
	"context"
	"fmt"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil/ewmh"
)

// Place submits the requested bounds to the native window manager.
func (window) Place(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	if waylandSession() || !x11Present() {
		return fmt.Errorf("window placement: %w", autoapi.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	win, err := x11ParseWindowID(w.ID)
	if err != nil {
		return err
	}
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	xu := session.xu
	if p.Restore {
		if err = ewmh.WmStateReqExtra(xu, win, ewmh.StateRemove, "_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ", 2); err != nil {
			return err
		}
		if err = ewmh.WmStateReqExtra(xu, win, ewmh.StateRemove, "_NET_WM_STATE_FULLSCREEN", "", 2); err != nil {
			return err
		}
		if err = xproto.MapWindowChecked(xu.Conn(), win).Check(); err != nil {
			return err
		}
	}
	if !p.Move && !p.Resize {
		return nil
	}
	width, height := 0, 0
	if p.Resize {
		width, height = p.Width, p.Height
	}
	return ewmh.MoveresizeWindowExtra(xu, win, p.X, p.Y, width, height, xproto.GravityStatic, 2, p.Move, p.Move)
}
