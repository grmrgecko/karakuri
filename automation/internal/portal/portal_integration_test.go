//go:build karakuri_integration && !windows && !darwin

package portal

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPortalSession uses an isolated real bus and a synthetic portal service.
// Its wire signatures follow the RemoteDesktop and ScreenCast specifications.
func TestPortalSession(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	daemon := exec.CommandContext(ctx, "dbus-daemon", "--session", "--nofork", "--print-address=1")
	stdout, err := daemon.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, daemon.Start())
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	address, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(address))
	conn, err := dbus.ConnectSessionBus()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.RequestName(portalService, dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	fixture := &portalFixture{conn: conn, points: make(chan portalPoint, 4)}
	require.NoError(t, conn.Export(fixture, portalPath, rdIface))
	require.NoError(t, conn.Export(fixture, portalPath, scIface))
	require.NoError(t, conn.Export(fixture, portalPath, "org.freedesktop.DBus.Properties"))
	backend := New("test", autoapi.FileTokenStore(t.TempDir()))
	t.Cleanup(func() { _ = backend.Close() })
	for _, x := range []int{-100, -200} {
		require.NoError(t, backend.Move(ctx, x, 100, false))
		select {
		case point := <-fixture.points:
			assert.Equal(t, portalPoint{71, float64(x + 1280), 100}, point)
		case <-ctx.Done():
			t.Fatal("no absolute pointer event")
		}
	}
	fixture.mu.Lock()
	assert.Equal(t, 1, fixture.starts, "commands must reuse the portal session")
	assert.True(t, fixture.sources, "absolute movement requires selecting monitor sources before Start")
	fixture.mu.Unlock()
	require.NoError(t, backend.Close())
	require.NoError(t, backend.Move(ctx, -300, 100, false))
	fixture.mu.Lock()
	assert.Equal(t, 2, fixture.starts)
	assert.Equal(t, "fixture-token", fixture.restore)
	fixture.mu.Unlock()
}

type portalPoint struct {
	Node uint32
	X, Y float64
}

type portalFixture struct {
	conn    *dbus.Conn
	mu      sync.Mutex
	starts  int
	sources bool
	restore string
	points  chan portalPoint
}

func (p *portalFixture) response(options map[string]dbus.Variant, result map[string]dbus.Variant) dbus.ObjectPath {
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/test/" + options["handle_token"].Value().(string))
	// Signals are intentionally emitted before the method reply. The client's
	// signal subscription must already exist when it invokes the method.
	_ = p.conn.Emit(path, "org.freedesktop.portal.Request.Response", uint32(0), result)
	return path
}

func (p *portalFixture) CreateSession(options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.response(options, map[string]dbus.Variant{"session_handle": dbus.MakeVariant("/org/freedesktop/portal/desktop/session/test/session")}), nil
}

func (p *portalFixture) SelectDevices(_ dbus.ObjectPath, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	p.restore, _ = options["restore_token"].Value().(string)
	p.mu.Unlock()
	return p.response(options, map[string]dbus.Variant{}), nil
}

func (p *portalFixture) SelectSources(_ dbus.ObjectPath, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	p.sources = options["types"].Value() == uint32(1) && options["multiple"].Value() == true
	p.mu.Unlock()
	return p.response(options, map[string]dbus.Variant{}), nil
}

func (p *portalFixture) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface == scIface && property == "AvailableSourceTypes" {
		return dbus.MakeVariant(uint32(1)), nil
	}
	return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("unknown property"))
}

func (p *portalFixture) Start(_ dbus.ObjectPath, _ string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	p.starts++
	p.mu.Unlock()
	type pair struct{ X, Y int32 }
	return p.response(options, map[string]dbus.Variant{
		"devices":       dbus.MakeVariant(uint32(3)),
		"restore_token": dbus.MakeVariant("fixture-token"),
		"streams": dbus.MakeVariant([]monitorStream{{71, map[string]dbus.Variant{
			"position": dbus.MakeVariant(pair{-1280, 0}), "size": dbus.MakeVariant(pair{1280, 720}),
		}}}),
	}), nil
}

func (p *portalFixture) NotifyKeyboardKeysym(dbus.ObjectPath, map[string]dbus.Variant, int32, uint32) *dbus.Error {
	return nil
}
func (p *portalFixture) NotifyPointerMotion(dbus.ObjectPath, map[string]dbus.Variant, float64, float64) *dbus.Error {
	return nil
}
func (p *portalFixture) NotifyPointerMotionAbsolute(_ dbus.ObjectPath, _ map[string]dbus.Variant, node uint32, x, y float64) *dbus.Error {
	p.points <- portalPoint{node, x, y}
	return nil
}
