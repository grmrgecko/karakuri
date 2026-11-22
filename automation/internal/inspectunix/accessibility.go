//go:build !windows && !darwin

// Package inspectunix reads desktop accessibility through AT-SPI over D-Bus.
package inspectunix

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/grmrgecko/karakuri/automation/autoapi"
)

type Backend struct{}
type reference struct {
	Bus  string
	Path dbus.ObjectPath
}

const accessible = "org.a11y.atspi.Accessible"

func connect(ctx context.Context) (*dbus.Conn, error) {
	session, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var address string
	if err = session.Object("org.a11y.Bus", "/org/a11y/bus").CallWithContext(ctx, "org.a11y.Bus.GetAddress", 0).Store(&address); err != nil {
		return nil, fmt.Errorf("accessibility bus unavailable; enable desktop accessibility: %w", err)
	}
	return dbus.Connect(address)
}
func call(ctx context.Context, b *dbus.Conn, r reference, method string, args ...any) *dbus.Call {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return b.Object(r.Bus, r.Path).CallWithContext(c, method, 0, args...)
}
func children(ctx context.Context, b *dbus.Conn, r reference) ([]reference, error) {
	var refs []reference
	err := call(ctx, b, r, accessible+".GetChildren").Store(&refs)
	return refs, err
}
func properties(ctx context.Context, b *dbus.Conn, r reference) map[string]dbus.Variant {
	var props map[string]dbus.Variant
	_ = call(ctx, b, r, "org.freedesktop.DBus.Properties.GetAll", accessible).Store(&props)
	return props
}
func textProperty(props map[string]dbus.Variant, key string) string {
	s, _ := props[key].Value().(string)
	return s
}
func root(ctx context.Context, b *dbus.Conn, w autoapi.WindowInfo) (reference, error) {
	apps, err := children(ctx, b, reference{"org.a11y.atspi.Registry", "/org/a11y/atspi/accessible/root"})
	if err != nil {
		return reference{}, err
	}
	var found, geometry []reference
	for _, app := range apps {
		if ctx.Err() != nil {
			return reference{}, ctx.Err()
		}
		if w.PID > 0 {
			if processID(ctx, b, app) != w.PID {
				continue
			}
		}
		windows, err := children(ctx, b, app)
		if err != nil {
			continue
		}
		for _, ref := range windows {
			if w.ID == "atspi:"+refID(ref) {
				return ref, nil
			}
			if textProperty(properties(ctx, b, ref), "Name") == w.Title {
				found = append(found, ref)
			}
			// GTK dialogs can expose a role name instead of their native title.
			// The root or its immediate content child carries the X11 client bounds.
			if w.PID > 0 && w.Width > 0 && w.Height > 0 && displayBackend() == "xrandr" {
				refs, _ := children(ctx, b, ref)
				for _, candidate := range append([]reference{ref}, refs...) {
					e := readElement(ctx, b, candidate)
					if e.X == w.X && e.Y == w.Y && e.Width == w.Width && e.Height == w.Height {
						geometry = append(geometry, ref)
						break
					}
				}
			}
		}
	}
	if len(found) == 0 && len(geometry) == 1 {
		return geometry[0], nil
	}
	if len(found) != 1 {
		return reference{}, fmt.Errorf("%d accessibility windows match %q; enable accessibility in the application and use a window with a unique title", len(found), w.Title)
	}
	return found[0], nil
}
func processID(ctx context.Context, b *dbus.Conn, r reference) int {
	var pid uint32
	if call(ctx, b, r, accessible+".GetProcessId").Store(&pid) != nil || pid == 0 {
		_ = b.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, r.Bus).Store(&pid)
	}
	return int(pid)
}
func refID(r reference) string { return r.Bus + "|" + string(r.Path) }
func readElement(ctx context.Context, b *dbus.Conn, r reference) autoapi.Element {
	props := properties(ctx, b, r)
	e := autoapi.Element{ID: refID(r), Name: textProperty(props, "Name"), Class: textProperty(props, "AccessibleId")}
	_ = call(ctx, b, r, accessible+".GetRoleName").Store(&e.Role)
	var states []uint32
	if call(ctx, b, r, accessible+".GetState").Store(&states) == nil && len(states) > 0 {
		e.Enabled = states[0]&(1<<8) != 0 && states[0]&(1<<24) != 0
		e.Visible = states[0]&(1<<25) != 0 && states[0]&(1<<30) != 0
	}
	e.Protected = e.Role == "password text"
	var interfaces []string
	_ = call(ctx, b, r, accessible+".GetInterfaces").Store(&interfaces)
	for _, iface := range interfaces {
		switch iface {
		case "org.a11y.atspi.Component":
			var rect struct{ X, Y, Width, Height int32 }
			if call(ctx, b, r, iface+".GetExtents", uint32(0)).Store(&rect) == nil && rect.X != -2147483648 && rect.Y != -2147483648 && rect.Width > 0 && rect.Height > 0 {
				e.X = int(rect.X)
				e.Y = int(rect.Y)
				e.Width = int(rect.Width)
				e.Height = int(rect.Height)
			}
		case "org.a11y.atspi.Text":
			if !e.Protected {
				var count dbus.Variant
				if call(ctx, b, r, "org.freedesktop.DBus.Properties.Get", iface, "CharacterCount").Store(&count) == nil {
					n, _ := count.Value().(int32)
					if n > 32768 {
						n = 32768
					}
					if n > 0 {
						_ = call(ctx, b, r, iface+".GetText", int32(0), n).Store(&e.Text)
					}
				}
			}
		case "org.a11y.atspi.Action":
			// Rocky 9's at-spi2-atk 2.38 aborts the application in GetActions.
			// GetName also avoids localized names when choosing DoAction's index.
			var count dbus.Variant
			if call(ctx, b, r, "org.freedesktop.DBus.Properties.Get", iface, "NActions").Store(&count) == nil {
				n, ok := count.Value().(int32)
				if ok && n > 0 && n <= 64 {
					e.Actions = make([]string, n)
					known := false
					for i := int32(0); i < n && ctx.Err() == nil; i++ {
						// Keep unreadable slots so later names retain their provider index.
						if call(ctx, b, r, iface+".GetName", i).Store(&e.Actions[i]) == nil && e.Actions[i] != "" {
							known = true
						}
					}
					if !known {
						e.Actions = nil
					}
				}
			}
		}
	}
	return e
}
func (Backend) Elements(ctx context.Context, w autoapi.WindowInfo) ([]autoapi.Element, error) {
	b, err := connect(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	r, err := root(ctx, b, w)
	if err != nil {
		return nil, err
	}
	type pending struct {
		Ref          reference
		Parent, Menu string
		Depth        int
	}
	queue := []pending{{Ref: r}}
	out := make([]autoapi.Element, 0)
	seen := map[string]bool{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := queue[0]
		queue = queue[1:]
		id := refID(n.Ref)
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(out) >= 4096 || n.Depth > 64 {
			return nil, fmt.Errorf("accessibility tree exceeds 4096 elements or 64 levels")
		}
		e := readElement(ctx, b, n.Ref)
		e.Parent = n.Parent
		menu := n.Menu
		if strings.Contains(e.Role, "menu") && e.Role != "menu bar" && e.Name != "" {
			if menu != "" {
				menu += " > "
			}
			menu += e.Name
			e.Path = menu
		}
		out = append(out, e)
		refs, err := children(ctx, b, n.Ref)
		if err != nil {
			continue
		}
		if len(queue)+len(refs) > 8192 {
			return nil, fmt.Errorf("accessibility tree has too many pending children")
		}
		for _, child := range refs {
			queue = append(queue, pending{child, id, menu, n.Depth + 1})
		}
	}
	return out, nil
}
func (b Backend) Invoke(ctx context.Context, w autoapi.WindowInfo, id string) error {
	elements, err := b.Elements(ctx, w)
	if err != nil {
		return err
	}
	var target *autoapi.Element
	for i := range elements {
		if elements[i].ID == id {
			target = &elements[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("element no longer belongs to the selected window")
	}
	if !target.Enabled {
		return fmt.Errorf("element is disabled")
	}
	index := -1
	for _, want := range []string{"click", "press", "activate", "toggle"} {
		for i, a := range target.Actions {
			if strings.EqualFold(a, want) {
				index = i
				break
			}
		}
		if index >= 0 {
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("element exposes no click, press, activate, or toggle action")
	}
	bus, path, ok := strings.Cut(id, "|")
	if !ok {
		return fmt.Errorf("invalid accessibility element id")
	}
	conn, err := connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var done bool
	if err = call(ctx, conn, reference{bus, dbus.ObjectPath(path)}, "org.a11y.atspi.Action.DoAction", int32(index)).Store(&done); err != nil {
		return err
	}
	if !done {
		return fmt.Errorf("application rejected the accessibility action")
	}
	return nil
}
