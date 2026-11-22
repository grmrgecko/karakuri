//go:build !windows && !darwin

package automation

import (
	"context"
	"github.com/grmrgecko/karakuri/automation/internal/inspectunix"
	"os"
	"strings"
)

type inspectionWindows struct{ Window }

func (w inspectionWindows) List(ctx context.Context) ([]WindowInfo, error) {
	native, err := w.Window.List(ctx)
	if err == nil && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("XDG_SESSION_TYPE") != "wayland" {
		return native, nil
	}
	accessible, accessErr := inspectunix.Windows(ctx)
	if err != nil && accessErr != nil {
		return nil, err
	}
	for _, a := range accessible {
		found := false
		for _, n := range native {
			if n.Title == a.Title && (n.PID == 0 || a.PID == 0 || n.PID == a.PID) {
				found = true
				break
			}
		}
		if !found {
			native = append(native, a)
		}
	}
	return native, nil
}
func (w inspectionWindows) Act(ctx context.Context, target WindowInfo, action WindowAction) error {
	if strings.HasPrefix(target.ID, "atspi:") {
		return inspectunix.WindowAction(ctx, target, action)
	}
	return w.Window.Act(ctx, target, action)
}
func enrichWindowBackend(w Window) Window {
	return metadataWindow{inspectionWindows{w}, inspectunix.EnrichWindow}
}

func rawPlatformWindow(w Window) Window {
	if v, ok := w.(inspectionWindows); ok {
		v.Window = rawWindow(v.Window)
		return v
	}
	return w
}

func (w inspectionWindows) Place(ctx context.Context, target WindowInfo, p WindowPlacement) error {
	if strings.HasPrefix(target.ID, "atspi:") {
		return ErrUnsupported
	}
	if placer, ok := w.Window.(windowPlacer); ok {
		return placer.Place(ctx, target, p)
	}
	return ErrUnsupported
}
