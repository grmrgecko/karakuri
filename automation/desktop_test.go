package automation

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"net"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// desktopWireFixture supplies synthetic OS observations to the real helper protocol.
// A unit test cannot safely click or change the developer's desktop.
type desktopWireFixture struct{ sess *Session }

func (desktopWireFixture) Drag(_ context.Context, r DragRequest) error {
	if r.StartX != -800 || r.EndY != 42 || r.Duration != 250*time.Millisecond || r.Button != "right" {
		return fmt.Errorf("incorrect drag request")
	}
	return nil
}
func (d desktopWireFixture) PlaceWindow(ctx context.Context, w WindowInfo, p WindowPlacement) error {
	return (localDesktop{d.sess}).PlaceWindow(ctx, w, p)
}

type placementWireFixture struct{}

func (placementWireFixture) List(context.Context) ([]WindowInfo, error)          { return nil, nil }
func (placementWireFixture) Act(context.Context, WindowInfo, WindowAction) error { return nil }
func (placementWireFixture) Place(_ context.Context, w WindowInfo, p WindowPlacement) error {
	if w.ID != "0x11" || !p.Restore || !p.Move || !p.Resize || p.X != -800 || p.Height != 480 {
		return fmt.Errorf("incorrect placement request")
	}
	return nil
}
func (desktopWireFixture) Controls(context.Context, WindowInfo) ([]Control, error) {
	return []Control{{ID: "0x42", Text: "", Protected: true, Visible: true, Enabled: true, X: -800, Width: 120}}, nil
}
func (desktopWireFixture) Menus(context.Context, WindowInfo) ([]MenuItem, error) {
	checked := true
	return []MenuItem{{ID: "file", Path: "File", Children: []MenuItem{{ID: "42", Path: "File > Save", Enabled: true, Checked: &checked}}}}, nil
}
func (desktopWireFixture) InvokeMenu(_ context.Context, w WindowInfo, target string) error {
	if w.ID != "0x11" || target != "File > Save" {
		return fmt.Errorf("unknown menu target")
	}
	return nil
}
func (desktopWireFixture) ClickControl(_ context.Context, w WindowInfo, target ControlTarget) error {
	if w.ID != "0x11" || target.ID != "42.7" || target.Text != "Save" || target.Role != "button" || target.Parent != "42" || target.MatchMode != "exact" {
		return fmt.Errorf("unknown control target")
	}
	return nil
}
func (desktopWireFixture) CaptureWindow(_ context.Context, w WindowInfo) (image.Image, error) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(1, 0, color.NRGBA{R: 17, G: 34, B: 51, A: 255})
	if w.ID == "mapped" {
		return autoapi.WithCaptureMapping(img, &CaptureMapping{OriginX: -800, OriginY: 40, UnitsPerPixelX: 0.5, UnitsPerPixelY: 1}), nil
	}
	return img, nil
}
func (desktopWireFixture) Dialogs(_ context.Context, options DialogOptions) (DialogResultList, error) {
	if !options.Scope || options.Target.Title != "Setup" || options.Target.MatchMode != "exact" || options.Target.PID != 42 || options.IncludeControls == nil || *options.IncludeControls {
		return DialogResultList{}, fmt.Errorf("incorrect dialog options")
	}
	return DialogResultList{Dialogs: []DialogInfo{{Window: SnapshotWindowInfo{ID: "0x11"}}}, Errors: map[string]string{"0x22": "provider unavailable"}}, nil
}

// TestDesktopWire preserves native IDs, partial errors, protected values, and PNG
// pixels across the same authenticated-session protocol used by desktop helpers.
func TestDesktopWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess := &Session{Window: placementWireFixture{}, env: Environment{Backends: map[string]string{}}}
	wireInput(sess)
	sess.Window = metadataWindow{Window: enrichWindowBackend(sess.Window), enrich: func(_ context.Context, w WindowInfo) WindowInfo { return w }}
	sess.Desktop = desktopWireFixture{sess}
	server := NewSessionServer(sess)
	clientConn, serverConn := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, serverConn) }()
	client, err := NewRemoteSession(ctx, clientConn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	w := WindowInfo{ID: "0x11"}
	require.NoError(t, client.Desktop.Drag(ctx, DragRequest{StartX: -800, EndY: 42, Button: "right", Duration: 250 * time.Millisecond}))
	require.NoError(t, client.Desktop.PlaceWindow(ctx, w, WindowPlacement{Restore: true, Move: true, Resize: true, X: -800, Width: 640, Height: 480}))
	controls, err := client.Desktop.Controls(ctx, w)
	require.NoError(t, err)
	require.Len(t, controls, 1)
	assert.True(t, controls[0].Protected)
	assert.Empty(t, controls[0].Text)
	assert.Equal(t, -800, controls[0].X)
	menus, err := client.Desktop.Menus(ctx, w)
	require.NoError(t, err)
	require.Len(t, menus, 1)
	require.Len(t, menus[0].Children, 1)
	assert.Equal(t, "42", menus[0].Children[0].ID)
	assert.Nil(t, menus[0].Checked)
	require.NotNil(t, menus[0].Children[0].Checked)
	assert.True(t, *menus[0].Children[0].Checked)
	for _, tc := range []struct {
		target string
		valid  bool
	}{{"File > Save", true}, {"File > Missing", false}} {
		err := client.Desktop.InvokeMenu(ctx, w, tc.target)
		if tc.valid {
			assert.NoError(t, err)
		} else {
			assert.ErrorContains(t, err, "unknown menu target")
		}
	}
	require.NoError(t, client.Desktop.ClickControl(ctx, w, ControlTarget{ID: "42.7", Text: "Save", Role: "button", Parent: "42", MatchMode: "exact"}))
	img, err := client.Desktop.CaptureWindow(ctx, w)
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 2, 1), img.Bounds())
	assert.Equal(t, color.NRGBA{R: 17, G: 34, B: 51, A: 255}, color.NRGBAModel.Convert(img.At(1, 0)))
	assert.False(t, ImageCaptureGeometry(img).MappingKnown)
	mapped, err := client.Desktop.CaptureWindow(ctx, WindowInfo{ID: "mapped"})
	require.NoError(t, err)
	assert.Equal(t, &CaptureMapping{OriginX: -800, OriginY: 40, UnitsPerPixelX: 0.5, UnitsPerPixelY: 1}, autoapi.ImageCaptureMapping(mapped))
	includeControls := false
	dialogs, err := client.Desktop.Dialogs(ctx, DialogOptions{Scope: true, Target: WindowTarget{Title: "Setup", MatchMode: "exact", PID: 42}, IncludeControls: &includeControls})
	require.NoError(t, err)
	assert.Equal(t, "provider unavailable", dialogs.Errors["0x22"])
	require.Len(t, dialogs.Dialogs, 1)
	assert.Equal(t, "0x11", dialogs.Dialogs[0].Window.ID)
	require.NoError(t, client.Close())
	require.NoError(t, <-done)
}
