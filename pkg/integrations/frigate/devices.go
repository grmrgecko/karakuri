package frigate

import (
	"context"
	"fmt"
	"strings"
)

// Switch is one camera switch a device can follow, with object ID
// <camera>/<feature>.
type Switch struct {
	ObjectID string
	Camera   string
	Feature  string
}

// Switches lists every camera's switches.
func (d *Driver) Switches() []Switch {
	var out []Switch
	for _, cam := range d.Cameras() {
		for _, f := range Features {
			out = append(out, Switch{ObjectID: cam.Name + "/" + f, Camera: cam.Name, Feature: f})
		}
	}
	return out
}

// splitObjectID reads <camera>/<feature>.
func splitObjectID(objectID string) (string, string, error) {
	cameraName, feature, ok := strings.Cut(objectID, "/")
	if !ok || !CameraNameRe.MatchString(cameraName) || !isFeature(feature) {
		return "", "", fmt.Errorf("%q is not a Frigate camera switch", objectID)
	}
	return cameraName, feature, nil
}

// WatchEntity calls fn with a switch's value {on}, now if known and on every
// change Frigate reports.
func (d *Driver) WatchEntity(objectID string, fn func(map[string]any)) (func(), error) {
	cameraName, feature, err := splitObjectID(objectID)
	if err != nil {
		return nil, err
	}
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	if d.entityWatch[objectID] == nil {
		d.entityWatch[objectID] = map[int]func(map[string]any){}
	}
	d.entityWatch[objectID][id] = fn
	d.watchMu.Unlock()

	d.mu.Lock()
	cam, ok := d.cameras[cameraName]
	var on, known bool
	if ok {
		on, known = cam.switches[feature]
	}
	connected := d.status.State == "connected"
	d.mu.Unlock()
	if known && connected {
		fn(map[string]any{"on": on})
	}
	return func() {
		d.watchMu.Lock()
		delete(d.entityWatch[objectID], id)
		d.watchMu.Unlock()
	}, nil
}

// WatchEntityAvailability calls fn with whether a switch's camera receives
// frames, now and on every change.
func (d *Driver) WatchEntityAvailability(objectID string, fn func(bool)) (func(), error) {
	cameraName, _, err := splitObjectID(objectID)
	if err != nil {
		return nil, err
	}
	d.watchMu.Lock()
	id := d.nextWatch
	d.nextWatch++
	if d.availWatch[objectID] == nil {
		d.availWatch[objectID] = map[int]func(bool){}
	}
	d.availWatch[objectID][id] = fn
	d.watchMu.Unlock()
	if d.Status().State == "connected" {
		fn(d.Receiving(cameraName))
	}
	return func() {
		d.watchMu.Lock()
		delete(d.availWatch[objectID], id)
		d.watchMu.Unlock()
	}, nil
}

// CommandEntity sets a switch. The value is already a boolean of the
// device's kind; the new state arrives as Frigate's state message.
func (d *Driver) CommandEntity(ctx context.Context, objectID, field string, value any) error {
	cameraName, feature, err := splitObjectID(objectID)
	if err != nil {
		return err
	}
	on, _ := value.(bool)
	return d.sendSwitch(ctx, cameraName, feature, on)
}
