//go:build !windows && !darwin

package automation

import (
	"context"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"

	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xgraphics"
)

func captureSelectedWindow(ctx context.Context, w WindowInfo) (image.Image, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return nil, fmt.Errorf("compositor does not expose an occluded-window capture API")
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(w.ID, "0x"), 16, 32)
	if err != nil {
		return nil, err
	}
	xu, err := xgbutil.NewConn()
	if err != nil {
		return nil, err
	}
	defer xu.Conn().Close()
	if err = composite.Init(xu.Conn()); err != nil {
		return nil, err
	}
	pixmap, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, err
	}
	if err = composite.NameWindowPixmapChecked(xu.Conn(), xproto.Window(id), pixmap).Check(); err != nil {
		return nil, err
	}
	defer xproto.FreePixmap(xu.Conn(), pixmap)
	return xgraphics.NewDrawable(xu, xproto.Drawable(pixmap))
}
