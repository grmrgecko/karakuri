package openhab

import (
	"context"
	"fmt"
	"sync/atomic"
)

// WatchEntity hands a device its item's value as the device kind holds it:
// the current state now, then every change. NULL and UNDEF hand nothing, so
// the device shows no value rather than a made-up one. A watch placed before
// the item is in the copy, as every device's is when the server starts, gets
// the state from the read that first lists the item: that read reports no
// change, so nothing else would hand it over.
func (d *Driver) WatchEntity(name string, fn func(map[string]any)) (func(), error) {
	stop := d.SubscribeChanges(func(c ItemChange) {
		if c.Name != name {
			return
		}
		it := c.Item
		it.State = c.New
		if v := Values(it); v != nil {
			fn(v)
		}
	})
	var seeded atomic.Bool
	seed := func(c Catalog) {
		it, ok := c[name]
		if !ok || !seeded.CompareAndSwap(false, true) {
			return
		}
		if v := Values(it); v != nil {
			fn(v)
		}
	}
	stopSeed := d.WatchItems(seed)
	seed(d.Items())
	return func() {
		stopSeed()
		stop()
	}, nil
}

// WatchEntityAvailability reports whether the item exists on the server; a
// removed item makes its device unavailable.
func (d *Driver) WatchEntityAvailability(name string, fn func(available bool)) (func(), error) {
	stop := d.WatchItems(func(c Catalog) {
		_, ok := c[name]
		fn(ok)
	})
	if d.Connected() {
		_, ok := d.Item(name)
		fn(ok)
	}
	return stop, nil
}

// EntityWritable reports whether an item takes commands from a device: a
// writable type that openHAB does not mark read only. known is false while
// the item is not in the copy.
func (d *Driver) EntityWritable(name string) (writable, known bool) {
	it, ok := d.Item(name)
	if !ok {
		return false, false
	}
	return Propose(it).Writable, true
}

// CommandEntity sends one device write to its item as the mapped openHAB
// command. The device's value moves when openHAB reports the new state.
func (d *Driver) CommandEntity(ctx context.Context, name, field string, value any) error {
	it, ok := d.Item(name)
	if !ok {
		return fmt.Errorf("openHAB has no item %s", name)
	}
	cmd, err := Command(it, field, value)
	if err != nil {
		return err
	}
	return d.SendCommand(ctx, name, cmd)
}
