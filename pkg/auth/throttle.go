package auth

import (
	"sync"
	"time"
)

// Logins throttles failed sign-ins: ten failures for one username or from one
// address inside ten minutes refuse further attempts on that key until the
// window has passed. bcrypt makes each guess slow; this caps how many guesses
// a client gets to make at all.
var Logins = NewThrottle(10, 10*time.Minute)

// Throttle counts failures per key inside a window that opens on the first
// failure and closes window later.
type Throttle struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	failures map[string]*failureWindow
}

// failureWindow is the failures counted for one key since its window opened.
type failureWindow struct {
	count int
	since time.Time
}

// NewThrottle builds a throttle that refuses a key after limit failures
// within window.
func NewThrottle(limit int, window time.Duration) *Throttle {
	return &Throttle{limit: limit, window: window, now: time.Now, failures: make(map[string]*failureWindow)}
}

// Allow reports whether every key is still under the limit. An empty key is
// ignored, so a request with no client address does not share one bucket
// with every other such request.
func (t *Throttle) Allow(keys ...string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, key := range keys {
		if key == "" {
			continue
		}
		w, ok := t.failures[key]
		if !ok {
			continue
		}
		if now.Sub(w.since) >= t.window {
			delete(t.failures, key)
			continue
		}
		if w.count >= t.limit {
			return false
		}
	}
	return true
}

// Fail records one failure on every key.
func (t *Throttle) Fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.failures) > 10000 {
		t.prune(now)
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		w, ok := t.failures[key]
		if !ok || now.Sub(w.since) >= t.window {
			t.failures[key] = &failureWindow{count: 1, since: now}
			continue
		}
		w.count++
	}
}

// Reset clears every key, which a successful sign-in earns.
func (t *Throttle) Reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, key := range keys {
		delete(t.failures, key)
	}
}

// prune drops windows that have closed. The caller holds mu.
func (t *Throttle) prune(now time.Time) {
	for key, w := range t.failures {
		if now.Sub(w.since) >= t.window {
			delete(t.failures, key)
		}
	}
}
