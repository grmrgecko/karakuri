//go:build karakuri_openhab

// Package openhabsuite runs the openHAB connection against the real openHAB
// in compose.yaml. Start it with `make openhab-up`, which writes the API token
// this suite reads, and run with `make test TARGET=openhab`.
package openhabsuite

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baseURL = "http://localhost:19080"

// connect starts a driver against the test openHAB with its token.
func connect(t *testing.T) *openhab.Driver {
	t.Helper()
	raw, err := os.ReadFile("../../build/openhab-token")
	require.NoError(t, err, "run make openhab-up first; it writes build/openhab-token")
	cfg, _ := json.Marshal(openhab.Config{BaseURL: baseURL, Token: strings.TrimSpace(string(raw))})
	built, err := openhab.New(integrations.ConnModel{ID: uuid.New(), Kind: openhab.Kind, Config: cfg}, integrations.Deps{})
	require.NoError(t, err)
	d := built.(*openhab.Driver)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	waitFor(t, d.Connected, "the driver never connected to the test openHAB")
	return d
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		require.True(t, time.Now().Before(deadline), msg)
		time.Sleep(20 * time.Millisecond)
	}
}

// stateOf waits for an item's state in the driver's copy.
func stateOf(t *testing.T, d *openhab.Driver, name, want string) {
	t.Helper()
	waitFor(t, func() bool { it, _ := d.Item(name); return it.State == want },
		name+" never reached "+want)
}

// TestRealOpenHAB connects, lists the items with their locations, commands a
// switch, a dimmer, a blind, and a number through the device mapping, and
// sees openHAB's reported states come back over the event stream.
func TestRealOpenHAB(t *testing.T) {
	d := connect(t)
	assert.NotEmpty(t, d.Version())

	catalog := d.Items()
	require.Contains(t, catalog, "KitchenSwitch")
	assert.Equal(t, "Kitchen", catalog.Locate("KitchenSwitch").LocationLabel)
	assert.Equal(t, "Living room", catalog.Locate("LivingDimmer").LocationLabel, "through the lamp equipment")
	assert.Equal(t, "Living room", catalog.Locate("Volume").LocationLabel, "through its group")
	assert.Equal(t, "Ground floor", catalog.Locate("KitchenSwitch").FloorLabel)
	assert.Equal(t, " °C", openhab.Propose(catalog["KitchenTemp"]).Options["unit"],
		"a dimensioned item has its unit before it has a state")

	ctx := context.Background()
	got := make(chan openhab.ItemChange, 32)
	defer d.SubscribeChanges(func(c openhab.ItemChange) { got <- c })()

	start := time.Now()
	require.NoError(t, d.CommandEntity(ctx, "KitchenSwitch", "on", true))
	stateOf(t, d, "KitchenSwitch", "ON")
	assert.Less(t, time.Since(start), time.Second, "a change arrives within a second (SC-002)")
	stateOf(t, d, "gLights", "ON")

	require.NoError(t, d.CommandEntity(ctx, "LivingDimmer", "brightness", 40.0))
	stateOf(t, d, "LivingDimmer", "40")
	require.NoError(t, d.CommandEntity(ctx, "KitchenBlind", "position", 30.0))
	stateOf(t, d, "KitchenBlind", "70")
	require.NoError(t, d.CommandEntity(ctx, "Volume", "value", 25.0))
	stateOf(t, d, "Volume", "25")

	it, _ := d.Item("KitchenBlind")
	assert.Equal(t, float64(30), openhab.Values(it)["position"], "openHAB's 70 is karakuri's 30")

	vol, _ := d.Item("Volume")
	p := openhab.Propose(vol)
	_, err := controls.CoerceAttribute(p.Kind, p.Options, "value", 70.0)
	assert.Error(t, err, "a volume past the item's maximum is refused before reaching openHAB (SC-004)")

	require.NoError(t, d.SendCommand(ctx, "gLights", "OFF"))
	stateOf(t, d, "KitchenSwitch", "OFF")
	stateOf(t, d, "LivingDimmer", "0")
	stateOf(t, d, "gLights", "OFF")

	err = d.SendCommand(ctx, "FrontDoor", "OPEN")
	require.Error(t, err, "openHAB refuses commands to a contact")
	assert.Contains(t, err.Error(), "400")
}
