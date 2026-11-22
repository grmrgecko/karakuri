package autoapi

import "time"

// Clock is the time source the keystroke pacing reads, injectable so a test can
// drive the delays without waiting. Its method set is intentionally identical to
// karakuri's engine clock (pkg/nodeapi.Clock), so an engine clock passes here
// with no adapter; keep the two in step.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production Clock backed by the standard library.
type RealClock struct{}

// Now reports the current time.
func (RealClock) Now() time.Time { return time.Now() }

// After returns a channel that fires after d.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
