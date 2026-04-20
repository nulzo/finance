// Package api exposes the application over HTTP using Gin. Handlers
// are small adapters over services / repositories; they never carry
// business logic beyond request parsing and response shaping.
//
// The HTTP surface is assembled in three files:
//
//   - server.go  — this file: dependencies, Server struct, New()
//     constructor (middleware wiring only), and shared response
//     helpers.
//   - routes.go  — route registration (SetupRoutes).
//   - handlers.go — per-endpoint handler methods.
//
// The split mirrors the sibling model-router-api project so the layout
// is familiar when hopping between repositories.
package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/nulzo/trader/internal/api/middleware"
	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/engine"
	"github.com/nulzo/trader/internal/providers/market"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
)

// Deps is the api's runtime context.
type Deps struct {
	Store       *storage.Store
	Broker      broker.Broker
	Market      market.QuoteProvider
	Engine      *engine.Engine
	Risk        *risk.Engine
	PortfolioID string
	APIToken    string
	Log         zerolog.Logger
	Version     string
	BuildCommit string

	// ServiceName is surfaced on OTel spans emitted by the Gin router.
	// Empty falls back to "trader".
	ServiceName string
	// MetricsHandler, when non-nil, is mounted at GET /metrics for
	// Prometheus scraping. When nil the endpoint returns 501.
	MetricsHandler http.Handler
	// ReadinessCheck, when set, is invoked by /readyz. A nil error
	// means the service is ready to accept traffic. Typical
	// implementation pings the database.
	ReadinessCheck func(context.Context) error
}

// Server is the HTTP handler root.
type Server struct {
	Deps   Deps
	Router *gin.Engine
}

// New constructs a Server with middleware wired and routes mounted.
//
// Middleware order (first runs outermost):
//
//  1. Recovery       — panic -> 500 with stack captured in a log field.
//  2. Tracing        — OTel Gin instrumentation (must run before Logger).
//  3. Logger         — structured request log with trace/span ids.
//  4. CORS           — permissive by default; tighten via proxy in prod.
//  5. ErrorHandler   — converts c.Error(...) values to JSON responses
//                      using the app's domain error -> status mapping.
//
// Auth is installed per-route inside SetupRoutes (on /v1 only).
func New(d Deps) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	svc := d.ServiceName
	if svc == "" {
		svc = "trader"
	}

	r.Use(
		middleware.Recovery(d.Log),
		middleware.Tracing(svc),
		middleware.Logger(d.Log),
		middleware.CORS(),
		middleware.ErrorHandler(d.Log),
	)

	s := &Server{Deps: d, Router: r}
	s.SetupRoutes()
	return s
}
