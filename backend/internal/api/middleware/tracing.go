package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// Tracing returns the OpenTelemetry Gin instrumentation wired up for
// serviceName. Health / metrics probes are filtered out — they are
// noisy and carry no business value.
//
// Must run BEFORE Logger() so request handlers and the request-log
// line both see a valid span context.
func Tracing(serviceName string) gin.HandlerFunc {
	return otelgin.Middleware(serviceName,
		otelgin.WithFilter(func(req *http.Request) bool {
			switch req.URL.Path {
			case "/health", "/healthz", "/livez", "/readyz", "/metrics":
				return false
			}
			return true
		}),
	)
}
