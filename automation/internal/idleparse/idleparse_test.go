package idleparse

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIdleConversions pins the rounding: both scales report whole tenths of a
// second, so a presence comparison in a flow is not defeated by jitter.
func TestIdleConversions(t *testing.T) {
	assert.InDelta(t, 4.8, FromNanos(4823000000), 0.001, "4823000000ns is 4.8 seconds, rounded to a tenth")
	assert.InDelta(t, 4.8, FromMillis(4823), 0.001, "4823ms is 4.8 seconds, rounded to a tenth")
	assert.Zero(t, FromMillis(99), "under a tenth of a second reads as zero")
}
