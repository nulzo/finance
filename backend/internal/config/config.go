// Package config loads runtime configuration from environment variables.
//
// The configuration is intentionally dependency-light: it reads from the
// process environment (optionally hydrated from a .env file) and validates
// values up front so the rest of the application can rely on well-formed
// configuration values at runtime.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

// Mode controls the trading behaviour of the engine.
type Mode string

const (
	// ModeMock executes trades entirely in-memory against the internal wallet.
	ModeMock Mode = "mock"
	// ModePaper routes trades through the broker's paper-trading endpoint.
	ModePaper Mode = "paper"
	// ModeLive routes trades through the broker's live endpoint.
	ModeLive Mode = "live"
)

// Config is the root configuration object.
type Config struct {
	Env         string
	HTTPAddr    string
	LogLevel    string
	DatabaseURL string
	// APIToken, when non-empty, is required as a Bearer token on all
	// /v1/* HTTP requests. Empty disables HTTP auth entirely.
	APIToken string

	Mode Mode

	// Broker
	AlpacaKey      string
	AlpacaSecret   string
	AlpacaBaseURL  string
	AlpacaDataURL  string
	BrokerProvider string // alpaca|mock

	// LLM (OpenAI-compatible endpoint; works with OpenRouter, a
	// self-hosted model-router, or OpenAI directly).
	LLMAPIKey    string
	LLMBaseURL   string
	LLMModel     string
	LLMFallbacks []string

	// Providers
	QuiverToken      string
	FinnhubKey       string
	NewsAPIKey       string
	AlphaVantageKey  string
	CapitolTradesURL string

	// Engine
	IngestInterval time.Duration
	DecideInterval time.Duration
	EnableEngine   bool

	// Risk / limits
	MaxPositionUSD     decimal.Decimal
	MaxOrderUSD        decimal.Decimal
	MaxDailyLossUSD    decimal.Decimal
	MaxDailyOrders     int
	MaxSymbolExposure  decimal.Decimal // fraction 0..1
	SymbolBlacklist    []string
	RequireApproval    bool
	InitialWalletCents int64

	// Exit policy: take-profit / stop-loss as fractions of average
	// cost. Zero disables that leg. Evaluated every decide tick for
	// held positions before any LLM/signal logic.
	TakeProfitPct float64
	StopLossPct   float64

	// WatchlistCap bounds how many top-signal candidates the decide
	// loop considers per tick. Held positions are always evaluated on
	// top of this cap.
	WatchlistCap int

	// CooldownDuration is how long a symbol is suspended from the
	// decide loop after a non-terminal risk/broker rejection. The
	// daily-cap and daily-loss rejections always suspend until the
	// next UTC midnight regardless of this value.
	CooldownDuration time.Duration

	// ReconcileInterval controls how often the broker reconciliation
	// loop polls open orders and position drift. Zero disables the
	// loop entirely.
	ReconcileInterval time.Duration

	// EquitySnapshotInterval controls how often the engine writes a
	// full portfolio valuation (cash, cost basis, mark-to-market,
	// realised, unrealised, equity) to `equity_snapshots`. Zero
	// disables the snapshot loop. Default 5m is a balance between
	// chart resolution and long-term row count.
	EquitySnapshotInterval time.Duration
	// EquitySnapshotRetention is the maximum age the equity
	// snapshot table will grow to before the hourly retention
	// sweeper starts deleting rows. Default 90d; zero disables.
	EquitySnapshotRetention time.Duration

	// MomentumEnabled turns the technical momentum strategy on/off.
	// Defaults on; operators can kill it via `MOMENTUM_ENABLED=false`
	// if the bar provider becomes unreliable.
	MomentumEnabled bool
	// MomentumWatchlist is an optional static universe for the
	// momentum strategy (comma-separated tickers). Merged with held
	// positions and symbols that appeared in the latest
	// politician/news signals.
	MomentumWatchlist []string
	// MomentumMinConfidence filters out low-conviction technical
	// classifications. Values in [0, 1]; 0 disables the filter.
	MomentumMinConfidence float64

	// PerKindCap is the max candidates any single signal kind
	// (politician/news/momentum/...) may contribute to the per-tick
	// candidate pool. Prevents a single strategy from swallowing
	// every evaluation slot.
	PerKindCap int
	// DiscoverySlots reserves a fixed number of candidate slots for
	// symbols with no current position. Without it the decide loop
	// saturates on whatever was bought on tick 1.
	DiscoverySlots int
	// CandidateConfidenceFloor drops non-held candidates below this
	// confidence before they reach the evaluate() path. Held
	// symbols ignore the floor so exit signals always fire.
	CandidateConfidenceFloor float64
	// MaxConcurrentPositions is the risk-enforced cap on how many
	// distinct symbols may have a non-zero position open at once.
	// Buys that would open a new symbol are rejected once this cap
	// is hit; exits, re-buys, and sells are unaffected. 0 = no cap.
	MaxConcurrentPositions int
	// PoliticianHalfLife controls the age-decay half-life applied
	// to individual politician trades inside the strategy's
	// aggregation window. A 7d half-life means a 7-day-old trade
	// counts half as much as today's. 0 disables decay.
	PoliticianHalfLife time.Duration

	// --- Wave 3 ops ---

	// Strategies gates which signal strategies register per process.
	// Empty = every strategy compiled in. Comma-separated:
	// STRATEGIES="politician,news,momentum".
	Strategies []string

	// OrderStaleTimeout cancels non-terminal orders that have been
	// in flight longer than this. 0 disables the stale-order
	// sweeper (MockBroker fills synchronously so the sweeper is a
	// no-op there anyway).
	OrderStaleTimeout time.Duration

	// AutoDisableBrokerRejects / Window: engine auto-disables after
	// this many broker-side rejections inside the window. 0 on
	// either field disables the breaker.
	AutoDisableBrokerRejects int
	AutoDisableBrokerWindow  time.Duration

	// AutoDisableDailyLossUSD kills the decide loop when realized
	// P&L since UTC midnight drops below -this-value. 0 disables.
	AutoDisableDailyLossUSD decimal.Decimal

	// --- Wave 4 LLM grounding extensions ---

	// PrismWebSearchEnabled turns on the `prism:web_search` LLM
	// extension. Off by default; enable with
	// `PRISM_WEB_SEARCH_ENABLED=true`. Requires PrismSearxngURL.
	PrismWebSearchEnabled bool
	// PrismSearxngURL is the HTTP URL of a searxng instance the
	// Prism gateway can query on the caller's behalf. When empty
	// the extension is force-disabled regardless of the toggle
	// above — a misconfigured searxng URL is the fastest way to
	// make the provider return empty content.
	PrismSearxngURL string
	// PrismMaxResults caps the number of search results the
	// extension returns. Default 3.
	PrismMaxResults int
	// PrismDatetimeEnabled turns on the `prism:datetime` extension
	// so the model sees wall-clock context.
	PrismDatetimeEnabled bool
	// PrismDefaultTimezone feeds the datetime extension. Defaults
	// to America/Chicago.
	PrismDefaultTimezone string
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	_ = godotenv.Load() // optional

	cfg := &Config{
		Env:              env("APP_ENV", "development"),
		HTTPAddr:         env("HTTP_ADDR", ":8080"),
		LogLevel:         env("LOG_LEVEL", "info"),
		DatabaseURL:      env("DATABASE_URL", "file:data/trader.db?_time_format=sqlite&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"),
		APIToken:         sanitizeAPIToken(os.Getenv("API_TOKEN")),
		Mode:             Mode(strings.ToLower(env("TRADER_MODE", string(ModeMock)))),
		AlpacaKey:        env("ALPACA_API_KEY", ""),
		AlpacaSecret:     env("ALPACA_API_SECRET", ""),
		AlpacaBaseURL:    env("ALPACA_BASE_URL", "https://paper-api.alpaca.markets"),
		AlpacaDataURL:    env("ALPACA_DATA_URL", "https://data.alpaca.markets"),
		BrokerProvider:   strings.ToLower(env("BROKER_PROVIDER", "mock")),
		LLMAPIKey:        firstNonEmpty(os.Getenv("LLM_API_KEY"), os.Getenv("OPENROUTER_API_KEY")),
		LLMBaseURL:       firstNonEmpty(os.Getenv("LLM_BASE_URL"), os.Getenv("OPENROUTER_BASE_URL"), "http://192.168.1.249:8080/v1"),
		LLMModel:         env("LLM_MODEL", "openai/gpt-4o-mini"),
		LLMFallbacks:     splitCSV(env("LLM_FALLBACK_MODELS", "anthropic/claude-3.5-haiku,google/gemini-2.0-flash,ollama/llama3.1")),
		QuiverToken:      env("QUIVER_TOKEN", ""),
		FinnhubKey:       env("FINNHUB_KEY", ""),
		NewsAPIKey:       env("NEWSAPI_KEY", ""),
		AlphaVantageKey:  env("ALPHAVANTAGE_KEY", ""),
		CapitolTradesURL: env("CAPITOLTRADES_URL", "https://bff.capitoltrades.com/trades"),
		EnableEngine:     envBool("ENGINE_ENABLED", true),
		RequireApproval:  envBool("REQUIRE_APPROVAL", false),
		SymbolBlacklist:  splitCSV(env("SYMBOL_BLACKLIST", "")),
	}

	var err error
	if cfg.IngestInterval, err = time.ParseDuration(env("INGEST_INTERVAL", "15m")); err != nil {
		return nil, fmt.Errorf("invalid INGEST_INTERVAL: %w", err)
	}
	if cfg.DecideInterval, err = time.ParseDuration(env("DECIDE_INTERVAL", "5m")); err != nil {
		return nil, fmt.Errorf("invalid DECIDE_INTERVAL: %w", err)
	}
	if cfg.CooldownDuration, err = time.ParseDuration(env("COOLDOWN_DURATION", "30m")); err != nil {
		return nil, fmt.Errorf("invalid COOLDOWN_DURATION: %w", err)
	}
	if cfg.ReconcileInterval, err = time.ParseDuration(env("RECONCILE_INTERVAL", "60s")); err != nil {
		return nil, fmt.Errorf("invalid RECONCILE_INTERVAL: %w", err)
	}
	if cfg.EquitySnapshotInterval, err = time.ParseDuration(env("EQUITY_SNAPSHOT_INTERVAL", "5m")); err != nil {
		return nil, fmt.Errorf("invalid EQUITY_SNAPSHOT_INTERVAL: %w", err)
	}
	if cfg.EquitySnapshotRetention, err = time.ParseDuration(env("EQUITY_SNAPSHOT_RETENTION", "2160h")); err != nil {
		return nil, fmt.Errorf("invalid EQUITY_SNAPSHOT_RETENTION: %w", err)
	}

	cfg.MaxPositionUSD = mustDecimal(env("MAX_POSITION_USD", "2500"))
	cfg.MaxOrderUSD = mustDecimal(env("MAX_ORDER_USD", "1000"))
	cfg.MaxDailyLossUSD = mustDecimal(env("MAX_DAILY_LOSS_USD", "500"))
	cfg.MaxSymbolExposure = mustDecimal(env("MAX_SYMBOL_EXPOSURE", "0.2"))
	cfg.MaxDailyOrders = envInt("MAX_DAILY_ORDERS", 20)
	cfg.InitialWalletCents = int64(envInt("INITIAL_WALLET_CENTS", 1000000)) // $10,000
	cfg.TakeProfitPct = envFloat("TAKE_PROFIT_PCT", 0.25)                   // +25%
	cfg.StopLossPct = envFloat("STOP_LOSS_PCT", 0.10)                       // -10%
	cfg.WatchlistCap = envInt("WATCHLIST_CAP", 30)

	cfg.MomentumEnabled = envBool("MOMENTUM_ENABLED", true)
	cfg.MomentumWatchlist = splitCSV(env("MOMENTUM_WATCHLIST", "AAPL,MSFT,NVDA,GOOGL,AMZN,META,TSLA,AMD,NFLX,SPY,QQQ"))
	cfg.MomentumMinConfidence = envFloat("MOMENTUM_MIN_CONFIDENCE", 0.4)

	cfg.PerKindCap = envInt("PER_KIND_CAP", 5)
	cfg.DiscoverySlots = envInt("DISCOVERY_SLOTS", 5)
	cfg.CandidateConfidenceFloor = envFloat("CANDIDATE_CONFIDENCE_FLOOR", 0.35)
	cfg.MaxConcurrentPositions = envInt("MAX_CONCURRENT_POSITIONS", 15)
	if cfg.PoliticianHalfLife, err = time.ParseDuration(env("POLITICIAN_HALFLIFE", "168h")); err != nil {
		return nil, fmt.Errorf("invalid POLITICIAN_HALFLIFE: %w", err)
	}

	// Wave 3 ops.
	cfg.Strategies = splitCSV(env("STRATEGIES", ""))
	if cfg.OrderStaleTimeout, err = time.ParseDuration(env("ORDER_STALE_TIMEOUT", "15m")); err != nil {
		return nil, fmt.Errorf("invalid ORDER_STALE_TIMEOUT: %w", err)
	}
	cfg.AutoDisableBrokerRejects = envInt("AUTO_DISABLE_BROKER_REJECTS", 10)
	if cfg.AutoDisableBrokerWindow, err = time.ParseDuration(env("AUTO_DISABLE_BROKER_WINDOW", "10m")); err != nil {
		return nil, fmt.Errorf("invalid AUTO_DISABLE_BROKER_WINDOW: %w", err)
	}
	cfg.AutoDisableDailyLossUSD = mustDecimal(env("AUTO_DISABLE_DAILY_LOSS_USD", "0"))

	// Wave 4 prism extensions. We intentionally force the web-search
	// toggle off when no searxng URL is supplied: an empty URL reliably
	// makes the gateway return empty `message.content`, which in turn
	// fails every `CompleteJSON` with "unexpected end of JSON input".
	cfg.PrismSearxngURL = strings.TrimSpace(env("PRISM_SEARXNG_URL", ""))
	cfg.PrismWebSearchEnabled = envBool("PRISM_WEB_SEARCH_ENABLED", false) && cfg.PrismSearxngURL != ""
	cfg.PrismMaxResults = envInt("PRISM_MAX_RESULTS", 3)
	cfg.PrismDatetimeEnabled = envBool("PRISM_DATETIME_ENABLED", false)
	cfg.PrismDefaultTimezone = env("PRISM_DEFAULT_TIMEZONE", "America/Chicago")

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Mode {
	case ModeMock, ModePaper, ModeLive:
	default:
		return fmt.Errorf("invalid TRADER_MODE %q", c.Mode)
	}
	if c.Mode == ModeLive && (c.AlpacaKey == "" || c.AlpacaSecret == "") {
		return errors.New("live mode requires ALPACA_API_KEY and ALPACA_API_SECRET")
	}
	if c.MaxOrderUSD.IsNegative() || c.MaxPositionUSD.IsNegative() {
		return errors.New("limits must be non-negative")
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envFloat(key string, def float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sanitizeAPIToken cleans up common footguns in the API_TOKEN env var.
//
// godotenv does not strip trailing "# comment" text on unquoted values,
// so a line like `API_TOKEN=   # optional` ends up setting the token to
// the literal comment text. That silently enables auth and 401s every
// request. We defensively strip whitespace and refuse values that clearly
// look like a stray comment (start with '#' or contain whitespace), since
// no real opaque bearer token would contain either.
func sanitizeAPIToken(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	if strings.HasPrefix(t, "#") || strings.ContainsAny(t, " \t") {
		return ""
	}
	return t
}

// firstNonEmpty returns the first non-empty string in the argument list, or "".
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func mustDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}
