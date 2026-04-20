// Package telemetry wires OpenTelemetry providers (traces, metrics)
// and exposes app-level metric instruments.
//
// The structured *logger* now lives in internal/platform/logger. This
// file keeps only the trace-aware helpers that pair a zerolog.Logger
// with an active OTel span so log lines can be joined to traces.
package telemetry

import (
	"context"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// LogWithTrace returns a child logger carrying the active span's
// trace_id / span_id fields when a span is present in ctx. When no
// span is active the input logger is returned unchanged.
//
// Use this in request handlers and other per-request code paths so
// every log line can be joined against traces in your backend:
//
//	log := telemetry.LogWithTrace(ctx, s.log)
//	log.Info().Msg("hello")  // emits trace_id / span_id
func LogWithTrace(ctx context.Context, log zerolog.Logger) zerolog.Logger {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return log
	}
	return log.With().
		Str("trace_id", sc.TraceID().String()).
		Str("span_id", sc.SpanID().String()).
		Logger()
}

// TraceHook is a zerolog hook that injects trace_id / span_id on every
// event logged through a context-aware API. Currently unused (we
// prefer the explicit LogWithTrace helper to avoid silently mutating
// logs that originate outside a span), but kept here because some
// callers want it available.
type TraceHook struct{ Ctx context.Context }

// Run implements zerolog.Hook.
func (h TraceHook) Run(e *zerolog.Event, _ zerolog.Level, _ string) {
	if h.Ctx == nil {
		return
	}
	sc := trace.SpanFromContext(h.Ctx).SpanContext()
	if !sc.IsValid() {
		return
	}
	e.Str("trace_id", sc.TraceID().String()).
		Str("span_id", sc.SpanID().String())
}
