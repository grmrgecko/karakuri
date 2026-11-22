//go:build !windows && !darwin

package inspectunix

import (
	"context"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/grmrgecko/karakuri/automation/autoapi"
)

const mutterInterface = "org.gnome.Mutter.DisplayConfig"

type monitorSpec struct{ Connector, Vendor, Product, Serial string }
type mutterMode struct {
	ID                      string
	Width, Height           int32
	Refresh, PreferredScale float64
	Scales                  []float64
	Properties              map[string]dbus.Variant
}
type mutterMonitor struct {
	Spec       monitorSpec
	Modes      []mutterMode
	Properties map[string]dbus.Variant
}
type logicalMonitor struct {
	X, Y       int32
	Scale      float64
	Transform  uint32
	Primary    bool
	Monitors   []monitorSpec
	Properties map[string]dbus.Variant
}
type configuredMonitor struct {
	Connector, Mode string
	Properties      map[string]dbus.Variant
}
type configuredLogical struct {
	X, Y      int32
	Scale     float64
	Transform uint32
	Primary   bool
	Monitors  []configuredMonitor
}

func mutterState(ctx context.Context, c *dbus.Conn) (uint32, []mutterMonitor, []logicalMonitor, map[string]dbus.Variant, error) {
	var serial uint32
	var monitors []mutterMonitor
	var logical []logicalMonitor
	var props map[string]dbus.Variant
	err := c.Object(mutterInterface, "/org/gnome/Mutter/DisplayConfig").CallWithContext(ctx, mutterInterface+".GetCurrentState", 0).Store(&serial, &monitors, &logical, &props)
	return serial, monitors, logical, props, err
}
func mutterDisplays(ctx context.Context) ([]autoapi.DisplayInfo, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_, monitors, logical, _, err := mutterState(ctx, conn)
	if err != nil {
		return nil, err
	}
	out := []autoapi.DisplayInfo{}
	for _, l := range logical {
		for _, spec := range l.Monitors {
			for _, m := range monitors {
				if m.Spec != spec {
					continue
				}
				for _, mode := range m.Modes {
					current, _ := mode.Properties["is-current"].Value().(bool)
					if current {
						out = append(out, autoapi.DisplayInfo{ID: spec.Connector, X: int(l.X), Y: int(l.Y), Width: int(mode.Width), Height: int(mode.Height), Depth: 32, Refresh: mode.Refresh, Primary: l.Primary})
					}
				}
			}
		}
	}
	return out, nil
}
func setMutterDisplay(ctx context.Context, wanted autoapi.DisplayMode) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	serial, monitors, logical, props, err := mutterState(ctx, conn)
	if err != nil {
		return err
	}
	config := []configuredLogical{}
	for _, l := range logical {
		entry := configuredLogical{X: l.X, Y: l.Y, Scale: l.Scale, Transform: l.Transform, Primary: l.Primary}
		for _, spec := range l.Monitors {
			selected := ""
			for _, m := range monitors {
				if m.Spec != spec {
					continue
				}
				for _, mode := range m.Modes {
					current, _ := mode.Properties["is-current"].Value().(bool)
					if !l.Primary && current {
						selected = mode.ID
						break
					}
					if l.Primary && int(mode.Width) == wanted.Width && int(mode.Height) == wanted.Height {
						for _, scale := range mode.Scales {
							if scale == l.Scale {
								selected = mode.ID
								break
							}
						}
						if selected != "" {
							break
						}
					}
				}
			}
			if selected == "" {
				return fmt.Errorf("output %s has no requested mode at scale %g", spec.Connector, l.Scale)
			}
			entry.Monitors = append(entry.Monitors, configuredMonitor{spec.Connector, selected, map[string]dbus.Variant{}})
		}
		config = append(config, entry)
	}
	applyProps := map[string]dbus.Variant{}
	if v, ok := props["layout-mode"]; ok {
		applyProps["layout-mode"] = v
	}
	object := conn.Object(mutterInterface, "/org/gnome/Mutter/DisplayConfig")
	for _, method := range []uint32{0, 1} {
		if err = object.CallWithContext(ctx, mutterInterface+".ApplyMonitorsConfig", 0, serial, method, config, applyProps).Err; err != nil {
			return err
		}
	}
	return nil
}
