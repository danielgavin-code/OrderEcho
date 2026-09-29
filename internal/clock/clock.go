// Package clock provides the only source of time the session core may use.
//
// The session core never calls time.Now(); it reads an injected Clock so tests
// can drive time deterministically with FakeClock.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time, always in UTC.
type Clock interface {
	Now() time.Time
}

// SystemClock is wall-clock time in UTC.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FakeClock is a manually advanced clock for tests.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a FakeClock starting at start (UTC). A zero start means
// 2026-01-01T00:00:00Z.
func NewFake(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &FakeClock{now: start.UTC()}
}

// Now returns the fake current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and returns the new time.
func (c *FakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// Set moves the clock to t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}
