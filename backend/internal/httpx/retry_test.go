package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoWithRetry_RetriesTransient5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(context.Background(), srv.Client(), req, RetryOptions{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("want 3 calls, got %d", got)
	}
}

func TestDoWithRetry_Gives4xxBackImmediately(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(context.Background(), srv.Client(), req, RetryOptions{MaxAttempts: 3, BaseDelay: time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("want 1 call, got %d", got)
	}
}

func TestDoWithRetry_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := DoWithRetry(ctx, srv.Client(), req, RetryOptions{MaxAttempts: 5, BaseDelay: time.Second})
	if err == nil {
		t.Fatal("want cancellation error, got nil")
	}
}

func TestDoWithRetry_ExhaustsAndReturnsLastResponse(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(context.Background(), srv.Client(), req, RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("want 502, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("want 2 calls, got %d", got)
	}
}

func TestDoWithRetry_HonoursRetryAfterSeconds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(context.Background(), srv.Client(), req, RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("want 2 calls, got %d", got)
	}
}

func TestDoWithRetry_ObservesOnRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	var events []string
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := DoWithRetry(context.Background(), srv.Client(), req, RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		OnRetry: func(attempt int, reason string) {
			events = append(events, fmt.Sprintf("%d:%s", attempt, reason))
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 onRetry event, got %d: %v", len(events), events)
	}
}
