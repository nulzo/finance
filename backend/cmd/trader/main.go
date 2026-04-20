// Command trader is the entry point for the autonomous trading daemon.
//
// Responsibilities:
//   - Load configuration from the environment.
//   - Initialise OpenTelemetry (traces + metrics) and global HTTP
//     instrumentation so every outbound call carries span context.
//   - Open the database and run migrations.
//   - Construct broker, market, LLM and provider aggregators from config.
//   - Seed the default portfolio wallet if missing.
//   - Start the engine's ingestion + decision loops.
//   - Expose the REST API + /metrics over gin.
//   - Expose pprof on a separate port (controlled via PPROF_ADDR).
package main

import (
	"context"
	"errors"
	"net/http"
	_ "net/http/pprof" // side-effect: mount /debug/pprof on DefaultServeMux
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/nulzo/trader/internal/api"
	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/cli"
	"github.com/nulzo/trader/internal/config"
	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/engine"
	"github.com/nulzo/trader/internal/llm"
	applog "github.com/nulzo/trader/internal/platform/logger"
	"github.com/nulzo/trader/internal/providers/congress"
	"github.com/nulzo/trader/internal/providers/market"
	"github.com/nulzo/trader/internal/providers/news"
	"github.com/nulzo/trader/internal/providers/quiver"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
	"github.com/nulzo/trader/internal/telemetry"
)

var (
	version = "0.1.0"
	commit  = "dev"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	// Pretty startup banner. Printed to stdout before the logger is
	// installed so it sits at the top of the boot output regardless of
	// LOG_FORMAT. Colours auto-disable when NO_COLOR is set.
	cli.PrintBanner(os.Stdout, cli.BannerInfo{
		Tagline:     "Autonomous trading daemon.",
		Version:     version,
		Commit:      commit,
		Environment: cfg.Env,
		Mode:        string(cfg.Mode),
		Addr:        cfg.HTTPAddr,
		GithubURL:   "https://github.com/nulzo/trader",
	})

	// Structured logger. The platform/logger package configures
	// zerolog with a colour-aware console encoder (LOG_FORMAT=console,
	// default) or newline-delimited JSON (LOG_FORMAT=json) for log
	// shippers. We install it as the process-wide logger so middleware
	// and lazily-built child loggers share the same sink + level.
	log := applog.New(applog.Config{
		Level:       cfg.LogLevel,
		Format:      applog.Format(env("LOG_FORMAT", consoleWhenDev(cfg.Env))),
		EnableColor: cfg.Env == "development",
		ServiceName: env("OTEL_SERVICE_NAME", "trader"),
	})
	applog.SetGlobal(log)
	log.Info().Str("env", cfg.Env).Str("mode", string(cfg.Mode)).Str("version", version).Str("commit", commit).Msg("starting")

	if err := os.MkdirAll(filepath.Dir("data/trader.db"), 0o755); err != nil {
		log.Fatal().Err(err).Msg("mkdir data")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ------------------------------------------------------------
	// Observability. Initialise OTel before anything else creates
	// HTTP clients or opens databases so every downstream call
	// inherits propagation + instrumentation.
	// ------------------------------------------------------------
	telProvs, telShutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:    env("OTEL_SERVICE_NAME", "trader"),
		ServiceVersion: version,
		Environment:    cfg.Env,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry init")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := telShutdown(shutdownCtx); err != nil {
			log.Warn().Err(err).Msg("telemetry shutdown")
		}
	}()
	log.Info().
		Bool("otlp_enabled", telProvs.OTLPEnabled).
		Str("otlp_endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")).
		Msg("telemetry ready")

	// Globally wrap http.DefaultTransport so every http.Client in the
	// process (including the providers we construct below) gets
	// otelhttp tracing + http.client.* metrics for free. Must happen
	// AFTER telemetry.Init so the right providers are captured.
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)

	store, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("open store")
	}
	defer store.Close()

	portfolio, err := seedPortfolio(ctx, store, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("seed portfolio")
	}
	log.Info().Str("portfolio", portfolio.ID).Msg("portfolio ready")

	// Fallback chain used both by the mock broker and the cached quote
	// provider. Order: Stooq (public CSV) -> Yahoo -> Synthetic.
	fallbackChain := market.NewCachedChain(30*time.Second,
		market.NewStooq(),
		market.NewYahoo(),
		market.NewSynthetic(),
	)

	// Broker selection.
	var b broker.Broker
	switch cfg.BrokerProvider {
	case "alpaca":
		b = broker.NewAlpacaBroker(broker.AlpacaConfig{
			APIKey:    cfg.AlpacaKey,
			APISecret: cfg.AlpacaSecret,
			BaseURL:   cfg.AlpacaBaseURL,
			DataURL:   cfg.AlpacaDataURL,
		})
	default:
		mb := broker.NewMockBroker(fallbackChain, decimal.NewFromInt(cfg.InitialWalletCents/100), 0.0005)
		// The mock broker keeps positions in-memory; without this the
		// DB has open positions from previous runs but the broker has
		// zero, and every sell comes back "insufficient position".
		if err := hydrateMockBroker(ctx, mb, store, portfolio, fallbackChain); err != nil {
			log.Warn().Err(err).Msg("hydrate mock broker (sells for pre-existing positions may fail)")
		}
		b = mb
	}
	log.Info().Str("broker", b.Name()).Msg("broker ready")

	// Market quotes: broker -> stooq -> yahoo -> synthetic (cached).
	quoteProv := market.NewCachedChain(10*time.Second,
		market.BrokerAdapter{B: b},
		market.NewStooq(),
		market.NewYahoo(),
		market.NewSynthetic(),
	)

	// Historical bars + technical indicators for the momentum strategy.
	// Stooq is free and reliable for US equities; we wrap it in a
	// 15-minute cache so the daily-bar calc doesn't re-hit the upstream
	// on every ingest tick.
	barChain := market.NewCachedBarChain(15*time.Minute, market.NewStooqBars())
	technicals := market.NewTechnicalProvider(barChain, 260, 5*time.Minute)

	// LLM pricing + DB-backed call recorder. Pricing is best-effort —
	// a malformed LLM_PRICING_JSON logs and falls back to the defaults.
	priceTable, err := llm.LoadPriceTableFromEnv()
	if err != nil {
		log.Warn().Err(err).Msg("llm pricing: bad LLM_PRICING_JSON, using defaults")
	}
	llmRecorder := newDBLLMRecorder(store, log)

	// Build Prism extensions on the client up front — we only add the
	// extension object when the feature is actually enabled AND, for
	// web_search, when a searxng URL was supplied. This avoids the
	// gateway silently responding with empty `message.content` when
	// the extension is configured against an unreachable host.
	var defaultExts []llm.Extension
	if cfg.PrismWebSearchEnabled {
		defaultExts = append(defaultExts, llm.Extension{
			ID:      "prism:web_search",
			Enabled: true,
			Config: llm.ExtensionConfig{
				"max_results": cfg.PrismMaxResults,
				"searxng_url": cfg.PrismSearxngURL,
			},
		})
	}
	if cfg.PrismDatetimeEnabled {
		defaultExts = append(defaultExts, llm.Extension{
			ID:      "prism:datetime",
			Enabled: true,
			Config: llm.ExtensionConfig{
				"default_timezone": cfg.PrismDefaultTimezone,
			},
		})
	}

	llmClient := llm.NewClient(
		cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMFallbacks,
		llm.WithPricing(priceTable),
		llm.WithRecorder(llmRecorder),
		llm.WithDefaultExtensions(defaultExts...),
	)
	if llmClient.Available() {
		log.Info().
			Str("model", cfg.LLMModel).
			Str("base_url", cfg.LLMBaseURL).
			Bool("authed", cfg.LLMAPIKey != "").
			Bool("web_search", cfg.PrismWebSearchEnabled).
			Bool("datetime", cfg.PrismDatetimeEnabled).
			Msg("llm available")
	} else {
		log.Warn().Msg("llm disabled (no base url); heuristic strategies only")
	}

	// Providers.
	congressAgg := &congress.Aggregator{
		Sources: []congress.Source{
			// Lambda Finance is free and keyless; it usually works even
			// when CapitolTrades' CloudFront→Lambda BFF is degraded.
			congress.NewLambdaFinance(),
			congress.NewCapitolTrades(cfg.CapitolTradesURL),
			congress.NewQuiver(cfg.QuiverToken),
		},
		Log: log,
	}
	newsSources := []news.Source{}
	if f := news.NewFinnhub(cfg.FinnhubKey); f != nil {
		newsSources = append(newsSources, f)
	}
	if n := news.NewNewsAPI(cfg.NewsAPIKey); n != nil {
		newsSources = append(newsSources, n)
	}
	newsSources = append(newsSources, news.NewRSS())
	newsAgg := &news.Aggregator{Sources: newsSources, Log: log}

	// Quiver client for Wave 4 alt-data (insiders/social/lobbying/
	// contracts/short-vol). Token reuse is fine — Quiver's DRF
	// tokens are scoped to the account, not per-endpoint.
	quiverClient := quiver.New(cfg.QuiverToken)
	if quiverClient.Available() {
		log.Info().Msg("quiver alt-data ready (insiders/wsb/twitter/lobbying/contracts/shortvol)")
	}

	// Risk.
	riskEngine := risk.NewEngine(risk.Limits{
		MaxOrderUSD:            cfg.MaxOrderUSD,
		MaxPositionUSD:         cfg.MaxPositionUSD,
		MaxDailyLossUSD:        cfg.MaxDailyLossUSD,
		MaxDailyOrders:         cfg.MaxDailyOrders,
		MaxSymbolExposure:      cfg.MaxSymbolExposure,
		MaxConcurrentPositions: cfg.MaxConcurrentPositions,
		Blacklist:              cfg.SymbolBlacklist,
		RequireApproval:        cfg.RequireApproval,
	})

	// Engine.
	eng := engine.New(engine.Deps{
		Store:             store,
		Broker:            b,
		Market:            quoteProv,
		Technicals:        technicals,
		LLM:               llmClient,
		Congress:          congressAgg,
		News:              newsAgg,
		Quiver:            quiverClient,
		Risk:              riskEngine,
		Log:               log,
		PortfolioID:       portfolio.ID,
		IngestInterval:          cfg.IngestInterval,
		DecideInterval:          cfg.DecideInterval,
		ReconcileInterval:       cfg.ReconcileInterval,
		EquitySnapshotInterval:  cfg.EquitySnapshotInterval,
		EquitySnapshotRetention: cfg.EquitySnapshotRetention,
		WatchlistCap:      cfg.WatchlistCap,
		CooldownDuration:  cfg.CooldownDuration,
		ExitPolicy: engine.ExitPolicy{
			TakeProfitPct: cfg.TakeProfitPct,
			StopLossPct:   cfg.StopLossPct,
		},
		MomentumEnabled:          cfg.MomentumEnabled,
		MomentumWatchlist:        cfg.MomentumWatchlist,
		MomentumMinConfidence:    cfg.MomentumMinConfidence,
		PerKindCap:               cfg.PerKindCap,
		DiscoverySlots:           cfg.DiscoverySlots,
		CandidateConfidenceFloor: cfg.CandidateConfidenceFloor,
		PoliticianHalfLife:       cfg.PoliticianHalfLife,
		Strategies:               cfg.Strategies,
		OrderStaleTimeout:        cfg.OrderStaleTimeout,
		AutoDisableBrokerRejects: cfg.AutoDisableBrokerRejects,
		AutoDisableBrokerWindow:  cfg.AutoDisableBrokerWindow,
		AutoDisableDailyLossUSD:  cfg.AutoDisableDailyLossUSD,
	})
	eng.SetEnabled(cfg.EnableEngine)

	if raw := os.Getenv("API_TOKEN"); raw != "" && cfg.APIToken == "" {
		log.Warn().
			Msg("API_TOKEN looked malformed (stray whitespace or inline # comment); auth disabled. " +
				"Quote the value or move the comment to its own line if you intended to enable auth.")
	}
	if cfg.APIToken == "" {
		log.Info().Msg("http auth disabled (no API_TOKEN set)")
	} else {
		log.Info().Msg("http auth enabled; /v1/* requires Bearer API_TOKEN")
	}

	srv := api.New(api.Deps{
		Store:          store,
		Broker:         b,
		Market:         quoteProv,
		Engine:         eng,
		Risk:           riskEngine,
		PortfolioID:    portfolio.ID,
		APIToken:       cfg.APIToken,
		Log:            log,
		Version:        version,
		BuildCommit:    commit,
		ServiceName:    "trader",
		MetricsHandler: telProvs.PromHandler,
		ReadinessCheck: func(ctx context.Context) error { return store.Ping(ctx) },
	})

	// Dump the mounted HTTP surface to stdout. Runs exactly once at
	// boot so devs can eyeball the API contract without hitting the
	// server. Suppress via TRADER_PRINT_ROUTES=false.
	if env("TRADER_PRINT_ROUTES", "true") != "false" {
		srv.PrintRoutes(os.Stdout)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Engine loop.
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		if err := eng.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("engine terminated")
		}
	}()

	// HTTP loop.
	httpDone := make(chan struct{})
	go func() {
		defer close(httpDone)
		log.Info().Str("addr", cfg.HTTPAddr).Msg("http listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("http server")
		}
	}()

	// pprof (optional, controlled via PPROF_ADDR). When empty pprof is
	// disabled; when set to e.g. ":6060" we serve /debug/pprof and
	// /debug/vars on a dedicated mux so the main API stays clean.
	pprofAddr := os.Getenv("PPROF_ADDR")
	if pprofAddr != "" {
		pprofSrv := &http.Server{Addr: pprofAddr, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Info().Str("addr", pprofAddr).Msg("pprof listening")
			if err := pprofSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Warn().Err(err).Msg("pprof server")
			}
		}()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = pprofSrv.Shutdown(ctx)
		}()
	}

	<-sigCh
	log.Info().Msg("shutdown requested")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = httpServer.Shutdown(shutdownCtx)
	cancel()
	<-engineDone
	<-httpDone
	log.Info().Msg("bye")
}

// env returns the first non-empty env var value or the fallback.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// consoleWhenDev picks the default log format: human-readable console
// for development, machine-parsable JSON otherwise. Explicit LOG_FORMAT
// always wins via env().
func consoleWhenDev(envName string) string {
	if envName == "development" || envName == "" {
		return string(applog.FormatConsole)
	}
	return string(applog.FormatJSON)
}

// dbLLMRecorder persists each LLM attempt via the storage layer.
// Writes are fire-and-forget on a background goroutine so the LLM hot
// path never blocks on SQLite. Failures are logged at debug level.
type dbLLMRecorder struct {
	store *storage.Store
	log   zerolog.Logger
}

func newDBLLMRecorder(store *storage.Store, log zerolog.Logger) llm.Recorder {
	return &dbLLMRecorder{store: store, log: log}
}

func (r *dbLLMRecorder) RecordCall(ctx context.Context, rec llm.CallRecord) {
	row := domain.LLMCall{
		Operation:        rec.Operation,
		AttemptIndex:     rec.AttemptIndex,
		ModelRequested:   rec.ModelRequested,
		ModelUsed:        rec.ModelUsed,
		Outcome:          rec.Outcome,
		PromptTokens:     rec.PromptTokens,
		CompletionTokens: rec.CompletionTokens,
		TotalTokens:      rec.TotalTokens,
		LatencyMS:        rec.LatencyMS,
		RequestBytes:     rec.RequestBytes,
		ResponseBytes:    rec.ResponseBytes,
		RequestMessages:  rec.RequestMessages,
		ResponseText:     rec.ResponseText,
		ErrorMessage:     rec.ErrorMessage,
		TraceID:          rec.TraceID,
		SpanID:           rec.SpanID,
		Temperature:      rec.Temperature,
		MaxTokens:        rec.MaxTokens,
		JSONMode:         rec.JSONMode,
	}
	row.PromptCostUSD, _ = decimal.NewFromString(nonEmpty(rec.PromptCostUSD, "0"))
	row.CompletionCostUSD, _ = decimal.NewFromString(nonEmpty(rec.CompletionCostUSD, "0"))
	row.TotalCostUSD, _ = decimal.NewFromString(nonEmpty(rec.TotalCostUSD, "0"))

	// Cap response/request payload size so one runaway prompt can't blow
	// out SQLite. 64KB per field is plenty for audit purposes.
	row.RequestMessages = clampLen(row.RequestMessages, 64*1024)
	row.ResponseText = clampLen(row.ResponseText, 64*1024)
	row.ErrorMessage = clampLen(row.ErrorMessage, 8*1024)

	// Fire-and-forget: detach from request ctx so a cancelled HTTP
	// request doesn't abort the write, and never block the LLM caller.
	go func() {
		wctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.store.LLMCalls.Insert(wctx, &row); err != nil {
			r.log.Debug().Err(err).Str("model", row.ModelUsed).Msg("llm call persist failed")
		}
	}()
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func clampLen(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// hydrateMockBroker syncs the mock broker's in-memory cash + position
// state with the persisted wallet + position rows. Without this, a
// restart leaves the broker with zero positions while the DB still
// reflects prior trades — every sell on a pre-existing position gets
// rejected by the broker for "insufficient position" and the engine
// can never unwind its own book.
//
// The market-value snapshot uses the live quote chain when available,
// falling back to average cost only when the quote provider fails.
// Using avg cost for MarketVal makes the mock broker's equity view
// stale on any symbol that's moved since it was opened.
func hydrateMockBroker(ctx context.Context, mb *broker.MockBroker, store *storage.Store, p *domain.Portfolio, quotes broker.PriceSource) error {
	positions, err := store.Positions.List(ctx, p.ID)
	if err != nil {
		return err
	}
	snap := make(map[string]broker.BrokerPosition, len(positions))
	for _, pos := range positions {
		markPrice := pos.AvgCostCents.Dollars()
		if quotes != nil {
			qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			if q, qerr := quotes.Quote(qctx, pos.Symbol); qerr == nil && q != nil && q.Price.GreaterThan(decimal.Zero) {
				markPrice = q.Price
			}
			cancel()
		}
		snap[pos.Symbol] = broker.BrokerPosition{
			Symbol:    pos.Symbol,
			Quantity:  pos.Quantity,
			AvgPrice:  pos.AvgCostCents.Dollars(),
			MarketVal: pos.Quantity.Mul(markPrice),
		}
	}
	mb.Hydrate(p.AvailableCents().Dollars(), snap)
	return nil
}

func seedPortfolio(ctx context.Context, store *storage.Store, cfg *config.Config) (*domain.Portfolio, error) {
	p, err := store.Portfolios.GetByName(ctx, "main")
	if err == nil {
		return p, nil
	}
	if !storage.IsNotFound(err) {
		return nil, err
	}
	p = &domain.Portfolio{
		Name:      "main",
		Mode:      string(cfg.Mode),
		CashCents: domain.Money(cfg.InitialWalletCents),
	}
	if err := store.Portfolios.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}
