package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMediaControlFromFlows covers driving a media control with the existing
// nodes: Control Set pauses it and sets its volume, a skip reaches a Control
// Changed watching command, and a later read holds no command.
func TestMediaControlFromFlows(t *testing.T) {
	setupControls(t)

	player, err := controls.S.Create("Media test", controls.KindMedia, "", nil, false)
	require.NoError(t, err)
	_, err = controls.S.SetAttribute(player.ID, "state", "playing", controls.By(controls.SourceAPI))
	require.NoError(t, err)

	cfg := `{"control":"` + player.ID.String() + `","attribute":"command","ignore_flow_writes":false}`
	watchCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	skips := make(chan *engine.Msg, 4)
	go func() {
		_ = ControlChangedNode{}.Start(watchCtx, newTestCtx(cfg, nil), func(m *engine.Msg) { skips <- m })
	}()
	time.Sleep(50 * time.Millisecond)

	set := func(attribute, value string) error {
		cfg := `{"control":"` + player.ID.String() + `","attribute":"` + attribute + `","source":"value","value":"` + value + `"}`
		_, err := ControlSetNode{}.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": {}})
		return err
	}
	require.NoError(t, set("state", "paused"))
	require.NoError(t, set("volume", "30"))
	require.NoError(t, set("command", "next"))
	assert.Error(t, set("volume", "120"), "a volume past 100 fails the node")

	select {
	case m := <-skips:
		payload := m.Payload.(map[string]any)
		assert.Equal(t, "command", payload["attribute"])
		assert.Equal(t, "next", payload["attribute_value"])
	case <-time.After(2 * time.Second):
		require.FailNow(t, "the skip did not reach Control Changed")
	}

	stored, ok := controls.S.Get(player.ID)
	require.True(t, ok)
	value := stored.Value.(map[string]any)
	assert.Equal(t, "paused", value["state"])
	assert.Equal(t, float64(30), value["volume"])
	assert.NotContains(t, value, "command", "a skip is never stored")
}
