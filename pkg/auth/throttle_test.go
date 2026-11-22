package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestThrottleRefusesAtTheLimitAndReleasesAfterTheWindow pins the sign-in
// policy: the failure that reaches the limit blocks both the username and the
// address it came from, other keys are untouched, the block lifts once the
// window has passed since the first failure, and a successful sign-in clears
// the count.
func TestThrottleRefusesAtTheLimitAndReleasesAfterTheWindow(t *testing.T) {
	clock := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	th := NewThrottle(3, time.Minute)
	th.now = func() time.Time { return clock }

	require.True(t, th.Allow("user:a", "ip:1"), "a fresh key is allowed")
	th.Fail("user:a", "ip:1")
	th.Fail("user:a", "ip:1")
	assert.True(t, th.Allow("user:a", "ip:1"), "two failures of three do not block")
	th.Fail("user:a", "ip:1")
	assert.False(t, th.Allow("user:a"), "the third failure blocks the username")
	assert.False(t, th.Allow("ip:1"), "and the address it came from")
	assert.True(t, th.Allow("user:b", ""), "another username, and an empty key, are unaffected")

	clock = clock.Add(time.Minute)
	assert.True(t, th.Allow("user:a", "ip:1"), "the block lifts once the window has passed")

	th.Fail("user:a")
	th.Fail("user:a")
	th.Fail("user:a")
	th.Reset("user:a")
	assert.True(t, th.Allow("user:a"), "a successful sign-in clears the count")
}
