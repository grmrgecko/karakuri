package nodeapi

import (
	"sort"
	"sync"
	"time"
)

// Clock abstracts time for the engine and nodes so tests can inject a fake.
// Engine and node code must never call time.Sleep or time.After directly.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock implements Clock using the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// fakeTimer is one pending waiter registered through FakeClock.After.
type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

// FakeClock is a deterministic Clock for tests. Time only moves when Advance
// is called; due timers are delivered synchronously inside Advance. Exported
// from the engine package so node tests can use it too.
type FakeClock struct {
	mu         sync.Mutex
	now        time.Time
	waiters    []fakeTimer
	afterCalls []time.Duration
}

// NewFakeClock returns a FakeClock starting at the given time.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the fake current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After registers a waiter that fires once Advance moves time past the
// deadline. Non-positive durations fire immediately.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.afterCalls = append(c.afterCalls, d)
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeTimer{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the fake time forward and delivers every timer whose deadline
// has been reached, in deadline order.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []fakeTimer
	var remaining []fakeTimer
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			due = append(due, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()

	sort.Slice(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, w := range due {
		w.ch <- w.at
	}
}

// AfterCalls returns a copy of every duration passed to After so far.
func (c *FakeClock) AfterCalls() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.afterCalls))
	copy(out, c.afterCalls)
	return out
}

// PendingWaiters returns the number of timers not yet delivered.
func (c *FakeClock) PendingWaiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// BlockUntilAfterCalls waits until at least n After calls have been made,
// polling briefly. Test synchronization helper only; returns false on timeout.
func (c *FakeClock) BlockUntilAfterCalls(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		count := len(c.afterCalls)
		c.mu.Unlock()
		if count >= n {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}
