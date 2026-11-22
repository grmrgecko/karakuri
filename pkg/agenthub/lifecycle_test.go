package agenthub

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestDropSessionCannotRemoveAReplacement protects reconnects from an older
// transport closing late. It also pins the disconnect event to the session
// that actually left the live map.
func TestDropSessionCannotRemoveAReplacement(t *testing.T) {
	id := uuid.New()
	oldDetached, currentDetached := false, false
	old := &liveSession{name: "old", detach: func() { oldDetached = true }}
	current := &liveSession{name: "current", detach: func() { currentDetached = true }}
	hub := &Hub{log: zap.NewNop(), sessions: map[uuid.UUID]*liveSession{id: current}, eventSub: make(map[uint64]chan AgentEvent)}
	events, cancel := hub.subscribe()
	defer cancel()

	hub.dropSession(id, old)
	assert.False(t, oldDetached)
	assert.False(t, currentDetached)
	assert.Same(t, current, hub.sessions[id])

	hub.dropSession(id, current)
	assert.True(t, currentDetached)
	assert.NotContains(t, hub.sessions, id)
	event := <-events
	assert.Equal(t, agentDisconnected, event.State)
	assert.Equal(t, "current", event.AgentName)
}
