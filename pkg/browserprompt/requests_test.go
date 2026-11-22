package browserprompt

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPromptExpiryBoundary ensures an answer at the deadline cannot revive a
// timed-out run while an answer immediately before it is retained.
func TestPromptExpiryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advance  time.Duration
		answered bool
	}{
		{"before deadline", time.Second - time.Nanosecond, true},
		{"at deadline", time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewManager()
			now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
			manager.now = func() time.Time { return now }
			token, pending, err := manager.Create(context.Background(), flow.BrowserPromptConfig{Buttons: flow.DefaultPromptButtons()}, time.Second)
			require.NoError(t, err)
			t.Cleanup(func() { manager.Release(token) })
			now = now.Add(tc.advance)
			err = manager.Answer(token, "stop", "")
			if tc.answered {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrGone)
			}
			answer, err := pending.Result()
			if tc.answered {
				require.NoError(t, err)
				assert.Equal(t, "stop", answer.ID)
			} else {
				assert.ErrorContains(t, err, "timed out")
				assert.Nil(t, answer)
			}
		})
	}
}
