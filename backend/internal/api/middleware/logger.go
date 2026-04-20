package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// Logger emits one structured log line per request.
//
// Level is picked from the response status: 5xx -> error, 4xx -> warn,
// otherwise info. When an active OTel span is present the line carries
// trace_id / span_id so logs and traces can be joined.
//
// Callers pass the zerolog.Logger they want to write into. To use the
// process-wide logger installed via platform/logger, pass
// platform/logger.Get().
func Logger(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		var evt *zerolog.Event
		switch {
		case status >= 500:
			evt = log.Error()
		case status >= 400:
			evt = log.Warn()
		default:
			evt = log.Info()
		}

		evt = evt.
			Int("status", status).
			Str("method", c.Request.Method).
			Str("path", path).
			Str("ip", c.ClientIP()).
			Dur("latency", latency)

		if query != "" {
			evt = evt.Str("query", query)
		}
		if ua := c.Request.UserAgent(); ua != "" {
			evt = evt.Str("user_agent", ua)
		}
		if sc := trace.SpanFromContext(c.Request.Context()).SpanContext(); sc.IsValid() {
			evt = evt.Str("trace_id", sc.TraceID().String()).
				Str("span_id", sc.SpanID().String())
		}
		if len(c.Errors) > 0 {
			evt = evt.Str("errors", c.Errors.String())
		}

		evt.Msg(path)
	}
}
