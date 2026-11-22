package portal

import (
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/grmrgecko/karakuri/automation/autoapi"
)

type monitorStream struct {
	Node       uint32
	Properties map[string]dbus.Variant
}

func decodeStreams(value dbus.Variant) ([]monitorStream, error) {
	if value.Signature().String() != "a(ua{sv})" {
		return nil, fmt.Errorf("invalid portal streams signature %s", value.Signature())
	}
	rows, ok := value.Value().([][]any)
	if !ok {
		return nil, fmt.Errorf("invalid portal stream array")
	}
	streams := make([]monitorStream, 0, len(rows))
	for _, row := range rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("invalid portal stream tuple")
		}
		node, nodeOK := row[0].(uint32)
		properties, propsOK := row[1].(map[string]dbus.Variant)
		if !nodeOK || !propsOK {
			return nil, fmt.Errorf("invalid portal stream fields")
		}
		// Store recursively unwraps and rewraps nested variants, losing the
		// (ii) signature on decoded struct slices. Preserve the property map.
		streams = append(streams, monitorStream{Node: node, Properties: properties})
	}
	return streams, nil
}

func streamPair(properties map[string]dbus.Variant, key string) (int64, int64, bool) {
	v, ok := properties[key]
	if !ok || v.Signature().String() != "(ii)" {
		return 0, 0, false
	}
	var pair struct{ X, Y int32 }
	if err := dbus.Store([]any{v.Value()}, &pair); err != nil {
		return 0, 0, false
	}
	return int64(pair.X), int64(pair.Y), true
}

func streamPoint(streams []monitorStream, x, y int) (uint32, float64, float64, error) {
	for _, stream := range streams {
		px, py, positioned := streamPair(stream.Properties, "position")
		w, h, sized := streamPair(stream.Properties, "size")
		if !positioned || !sized || w <= 0 || h <= 0 {
			continue
		}
		dx, dy := int64(x)-px, int64(y)-py
		if dx >= 0 && dy >= 0 && dx < w && dy < h {
			return stream.Node, float64(dx), float64(dy), nil
		}
	}
	return 0, 0, 0, fmt.Errorf("%w: point (%d,%d) is outside shared monitors with known logical geometry", autoapi.ErrUnsupported, x, y)
}

// sessionClosed drains lifecycle signals while no request is in flight.
func (b *Backend) sessionClosed() bool {
	for {
		select {
		case signal, ok := <-b.sigc:
			if !ok || signal == nil {
				return true
			}
			if signal.Path == b.session && signal.Name == "org.freedesktop.portal.Session.Closed" {
				return true
			}
		default:
			return !b.conn.Connected()
		}
	}
}

// reset closes the private bus connection, releasing its portal session.
// The caller holds mu; the next command may establish a fresh session.
func (b *Backend) reset() {
	if b.conn != nil {
		b.conn.RemoveSignal(b.sigc)
		_ = b.conn.Close()
	}
	b.conn, b.sigc, b.session, b.started, b.streams, b.devices = nil, nil, "", false, nil, 0
}

// Close releases the portal session and its private bus connection.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reset()
	return nil
}
