package core

import (
	"context"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConditionWaitRoutesObservedHoldAndDeadline establishes matched versus
// timeout at a fake-clock deadline without spending a worker on sleep.
func TestConditionWaitRoutesObservedHoldAndDeadline(t *testing.T) {
	for _, tc := range []struct{ name, condition, port string }{
		{"held true", "payload == true", "matched"},
		{"false through deadline", "payload == false", "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := engine.NewFakeClock(time.Now())
			ec := newTestCtx(`{"condition":"`+tc.condition+`","stable_for":"2s","timeout":"2s","poll_interval":"1s"}`, fc)
			msg := &engine.Msg{Payload: true, Topic: "room", Meta: map[string]any{"id": "keep"}}
			run := runAsync(ConditionWaitNode{}, ec, map[string]*engine.Msg{"in": msg})
			for calls := 1; calls <= 2; calls++ {
				require.True(t, fc.BlockUntilAfterCalls(calls, time.Second))
				fc.Advance(time.Second)
			}
			res := run.wait(t)
			got := only(res.out, tc.port)
			require.NotNil(t, got)
			assert.Equal(t, msg.Payload, got.Payload, "wait outcomes must preserve the original payload")
			assert.Equal(t, "keep", got.Meta["id"], "correlation metadata survives the wait")
			assert.Equal(t, true, got.Meta["condition_value"], "the last observation is available for inspection")
		})
	}
}

// TestConditionWaitCancellation prevents a downstream action after a parked
// condition run is cancelled.
func TestConditionWaitCancellation(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	ec := newTestCtx(`{"condition":"false","timeout":"1m"}`, fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan nodeOutcome, 1)
	go func() {
		out, err := (ConditionWaitNode{}).Execute(ctx, ec, map[string]*engine.Msg{"in": {Payload: "keep"}})
		done <- nodeOutcome{out: out, err: err}
	}()
	require.True(t, fc.BlockUntilAfterCalls(1, time.Second))
	cancel()
	select {
	case res := <-done:
		assert.ErrorIs(t, res.err, context.Canceled)
		assert.Empty(t, res.out)
	case <-time.After(time.Second):
		require.FailNow(t, "a cancelled condition wait must return")
	}
}
