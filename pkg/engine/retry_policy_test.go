package engine

import (
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRetryWaitBounds(t *testing.T) {
	policy := &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 1000, Backoff: 2, Jitter: 0.2, MaxDelayMs: 5000}
	for range 100 {
		delay := retryWait(policy, 1, nil)
		assert.GreaterOrEqual(t, delay, 800*time.Millisecond)
		assert.LessOrEqual(t, delay, 1200*time.Millisecond)
		assert.Equal(t, 3*time.Second, retryWait(policy, 1, &ExecutionError{RetryAfterMS: 3000}))
		assert.Equal(t, 5*time.Second, retryWait(policy, 10, &ExecutionError{RetryAfterMS: 60000}))
	}
}
