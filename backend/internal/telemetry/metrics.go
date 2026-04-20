package telemetry

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// App is the process-wide bundle of app-specific instruments.
//
// All counters/histograms live on this struct so business code can call
// them without depending directly on the OTel API. Every instrument is
// created lazily via initAppMetrics; before Init is called the struct
// falls back to no-op instruments so tests and early-startup code
// paths are safe.
var App = &AppMetrics{}

// AppMetrics is a shared collection of OTel instruments. Methods on
// this struct are goroutine-safe. Field zero-values are replaced on
// the first call to initAppMetrics.
type AppMetrics struct {
	mu sync.RWMutex

	// HTTP — populated even without explicit instrumentation because
	// otelhttp emits the standard http.server.* counters through the
	// registered MeterProvider.

	// Orders
	OrdersSubmitted metric.Int64Counter
	OrdersRejected  metric.Int64Counter
	OrdersFilled    metric.Int64Counter

	// Engine
	EngineTickDuration metric.Float64Histogram
	EngineTickErrors   metric.Int64Counter

	// Ingestion
	CongressFetched metric.Int64Counter
	NewsFetched     metric.Int64Counter
	NewsEnriched    metric.Int64Counter

	// Signals + decisions
	SignalsGenerated metric.Int64Counter
	DecisionsMade    metric.Int64Counter

	// LLM
	LLMCalls       metric.Int64Counter
	LLMLatency     metric.Float64Histogram
	LLMTokens      metric.Int64Counter

	// Broker
	BrokerCalls metric.Int64Counter
}

func initAppMetrics(mp metric.MeterProvider) {
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	m := mp.Meter("github.com/nulzo/trader")

	App.mu.Lock()
	defer App.mu.Unlock()

	App.OrdersSubmitted = mustI64(m.Int64Counter("trader.orders.submitted",
		metric.WithDescription("Orders submitted to the broker."),
		metric.WithUnit("{order}"),
	))
	App.OrdersRejected = mustI64(m.Int64Counter("trader.orders.rejected",
		metric.WithDescription("Orders rejected by risk or broker."),
		metric.WithUnit("{order}"),
	))
	App.OrdersFilled = mustI64(m.Int64Counter("trader.orders.filled",
		metric.WithDescription("Orders that reached filled/partially_filled state."),
		metric.WithUnit("{order}"),
	))

	App.EngineTickDuration = mustF64Hist(m.Float64Histogram("trader.engine.tick.duration",
		metric.WithDescription("Wall-clock time of one engine tick (ingest or decide)."),
		metric.WithUnit("s"),
	))
	App.EngineTickErrors = mustI64(m.Int64Counter("trader.engine.tick.errors",
		metric.WithDescription("Engine ticks that returned an error."),
		metric.WithUnit("{tick}"),
	))

	App.CongressFetched = mustI64(m.Int64Counter("trader.ingest.congress.rows",
		metric.WithDescription("Politician trade rows ingested per source."),
		metric.WithUnit("{row}"),
	))
	App.NewsFetched = mustI64(m.Int64Counter("trader.ingest.news.items",
		metric.WithDescription("News items ingested per source."),
		metric.WithUnit("{item}"),
	))
	App.NewsEnriched = mustI64(m.Int64Counter("trader.ingest.news.enriched",
		metric.WithDescription("News items annotated with LLM sentiment."),
		metric.WithUnit("{item}"),
	))

	App.SignalsGenerated = mustI64(m.Int64Counter("trader.signals.generated",
		metric.WithDescription("Trading signals emitted per strategy kind."),
		metric.WithUnit("{signal}"),
	))
	App.DecisionsMade = mustI64(m.Int64Counter("trader.decisions.made",
		metric.WithDescription("Trade decisions recorded after evaluating signals."),
		metric.WithUnit("{decision}"),
	))

	App.LLMCalls = mustI64(m.Int64Counter("trader.llm.calls",
		metric.WithDescription("LLM API calls by model and outcome."),
		metric.WithUnit("{call}"),
	))
	App.LLMLatency = mustF64Hist(m.Float64Histogram("trader.llm.latency",
		metric.WithDescription("End-to-end latency of LLM calls."),
		metric.WithUnit("s"),
	))
	App.LLMTokens = mustI64(m.Int64Counter("trader.llm.tokens",
		metric.WithDescription("Tokens consumed on LLM calls (prompt + completion)."),
		metric.WithUnit("{token}"),
	))

	App.BrokerCalls = mustI64(m.Int64Counter("trader.broker.calls",
		metric.WithDescription("Broker API invocations by operation and outcome."),
		metric.WithUnit("{call}"),
	))
}

// RecordEngineTick is a small helper used by the engine loop. It emits
// the tick duration and an errors counter with a shared attribute set.
func (a *AppMetrics) RecordEngineTick(ctx context.Context, kind string, start time.Time, err error) {
	if a == nil {
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	attrs := metric.WithAttributes(attribute.String("kind", kind))
	if a.EngineTickDuration != nil {
		a.EngineTickDuration.Record(ctx, time.Since(start).Seconds(), attrs)
	}
	if err != nil && a.EngineTickErrors != nil {
		a.EngineTickErrors.Add(ctx, 1, attrs)
	}
}

// RecordLLMCall is called by the LLM client for every completed call.
func (a *AppMetrics) RecordLLMCall(ctx context.Context, model, outcome string, latency time.Duration, promptTokens, completionTokens int) {
	if a == nil {
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	attrs := metric.WithAttributes(
		attribute.String("model", model),
		attribute.String("outcome", outcome),
	)
	if a.LLMCalls != nil {
		a.LLMCalls.Add(ctx, 1, attrs)
	}
	if a.LLMLatency != nil && latency > 0 {
		a.LLMLatency.Record(ctx, latency.Seconds(), attrs)
	}
	if a.LLMTokens != nil {
		if promptTokens > 0 {
			a.LLMTokens.Add(ctx, int64(promptTokens), metric.WithAttributes(
				attribute.String("model", model),
				attribute.String("kind", "prompt"),
			))
		}
		if completionTokens > 0 {
			a.LLMTokens.Add(ctx, int64(completionTokens), metric.WithAttributes(
				attribute.String("model", model),
				attribute.String("kind", "completion"),
			))
		}
	}
}

// RecordOrder is called from the trading engine after each order attempt.
// outcome is one of: "submitted", "filled", "rejected".
func (a *AppMetrics) RecordOrder(ctx context.Context, outcome, symbol, side string) {
	if a == nil {
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	attrs := metric.WithAttributes(
		attribute.String("symbol", symbol),
		attribute.String("side", side),
	)
	switch outcome {
	case "submitted":
		if a.OrdersSubmitted != nil {
			a.OrdersSubmitted.Add(ctx, 1, attrs)
		}
	case "filled":
		if a.OrdersFilled != nil {
			a.OrdersFilled.Add(ctx, 1, attrs)
		}
	case "rejected":
		if a.OrdersRejected != nil {
			a.OrdersRejected.Add(ctx, 1, attrs)
		}
	}
}

// --------------------------------------------------------------- helpers

func mustI64(c metric.Int64Counter, err error) metric.Int64Counter {
	if err != nil {
		// We never want metric creation to crash the process. Fall
		// back to a noop counter so callers can still call .Add.
		return noopI64{}
	}
	return c
}

func mustF64Hist(h metric.Float64Histogram, err error) metric.Float64Histogram {
	if err != nil {
		return noopF64Hist{}
	}
	return h
}

type noopI64 struct{ metric.Int64Counter }

func (noopI64) Add(context.Context, int64, ...metric.AddOption) {}

type noopF64Hist struct{ metric.Float64Histogram }

func (noopF64Hist) Record(context.Context, float64, ...metric.RecordOption) {}
