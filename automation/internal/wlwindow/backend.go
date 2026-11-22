//go:build !windows && !darwin

package wlwindow

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Backend implements autoapi.Window over the foreign-toplevel protocol. Each
// call opens its own short-lived connection, the same stateless bargain wldata
// and wlinput make.
type Backend struct{}

// New probes the session for the foreign-toplevel manager and returns a backend,
// or an error when the compositor does not serve it.
func New(ctx context.Context) (*Backend, error) {
	c, err := setup(ctx)
	if err != nil {
		return nil, err
	}
	c.close()
	return &Backend{}, nil
}

// List reports the windows the compositor knows about. The ID is the app id and
// title joined, the key Act matches a window by, since the toplevel handle does
// not survive the connection that saw it. The PID is left zero: the protocol
// does not carry it.
func (Backend) List(ctx context.Context) ([]autoapi.WindowInfo, error) {
	c, err := setup(ctx)
	if err != nil {
		return nil, err
	}
	defer c.close()
	tops, err := c.enumerate()
	if err != nil {
		return nil, err
	}
	out := make([]autoapi.WindowInfo, 0, len(tops))
	for _, t := range tops {
		out = append(out, autoapi.WindowInfo{
			ID:     windowKey(t.appID, t.title),
			Title:  t.title,
			Class:  t.appID,
			Active: t.active,
		})
	}
	return out, nil
}

// Act performs an action on the window the caller named. It re-enumerates,
// matches the target by app id and title, and sends the request on that
// connection's handle. A target that matches nothing is an error rather than a
// silent no-op, because acting on the wrong window is worse than not acting.
func (Backend) Act(ctx context.Context, w autoapi.WindowInfo, action autoapi.WindowAction) error {
	c, err := setup(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	tops, err := c.enumerate()
	if err != nil {
		return err
	}

	want := w.ID
	if want == "" {
		want = windowKey(w.Class, w.Title)
	}
	var target *toplevel
	for _, t := range tops {
		if windowKey(t.appID, t.title) == want {
			if target != nil {
				return fmt.Errorf("multiple windows have the selected app id and title")
			}
			target = t
		}
	}
	if target == nil {
		named := w.Title
		if named == "" {
			named = w.Class
		}
		return fmt.Errorf("no window matching %q is open", named)
	}

	switch action {
	case autoapi.WindowActivate:
		if c.seat == 0 {
			return fmt.Errorf("cannot activate a window: the compositor exposed no seat")
		}
		if err := c.request(target.handle, handleRequestActivate, c.seat); err != nil {
			return err
		}
	case autoapi.WindowMinimize:
		if err := c.request(target.handle, handleRequestSetMinimized); err != nil {
			return err
		}
	case autoapi.WindowMaximize:
		if err := c.request(target.handle, handleRequestSetMaximized); err != nil {
			return err
		}
	case autoapi.WindowAction("restore"):
		if err := c.request(target.handle, handleRequestUnsetMinimized); err != nil {
			return err
		}
		if err := c.request(target.handle, handleRequestUnsetMaximized); err != nil {
			return err
		}
		if c.managerVersion >= 2 {
			if err := c.request(target.handle, handleRequestUnsetFullscreen); err != nil {
				return err
			}
		}
	case autoapi.WindowClose:
		if err := c.request(target.handle, handleRequestClose); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown window action %q", action)
	}
	// Flush the request before the connection closes, so the compositor sees it.
	return c.roundtrip(nil)
}

// windowKey is the stable match key for a window across the list and act calls.
// A NUL joins the two fields so an app id ending where a title begins cannot be
// confused with a different split.
func windowKey(appID, title string) string {
	return "wl:" + base64.RawURLEncoding.EncodeToString([]byte(appID+"\x00"+title))
}
