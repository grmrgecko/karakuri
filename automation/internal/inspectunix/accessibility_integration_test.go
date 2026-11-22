//go:build karakuri_integration && !windows && !darwin

package inspectunix

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestActionNames uses the AT-SPI Action XML's signed indices and NActions property.
// The synthetic provider omits GetActions, which aborts Rocky 9's at-spi2-atk 2.38.
func TestActionNames(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	daemon := exec.CommandContext(ctx, "dbus-daemon", "--session", "--nofork", "--print-address=1")
	stdout, err := daemon.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, daemon.Start())
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	address, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	provider, err := dbus.Connect(strings.TrimSpace(address))
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Close() })
	client, err := dbus.Connect(strings.TrimSpace(address))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ref := reference{provider.Names()[0], "/fixture"}
	fixture := actionFixture{}
	for _, iface := range []string{accessible, "org.a11y.atspi.Action", "org.freedesktop.DBus.Properties"} {
		require.NoError(t, provider.Export(fixture, ref.Path, iface))
	}
	element := readElement(ctx, client, ref)
	assert.Equal(t, []string{"", "click"}, element.Actions, "a failed name read must not shift DoAction indices")
}

type actionFixture struct{}

func (actionFixture) GetInterfaces() ([]string, *dbus.Error) {
	return []string{"org.a11y.atspi.Action"}, nil
}
func (actionFixture) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != "org.a11y.atspi.Action" || property != "NActions" {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
	}
	return dbus.MakeVariant(int32(2)), nil
}
func (actionFixture) GetName(index int32) (string, *dbus.Error) {
	if index != 1 {
		return "", dbus.NewError("org.freedesktop.DBus.Error.Failed", nil)
	}
	return "click", nil
}
