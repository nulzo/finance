package api

import "github.com/nulzo/trader/internal/api/middleware"

// SetupRoutes mounts every HTTP endpoint on s.Router.
//
// Public endpoints (health / metrics / version) deliberately sit
// outside /v1 so Kubernetes probes and Prometheus can scrape them
// without a Bearer token. Business endpoints live under /v1 and are
// protected by middleware.Auth when Deps.APIToken is non-empty.
func (s *Server) SetupRoutes() {
	r := s.Router

	// Unauthenticated probes + metrics.
	r.GET("/health", s.health)   // legacy alias
	r.GET("/healthz", s.health)  // preferred k8s-style alias
	r.GET("/livez", s.livez)     // liveness: process is up
	r.GET("/readyz", s.readyz)   // readiness: deps (DB) are usable
	r.GET("/metrics", s.metrics) // Prometheus scrape endpoint

	// Version is cheap and useful for diagnostics; keep it outside
	// auth so dashboards can probe which build is running.
	r.GET("/v1/version", s.version)

	v1 := r.Group("/v1")
	// Auth resolves the token lazily from Deps so the value can change
	// at runtime (tests mutate Deps.APIToken after construction).
	v1.Use(middleware.Auth(func() string { return s.Deps.APIToken }))

	// Portfolio management
	v1.GET("/portfolios", s.listPortfolios)
	v1.GET("/portfolios/:id", s.getPortfolio)
	v1.POST("/portfolios/:id/deposit", s.deposit)
	v1.POST("/portfolios/:id/withdraw", s.withdraw)
	v1.GET("/portfolios/:id/positions", s.listPositions)
	v1.GET("/portfolios/:id/orders", s.listOrders)
	v1.POST("/portfolios/:id/orders", s.createOrder)
	v1.GET("/portfolios/:id/cooldowns", s.listCooldowns)
	v1.DELETE("/portfolios/:id/cooldowns/:symbol", s.clearCooldown)
	v1.GET("/portfolios/:id/rejections", s.listRejections)
	v1.GET("/portfolios/:id/pnl", s.pnlSeries)
	// Wave 5 analytics: equity curve, per-position unrealised,
	// header summary for the Overview/Analytics pages.
	v1.GET("/portfolios/:id/equity", s.equityLive)
	v1.GET("/portfolios/:id/equity/history", s.equityHistory)
	v1.GET("/portfolios/:id/positions/pnl", s.positionsPnL)
	v1.GET("/portfolios/:id/analytics/summary", s.analyticsSummaryHandler)

	// Congress tracking
	v1.GET("/politicians", s.listPoliticians)
	v1.POST("/politicians", s.upsertPolitician)
	v1.GET("/politician-trades", s.listPoliticianTrades)

	// Wave 4 alternative data. Each dataset exposes a `recent`-style
	// GET that the frontend "Intelligence" tabs consume. Read-only;
	// ingestion lives in the engine.
	v1.GET("/insiders", s.listInsiders)
	v1.GET("/social", s.listSocial)
	v1.GET("/lobbying", s.listLobbying)
	v1.GET("/contracts", s.listContracts)
	v1.GET("/short-volume/:symbol", s.listShortVolume)

	// News + signals + decisions
	v1.GET("/news", s.listNews)
	v1.GET("/signals", s.listSignals)
	v1.GET("/decisions", s.listDecisions)
	v1.POST("/decisions/:id/execute", s.executeDecision)

	// Audit + LLM cost tracking
	v1.GET("/audit", s.listAudit)
	v1.GET("/llm/calls", s.listLLMCalls)
	v1.GET("/llm/usage", s.llmUsage)

	// Market data
	v1.GET("/quotes/:symbol", s.quote)

	// Risk control (Wave 3): live view + mutation of the risk
	// engine's limits. Mutations are in-memory only — process
	// restart reloads from env.
	rsk := v1.Group("/risk")
	rsk.GET("/limits", s.getRiskLimits)
	rsk.PATCH("/limits", s.patchRiskLimits)

	// Engine control
	eng := v1.Group("/engine")
	eng.GET("/status", s.engineStatus)
	eng.POST("/toggle", s.engineToggle)
	eng.POST("/ingest", s.engineIngest)
	eng.POST("/decide", s.engineDecide)

	// Broker passthrough
	br := v1.Group("/broker")
	br.GET("/account", s.brokerAccount)
	br.GET("/positions", s.brokerPositions)
}
