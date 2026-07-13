package bond

import (
	"math"
	"math/rand/v2"
	"time"
)

// RetryPolicy decides whether and how long to wait before retrying a failed
// model invocation. The agent loop consults this policy when the provider
// returns an error during streaming.
type RetryPolicy interface {
	// ShouldRetry reports whether the error is retryable and, if so, how long
	// to wait before the next attempt. attempt is 0-indexed (first retry is
	// attempt 0, second is attempt 1, etc.). Return (0, false) to stop retrying.
	ShouldRetry(err error, attempt int) (backoff time.Duration, retry bool)
}

// ExponentialBackoff is a [RetryPolicy] that uses exponential backoff with
// optional jitter. It retries all errors up to MaxAttempts times.
//
// The delay for attempt n is: min(BaseDelay * 2^n, MaxDelay), with random
// jitter applied if Jitter is true.
type ExponentialBackoff struct {
	// MaxAttempts is the maximum number of retry attempts. Zero means no retries.
	MaxAttempts int
	// BaseDelay is the initial delay before the first retry. Defaults to 1s.
	BaseDelay time.Duration
	// MaxDelay caps the backoff duration. Defaults to 30s.
	MaxDelay time.Duration
	// Jitter adds randomness to the delay to avoid thundering herd.
	Jitter bool
	// ShouldRetryFunc optionally classifies errors. If nil, all errors are
	// considered retryable. Return false to treat an error as terminal.
	ShouldRetryFunc func(err error) bool
}

// NewExponentialBackoff creates an [ExponentialBackoff] with sensible defaults:
// 3 max attempts, 1s base delay, 30s max delay, jitter enabled, all errors retryable.
func NewExponentialBackoff() *ExponentialBackoff {
	return &ExponentialBackoff{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
		Jitter:      true,
	}
}

// ShouldRetry implements [RetryPolicy].
func (e *ExponentialBackoff) ShouldRetry(err error, attempt int) (time.Duration, bool) {
	if attempt >= e.MaxAttempts {
		return 0, false
	}

	if e.ShouldRetryFunc != nil && !e.ShouldRetryFunc(err) {
		return 0, false
	}

	baseDelay := e.BaseDelay
	if baseDelay == 0 {
		baseDelay = 1 * time.Second
	}
	maxDelay := e.MaxDelay
	if maxDelay == 0 {
		maxDelay = 30 * time.Second
	}

	delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt)))
	if delay > maxDelay {
		delay = maxDelay
	}

	if e.Jitter {
		delay = time.Duration(rand.Int64N(int64(delay) + 1))
	}

	return delay, true
}

// Verify interface compliance.
var _ RetryPolicy = (*ExponentialBackoff)(nil)
