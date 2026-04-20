package httpx_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nulzo/trader/internal/httpx"
)

func TestSourceBreaker_ReadyUntilFailure(t *testing.T) {
	t.Parallel()
	b := httpx.NewSourceBreaker(time.Second, time.Minute)
	assert.True(t, b.Ready(), "fresh breaker should be ready")
	assert.Equal(t, 0, b.Failures())
}

func TestSourceBreaker_FailureBlocksUntilDelay(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := httpx.NewSourceBreaker(time.Second, time.Minute)
	b.SetClock(func() time.Time { return now })

	b.RecordFailure()
	assert.Equal(t, 1, b.Failures())
	assert.False(t, b.Ready(), "breaker should be blocked immediately after a failure")

	now = now.Add(500 * time.Millisecond)
	assert.False(t, b.Ready(), "still inside 1s window")

	now = now.Add(600 * time.Millisecond)
	assert.True(t, b.Ready(), "past the 1s base delay; should be ready again")
}

// Backoff grows 1s, 2s, 4s, 8s ... and is clamped at maxDelay.
func TestSourceBreaker_ExponentialAndClamp(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := httpx.NewSourceBreaker(time.Second, 10*time.Second)
	b.SetClock(func() time.Time { return now })

	expect := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, want := range expect {
		b.RecordFailure()
		got := b.NextAttempt().Sub(now)
		assert.Equal(t, want, got, "delay after failure #%d", i+1)
	}
}

func TestSourceBreaker_SuccessResets(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	b := httpx.NewSourceBreaker(time.Second, time.Minute)
	b.SetClock(func() time.Time { return now })

	b.RecordFailure()
	b.RecordFailure()
	assert.False(t, b.Ready())
	b.RecordSuccess()
	assert.True(t, b.Ready())
	assert.Equal(t, 0, b.Failures())
	assert.True(t, b.NextAttempt().IsZero())
}
