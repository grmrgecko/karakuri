package connmgr

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusWatchBoundsAndGeneration preserves ordered snapshot/watch delivery,
// bounded memory under a stalled consumer, and exclusion of retired callbacks.
func TestStatusWatchBoundsAndGeneration(t *testing.T) {
	mgr := New(nil, nil)
	t.Cleanup(mgr.Stop)
	id := uuid.New()
	mgr.StopConnection(id)
	initial, found, updates, unsubscribe := mgr.WatchStatus(id)
	t.Cleanup(unsubscribe)
	require.True(t, found)
	assert.Equal(t, integrations.StateDisabled, initial.State)
	assert.False(t, initial.Since.IsZero(), "manager-created statuses need a state-entry timestamp")
	for i := 0; i < 70; i++ {
		mgr.publishStatusFor(id, 1, integrations.Status{State: integrations.StateError, Error: fmt.Sprint(i)})
	}
	assert.Len(t, updates, 64, "a stalled trigger cannot accumulate unbounded lifecycle reports")
	first := <-updates
	assert.Equal(t, "6", first.Error, "overflow keeps the newest 64 reports in order")
	var last integrations.Status
	for len(updates) > 0 {
		last = <-updates
	}
	assert.Equal(t, "69", last.Error)
	assert.Equal(t, first.Since, last.Since, "error detail changes do not restart the state's age")
	retained, found := mgr.Status(id)
	require.True(t, found)
	assert.Equal(t, last, retained, "the initial snapshot for the next watcher is the last retained report")
	mgr.StopConnection(id)
	disabled := <-updates
	mgr.publishStatusFor(id, 1, integrations.Status{State: integrations.StateConnected, Since: time.Now()})
	current, found := mgr.Status(id)
	require.True(t, found)
	assert.Equal(t, disabled, current, "a retired driver cannot report connected after deliberate disablement")
	assert.Empty(t, updates)
	unsubscribe()
	_, open := <-updates
	assert.False(t, open, "unregistering must close the watcher exactly once")
	unsubscribe()
}
