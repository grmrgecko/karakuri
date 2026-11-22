package engine_test

import (
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteOnceRunsForTheFirstPartOnly covers the per-node setting against a
// real fan-out, which is the only place it means anything: a split delivers one
// message per part and schedules one instance of every downstream node per
// part, so "once" has to hold across instances rather than within one.
//
// The case it exists for is work that is per-run rather than per-part — one
// notification about a batch, one call to an API that needs asking once — where
// the alternative is a limit node in front of everything expensive.
func TestExecuteOnceRunsForTheFirstPartOnly(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	once := &collector{}
	every := &collector{}
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		fanStub("split", 4),
		once.node("once"),
		every.node("every"),
	)

	// Both sinks hang off the same fan-out, so the only difference between them
	// is the flag.
	g := graph(
		[]flow.Node{
			gnode("t", "trigger"),
			gnode("s", "split"),
			{ID: "o", Type: "once", ExecuteOnce: true},
			gnode("e", "every"),
		},
		[]flow.Edge{
			gedge("t", "out", "s", "in"),
			gedge("s", "out", "o", "in"),
			gedge("s", "out", "e", "in"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "dropping the extra parts is not a failure: %s", snap.Error)

	assert.Len(t, once.payloads(), 1,
		"the node runs for whichever part arrives first and drops the other three")
	assert.Equal(t, []int{0, 1, 2, 3}, every.payloads(),
		"the sibling without the flag still sees every part, so the setting is per node rather than per graph")

	// Dropping a part looks like a filter dropping a message: one success, no
	// emission. Reporting them as failures or skips would put four red nodes on
	// the canvas for a run that did what it was told.
	succeeded := 0
	for _, state := range pub.nodeStates()["o"] {
		if state == "succeeded" {
			succeeded++
		}
	}
	assert.Equal(t, 4, succeeded,
		"every instance still reports success; three of them simply emitted nothing")
}
