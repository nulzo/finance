// Package httpx provides small, dependency-free helpers for working with
// outbound HTTP clients. The primary export is DoWithRetry: a thin wrapper
// around http.Client.Do that retries on transient network errors and
// 5xx/429 responses with exponential backoff.
//
// Upstream providers the trader ingests from (Quiver Quantitative, Finnhub,
// CapitolTrades, assorted RSS feeds) are all public third-party services
// with periodic transient failures. A one-shot request is brittle: a single
// blip drops an entire ingestion cycle and starves downstream strategies.
// Wrapping the outbound call in a bounded retry loop keeps the happy path
// fast while absorbing the noise.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// RetryOptions tunes the retry behaviour. The zero value is usable and
// picks sensible defaults (MaxAttempts=3, BaseDelay=250ms, MaxDelay=2s,
// full jitter).
type RetryOptions struct {
	// MaxAttempts is the total number of attempts including the first.
	// Must be >= 1. Defaults to 3 when <= 0.
	MaxAttempts int

	// BaseDelay is the initial backoff between attempts. Defaults to
	// 250ms when <= 0.
	BaseDelay time.Duration

	// MaxDelay caps a single backoff window. Defaults to 2s when <= 0.
	MaxDelay time.Duration

	// OnRetry is an optional observer called before each retry with the
	// attempt number (1-indexed) and the reason. Useful for logging.
	OnRetry func(attempt int, reason string)
}

func (o RetryOptions) normalise() RetryOptions {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = 250 * time.Millisecond
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = 2 * time.Second
	}
	return o
}

// DoWithRetry issues req using client and retries transient failures. The
// caller owns req; DoWithRetry clones it for each attempt so the body can
// safely be read on retry. For requests with bodies, pass a GetBody on the
// request (net/http sets this automatically for bytes.Buffer / Reader
// bodies created via http.NewRequest).
//
// A transient failure is one of:
//   - any network error from client.Do (dial/timeout/reset)
//   - HTTP 429 Too Many Requests
//   - HTTP 5xx
//
// 4xx (other than 429) are returned immediately so callers don't hammer
// endpoints for auth or schema issues.
//
// The returned response body (if non-nil) belongs to the caller and must
// be closed. On retry, the intermediate response bodies are drained and
// closed automatically.
func DoWithRetry(ctx context.Context, client *http.Client, req *http.Request, opts RetryOptions) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	opts = opts.normalise()

	var lastErr error
	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("%w (last attempt: %v)", err, lastErr)
			}
			return nil, err
		}

		tryReq := req.Clone(ctx)
		// Clone preserves Body for memory-backed bodies; for reader-backed
		// ones, GetBody is required to rewind. If the caller didn't set
		// one and the body was already consumed, the retry is effectively
		// skipped – but most callers here are GETs with no body.
		if req.Body != nil && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("httpx: rewind body: %w", err)
			}
			tryReq.Body = body
		}

		resp, err := client.Do(tryReq)
		if err != nil {
			lastErr = err
			if !shouldRetryErr(err) || attempt == opts.MaxAttempts {
				return nil, err
			}
			if opts.OnRetry != nil {
				opts.OnRetry(attempt, err.Error())
			}
			if sleepErr := sleep(ctx, backoff(attempt, opts)); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}

		if !shouldRetryStatus(resp.StatusCode) || attempt == opts.MaxAttempts {
			return resp, nil
		}
		// Drain + close so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		lastErr = fmt.Errorf("http %d %s", resp.StatusCode, resp.Status)
		if opts.OnRetry != nil {
			opts.OnRetry(attempt, lastErr.Error())
		}
		// Honour Retry-After when provided.
		wait := retryAfter(resp.Header.Get("Retry-After"))
		if wait <= 0 {
			wait = backoff(attempt, opts)
		}
		if sleepErr := sleep(ctx, wait); sleepErr != nil {
			return nil, sleepErr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("httpx: retry exhausted without result")
	}
	return nil, lastErr
}

func shouldRetryStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// shouldRetryErr returns true for transport-level errors. We deliberately
// treat every non-nil error as retryable except explicit context
// cancellation. Upstream providers frequently return opaque errors
// (connection resets, TLS handshake timeouts) that don't satisfy the
// net.Error interface in obvious ways.
func shouldRetryErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func backoff(attempt int, opts RetryOptions) time.Duration {
	// Full jitter: sleep ∈ [0, min(MaxDelay, BaseDelay*2^(attempt-1))]
	exp := opts.BaseDelay << (attempt - 1)
	if exp <= 0 || exp > opts.MaxDelay {
		exp = opts.MaxDelay
	}
	if exp <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(exp)))
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func retryAfter(h string) time.Duration {
	h = firstNonEmpty(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func firstNonEmpty(s string) string {
	for _, r := range s {
		if r != ' ' && r != '\t' {
			return s
		}
	}
	return ""
}
