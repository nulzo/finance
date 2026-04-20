package httpx

import (
	"math"
	"sync"
	"time"
)

// SourceBreaker tracks consecutive failures for a named upstream and
// answers the simple question "should I even try calling this source
// right now?" with exponential backoff between attempts.
//
// It is NOT a full circuit breaker in the Martin Fowler sense (no
// half-open probing, no rolling windows, no thread-counted concurrency
// limits). It just:
//   - records success/failure of each attempt,
//   - computes the next earliest-retry timestamp via exp-backoff,
//   - gates future Ready() calls on the wall clock.
//
// Zero value is usable: a brand-new breaker is always Ready.
//
// Concurrency: methods are safe to call from multiple goroutines. The
// intended usage is one breaker per Source instance wrapped in the
// aggregator, accessed by whichever goroutine is currently trying to
// fetch that source.
type SourceBreaker struct {
	mu           sync.Mutex
	failures     int
	nextAttempt  time.Time
	baseDelay    time.Duration
	maxDelay     time.Duration
	nowFn        func() time.Time
}

// NewSourceBreaker builds a breaker. Zero baseDelay defaults to 5s;
// zero maxDelay defaults to 10 minutes. These are sized for external
// HTTP providers where burst retries are rude but hour-long freezes
// make the data stale.
func NewSourceBreaker(baseDelay, maxDelay time.Duration) *SourceBreaker {
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}
	if maxDelay <= 0 {
		maxDelay = 10 * time.Minute
	}
	return &SourceBreaker{baseDelay: baseDelay, maxDelay: maxDelay}
}

// now returns the current time through the injected clock or
// time.Now(). Tests override nowFn to drive the breaker deterministically.
func (b *SourceBreaker) now() time.Time {
	if b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

// SetClock overrides the time source. Intended for tests.
func (b *SourceBreaker) SetClock(fn func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nowFn = fn
}

// Ready reports whether the breaker will accept a new attempt now.
// Callers should respect this and skip the invocation when false; the
// aggregator still completes (with an empty result from this source)
// and other sources run normally.
func (b *SourceBreaker) Ready() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.now().Before(b.nextAttempt)
}

// NextAttempt returns the earliest time this breaker will be Ready.
// Zero time means "ready right now".
func (b *SourceBreaker) NextAttempt() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextAttempt
}

// Failures returns the current consecutive-failure count.
func (b *SourceBreaker) Failures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// RecordSuccess clears the failure count and releases the gate.
func (b *SourceBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.nextAttempt = time.Time{}
}

// RecordFailure increments the failure count and pushes the next-
// attempt time forward with exponential backoff, capped at maxDelay.
// The backoff is base * 2^(failures-1) — so the first failure waits
// one base, the second two bases, the third four, and so on.
func (b *SourceBreaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	// Clamp the exponent to prevent overflow at very high failure counts.
	exp := b.failures - 1
	if exp > 20 {
		exp = 20
	}
	mult := math.Pow(2, float64(exp))
	delay := time.Duration(float64(b.baseDelay) * mult)
	if delay > b.maxDelay {
		delay = b.maxDelay
	}
	b.nextAttempt = b.now().Add(delay)
}
