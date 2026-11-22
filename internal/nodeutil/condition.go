package nodeutil

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// WaitCondition checks observations until they remain matched for stableFor or
// timeout elapses. Stability describes samples; changes between polls are unseen.
func WaitCondition(ctx context.Context, ec nodeapi.ExecContext, timeout, interval, stableFor time.Duration, observe func(context.Context) (any, bool, error)) (any, bool, error) {
	return WaitConditionWithSignal(ctx, ec, timeout, interval, stableFor, nil, observe)
}

// WaitConditionWithSignal also observes when signal arrives without changing
// the deadline or stability rules. Repeated pending signals may coalesce.
func WaitConditionWithSignal(ctx context.Context, ec nodeapi.ExecContext, timeout, interval, stableFor time.Duration, signal <-chan struct{}, observe func(context.Context) (any, bool, error)) (any, bool, error) {
	if timeout <= 0 || interval <= 0 || stableFor < 0 || stableFor > timeout {
		return nil, false, fmt.Errorf("condition wait needs positive timeout and poll interval, and a stable duration between zero and timeout")
	}
	observationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline := ec.Clock().Now().Add(timeout)
	var since time.Time
	var last any
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		now := ec.Clock().Now()
		if now.After(deadline) {
			return last, false, nil
		}
		value, matched, err := observe(observationCtx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			if errors.Is(observationCtx.Err(), context.DeadlineExceeded) {
				return last, false, nil
			}
			return nil, false, err
		}
		last = value
		now = ec.Clock().Now()
		if matched {
			if since.IsZero() {
				since = now
			}
			if !now.After(deadline) && now.Sub(since) >= stableFor {
				return last, true, nil
			}
		} else {
			since = time.Time{}
		}
		if !now.Before(deadline) {
			return last, false, nil
		}
		wait := interval
		if left := deadline.Sub(now); left < wait {
			wait = left
		}
		if !since.IsZero() {
			if left := since.Add(stableFor).Sub(now); left > 0 && left < wait {
				wait = left
			}
		}
		var parkErr error
		if parker, ok := ec.(nodeapi.SignalParker); ok && signal != nil {
			parkErr = parker.ParkUntil(ctx, wait, signal)
		} else {
			parkErr = ec.Park(ctx, wait)
		}
		if parkErr != nil {
			return nil, false, parkErr
		}
	}
}
