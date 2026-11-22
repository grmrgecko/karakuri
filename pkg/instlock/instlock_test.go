package instlock_test

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/instlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAcquireRefusesASecondHolder covers what restore relies on: while one
// holder has the lock a second is refused, and releasing it lets the next in.
func TestAcquireRefusesASecondHolder(t *testing.T) {
	dir := t.TempDir()
	release, err := instlock.Acquire(dir)
	require.NoError(t, err)

	_, err = instlock.Acquire(dir)
	assert.ErrorIs(t, err, instlock.ErrHeld, "a second holder is refused, even in the same process")

	release()
	again, err := instlock.Acquire(dir)
	require.NoError(t, err, "a released lock can be taken again")
	again()
}
