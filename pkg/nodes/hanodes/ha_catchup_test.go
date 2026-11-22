package hanodes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// detailedConn is a connection that tells caught-up changes apart.
type detailedConn struct {
	*fakeConn
	handler chan func(homeassistant.StateChange)
}

func (c *detailedConn) SubscribeStateChangesDetailed(h func(homeassistant.StateChange)) (func(), error) {
	c.handler <- h
	return func() {}, nil
}

// detailedEC serves the detailed connection.
type detailedEC struct {
	*fakeEC
	conn *detailedConn
}

func (e *detailedEC) Connection(string) (any, error) { return e.conn, nil }

// TestStateTriggerCaughtUp covers FR-024: a caught-up change fires once with
// the mark, and a trigger told to ignore caught-up changes does not fire.
func TestStateTriggerCaughtUp(t *testing.T) {
	change := homeassistant.StateChange{EntityID: "binary_sensor.door", CaughtUp: true,
		Old: json.RawMessage(`{"state":"off"}`), New: json.RawMessage(`{"state":"on"}`)}
	for _, tc := range []struct {
		name  string
		cfg   string
		fires bool
	}{
		{"reported with the mark", `{"connection":"c","entity_id":"binary_sensor.door"}`, true},
		{"ignored when asked", `{"connection":"c","entity_id":"binary_sensor.door","ignore_caught_up":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &detailedConn{fakeConn: &fakeConn{}, handler: make(chan func(homeassistant.StateChange), 1)}
			ec := &detailedEC{fakeEC: &fakeEC{cfg: tc.cfg}, conn: conn}
			emits := make(chan *engine.Msg, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = StateNode{}.Start(ctx, ec, func(m *engine.Msg) { emits <- m }) }()
			var h func(homeassistant.StateChange)
			select {
			case h = <-conn.handler:
			case <-time.After(2 * time.Second):
				require.FailNow(t, "the trigger never subscribed")
			}
			h(change)
			select {
			case m := <-emits:
				require.True(t, tc.fires, "fired although caught-up changes are ignored")
				assert.Equal(t, true, m.Payload.(map[string]any)["caught_up"])
				assert.Equal(t, true, m.Meta["caught_up"])
			case <-time.After(150 * time.Millisecond):
				assert.False(t, tc.fires, "a caught-up change never fired")
			}
		})
	}
}
