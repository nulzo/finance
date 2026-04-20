package telemetry_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/telemetry"
)

// Init must succeed with no OTLP endpoint and expose a working
// Prometheus handler. This is the "local dev" code path.
func TestInit_NoOTLPEndpoint_PrometheusStillWorks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provs, shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:    "trader-test",
		ServiceVersion: "0.0.0-test",
		Environment:    "test",
	})
	require.NoError(t, err)
	defer func() { _ = shutdown(context.Background()) }()

	assert.False(t, provs.OTLPEnabled, "OTLP should stay disabled when endpoint is empty")
	require.NotNil(t, provs.PromHandler)

	// Scrape /metrics via the handler to prove it serves something.
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	provs.PromHandler.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	// Prom exposition format always contains at least one HELP line.
	assert.Contains(t, string(body), "# HELP")
}

// App-level metrics must be registered and usable after Init without
// panicking even when OTLP is disabled.
func TestInit_AppMetricsUsable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, shutdown, err := telemetry.Init(ctx, telemetry.Config{ServiceName: "trader-test"})
	require.NoError(t, err)
	defer func() { _ = shutdown(context.Background()) }()

	// These should all be safe to call. They'll no-op into the
	// Prometheus registry but must not panic or nil-deref.
	telemetry.App.RecordEngineTick(ctx, "ingest", time.Now(), nil)
	telemetry.App.RecordOrder(ctx, "submitted", "AAPL", "buy")
	telemetry.App.RecordLLMCall(ctx, "gpt-4o-mini", "ok", 250*time.Millisecond, 100, 50)
}

// stripScheme is unexported; we assert behaviour through Init by
// supplying a URL with a scheme and ensuring initialisation doesn't
// fail. The real check for scheme trimming lives in the
// otlptracegrpc.New timeout path, which returns within the context we
// pass below.
func TestInit_OTLPEndpointWithSchemeIsTolerated(t *testing.T) {
	// Intentionally short timeout: we want Init to accept the URL
	// shape and return promptly when the collector isn't reachable.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  "trader-test",
		OTLPEndpoint: "http://localhost:4317", // scheme gets stripped internally
	})
	// Either init succeeds (grpc is lazily-connected) or fails with
	// a deadline-exceeded style error — both are fine as long as the
	// binary doesn't crash on URL parsing. Assert no panic + cleanup.
	if shutdown != nil {
		_ = shutdown(context.Background())
	}
	if err != nil {
		// Must not be a "parse URL" / "invalid endpoint" style error.
		assert.NotContains(t, strings.ToLower(err.Error()), "parse")
		assert.NotContains(t, strings.ToLower(err.Error()), "invalid")
	}
}
