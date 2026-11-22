//go:build unix && !darwin

// Package kwin lists and acts on windows on a KDE Plasma (KWin) session, which
// a Wayland compositor exposes no other way to a command-line caller. KWin runs
// the code through its scripting engine: a script is loaded over D-Bus, started,
// and hands its result back by calling a helper method this package exports. It
// is the one backend that reaches Wayland-native windows on this session, where
// the X server sees only the XWayland clients.
package kwin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/session"
)

const (
	kwinService = "org.kde.KWin"
	kwinPath    = "/Scripting"
	kwinIface   = "org.kde.kwin.Scripting"

	helperName = "org.karakuri.KWinHelper"
	helperPath = "/org/karakuri/KWinHelper"

	scriptName = "karakuri-automation-window"
	runTimeout = 5 * time.Second
)

// Backend implements autoapi.Window over KWin scripting.
type Backend struct{}

// New returns the KWin window backend.
func New() Backend { return Backend{} }

// Available reports whether KWin is on the session bus to script.
func Available() bool {
	return session.NameHasOwner(kwinService)
}

// helper receives the one string a running KWin script hands back.
type helper struct {
	result chan string
}

// Return is the method a KWin script calls with its output.
func (h *helper) Return(s string) *dbus.Error {
	select {
	case h.result <- s:
	default:
	}
	return nil
}

var (
	setupOnce sync.Once
	setupErr  error
	runMu     sync.Mutex
	callback  = &helper{result: make(chan string, 1)}
)

// ensureHelper claims the helper name and exports the callback object once on
// the shared session bus.
func ensureHelper() error {
	setupOnce.Do(func() {
		conn := session.SessionBus()
		if conn == nil {
			setupErr = fmt.Errorf("no session bus to script KWin over")
			return
		}
		if err := conn.Export(callback, dbus.ObjectPath(helperPath), helperName); err != nil {
			setupErr = err
			return
		}
		if _, err := conn.RequestName(helperName, dbus.NameFlagDoNotQueue); err != nil {
			setupErr = err
		}
	})
	return setupErr
}

// run loads a script that ends by calling the helper, starts it, and returns
// what it handed back. Calls are serialized because the helper name and result
// channel are shared.
func run(ctx context.Context, body string) (string, error) {
	if err := ensureHelper(); err != nil {
		return "", err
	}
	conn := session.SessionBus()

	runMu.Lock()
	defer runMu.Unlock()

	// Drain any stale result the previous run left behind.
	select {
	case <-callback.result:
	default:
	}

	// The script body sets `result`; this wrapper delivers it to the helper.
	script := body + fmt.Sprintf(`
try {
  callDBus(%q, %q, %q, "Return", "" + result);
} catch (e) {
  callDBus(%q, %q, %q, "Return", "ERR:" + e);
}
`, helperName, helperPath, helperName, helperName, helperPath, helperName)

	file, err := os.CreateTemp("", "karakuri-kwin-*.js")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := file.WriteString(script); err != nil {
		_ = file.Close()
		return "", err
	}
	_ = file.Close()

	scripting := conn.Object(kwinService, kwinPath)
	// Unload any leftover from a previous run that did not clean up.
	_ = scripting.CallWithContext(ctx, kwinIface+".unloadScript", 0, scriptName).Err
	if err := scripting.CallWithContext(ctx, kwinIface+".loadScript", 0, path, scriptName).Err; err != nil {
		return "", fmt.Errorf("KWin refused the window script: %w", err)
	}
	defer scripting.CallWithContext(context.Background(), kwinIface+".unloadScript", 0, scriptName)

	if err := scripting.CallWithContext(ctx, kwinIface+".start", 0).Err; err != nil {
		return "", fmt.Errorf("KWin could not start the window script: %w", err)
	}

	timeout := time.NewTimer(runTimeout)
	defer timeout.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timeout.C:
		return "", fmt.Errorf("the KWin window script did not answer in time")
	case out := <-callback.result:
		if len(out) >= 4 && out[:4] == "ERR:" {
			return "", fmt.Errorf("KWin window script: %s", out[4:])
		}
		return out, nil
	}
}

// jsWindow is the shape the listing script emits per window.
type jsWindow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Class  string `json:"class"`
	PID    int    `json:"pid"`
	Active bool   `json:"active"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// List reports the normal windows KWin manages.
func (Backend) List(ctx context.Context) ([]autoapi.WindowInfo, error) {
	const body = `
var out = [];
var wins = workspace.windowList();
var active = workspace.activeWindow;
for (var i = 0; i < wins.length; i++) {
  var w = wins[i];
  if (!w.normalWindow) { continue; }
  out.push({
    id: "" + w.internalId,
    title: w.caption ? w.caption : "",
    class: w.resourceClass ? w.resourceClass : "",
    pid: w.pid,
    active: (active && w.internalId == active.internalId),
    x: w.frameGeometry.x, y: w.frameGeometry.y,
    width: w.frameGeometry.width, height: w.frameGeometry.height
  });
}
var result = JSON.stringify(out);
`
	raw, err := run(ctx, body)
	if err != nil {
		return nil, err
	}
	var windows []jsWindow
	if err := json.Unmarshal([]byte(raw), &windows); err != nil {
		return nil, fmt.Errorf("cannot read the KWin window list: %w", err)
	}
	result := make([]autoapi.WindowInfo, 0, len(windows))
	for _, w := range windows {
		result = append(result, autoapi.WindowInfo{
			ID:     w.ID,
			Title:  w.Title,
			Class:  w.Class,
			PID:    w.PID,
			Active: w.Active,
			X:      w.X, Y: w.Y, Width: w.Width, Height: w.Height,
		})
	}
	return result, nil
}

// Act performs one action on the window with the given internal id.
func (Backend) Act(ctx context.Context, w autoapi.WindowInfo, action autoapi.WindowAction) error {
	var verb string
	switch action {
	case autoapi.WindowActivate:
		verb = `workspace.activeWindow = win;`
	case autoapi.WindowMinimize:
		verb = `win.minimized = true;`
	case autoapi.WindowMaximize:
		verb = `win.setMaximize(true, true);`
	case autoapi.WindowClose:
		verb = `win.closeWindow();`
	default:
		return fmt.Errorf("unknown window action %q", action)
	}

	// The id is embedded as a JSON string literal so a stray quote cannot break
	// the script.
	idLiteral, _ := json.Marshal(w.ID)
	body := fmt.Sprintf(`
var target = %s;
var wins = workspace.windowList();
var win = null;
for (var i = 0; i < wins.length; i++) {
  if ("" + wins[i].internalId == target) { win = wins[i]; break; }
}
var result;
if (win == null) {
  result = "ERR:no window with id " + target;
} else {
  %s
  result = "ok";
}
`, string(idLiteral), verb)

	out, err := run(ctx, body)
	if err != nil {
		return err
	}
	if out != "ok" {
		return fmt.Errorf("KWin window action failed: %s", out)
	}
	return nil
}

// Place requests floating window geometry through KWin's scripting API.
func (Backend) Place(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	id, _ := json.Marshal(w.ID)
	body := fmt.Sprintf(`
var wins=workspace.windowList(); var win=null;
for(var i=0;i<wins.length;i++){if(""+wins[i].internalId==%s){win=wins[i];break;}}
var result="ok";
if(win==null){result="ERR:window no longer exists";}
else if ((%t||%t) && win.tile) {result="ERR:window placement requires a floating window";}
else if (%t && !win.moveable) {result="ERR:window does not support movement";}
else if (%t && !win.resizeable) {result="ERR:window does not support resizing";}
else {
 if(%t){win.minimized=false;win.fullScreen=false;win.setMaximize(false,false);}
 if(%t||%t){var r=win.frameGeometry;win.frameGeometry={x:%t?%d:r.x,y:%t?%d:r.y,width:%t?%d:r.width,height:%t?%d:r.height};}
}
`, string(id), p.Move, p.Resize, p.Move, p.Resize, p.Restore, p.Move, p.Resize, p.Move, p.X, p.Move, p.Y, p.Resize, p.Width, p.Resize, p.Height)
	out, err := run(ctx, body)
	if err != nil {
		return err
	}
	if out != "ok" {
		return fmt.Errorf("KWin window placement: %s", out)
	}
	return nil
}
