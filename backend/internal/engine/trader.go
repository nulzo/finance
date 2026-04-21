// Package engine hosts the trading orchestrator. It runs three loops:
//
//  1. Ingestion — polls providers, deduplicates, enriches with the LLM,
//     and persists signals.
//  2. Decision — evaluates active signals, asks the LLM/risk engine to
//     size a trade, and executes via the broker.
//  3. Reconciliation — periodically syncs the DB's view of open orders
//     and positions with the broker's authoritative view.
//
// All loops are driven by independent tick intervals, are cancellable
// via context, and never abort on provider error (errors are logged and
// the loop continues).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/llm"
	"github.com/nulzo/trader/internal/providers/congress"
	"github.com/nulzo/trader/internal/providers/market"
	"github.com/nulzo/trader/internal/providers/news"
	"github.com/nulzo/trader/internal/providers/quiver"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
	"github.com/nulzo/trader/internal/strategy"
	"github.com/nulzo/trader/internal/telemetry"
)

// tracer is the engine's OTel tracer. It's safe to capture at init time
// because otel.Tracer returns a thin wrapper that resolves the current
// provider lazily on every Start call.
var tracer = otel.Tracer("github.com/nulzo/trader/internal/engine")

// Deps collects everything the engine needs.
type Deps struct {
	Store             *storage.Store
	Broker            broker.Broker
	Market            market.QuoteProvider
	Technicals        *market.TechnicalProvider
	LLM               *llm.Client
	Congress          *congress.Aggregator
	News              *news.Aggregator
	Quiver            *quiver.Client // optional — powers insider/social/lobbying/contracts/shortvol
	Risk              *risk.Engine
	Log               zerolog.Logger
	PortfolioID       string
	IngestInterval    time.Duration
	DecideInterval    time.Duration
	ReconcileInterval time.Duration
	// EquitySnapshotInterval controls how often the engine writes a
	// full portfolio valuation (cash, cost, MTM, realised, unrealised,
	// equity) to the `equity_snapshots` table. Zero disables the
	// snapshot loop entirely (tests use this to stay deterministic).
	// Default 5m is a balance between chart resolution and table size
	// (~288 rows/day ≈ 100k rows/year per portfolio).
	EquitySnapshotInterval time.Duration
	// EquitySnapshotRetention is the maximum age kept in the
	// `equity_snapshots` table; anything older is purged in a
	// once-per-hour GC sweep the snapshot loop fires. Zero disables
	// retention (not recommended in prod). Default 90d.
	EquitySnapshotRetention time.Duration
	WatchlistCap      int
	ExitPolicy        ExitPolicy
	// CooldownDuration is the default suspension applied to a symbol
	// after a non-terminal risk / broker rejection. Zero falls back
	// to 30 minutes.
	CooldownDuration time.Duration

	// MomentumEnabled turns the technical momentum strategy on/off.
	// When off the engine skips the whole bar-ingest step and never
	// emits SignalKindMomentum rows.
	MomentumEnabled bool
	// MomentumWatchlist seeds the momentum universe with symbols that
	// may not yet have any politician / news signal. Held positions
	// and newly-signalled symbols are merged in automatically.
	MomentumWatchlist []string
	// MomentumMinConfidence filters weak technical classifications
	// before they ever land in the Signals store.
	MomentumMinConfidence float64

	// PerKindCap limits how many symbols from each signal kind
	// reach the candidate pool each tick. Prevents politician
	// signals from monopolising the leaderboard. Default 5.
	PerKindCap int
	// DiscoverySlots reserves N slots in the candidate pool for
	// unheld symbols so new names actually get considered.
	// Default 5.
	DiscoverySlots int
	// CandidateConfidenceFloor drops candidates with confidence
	// below this floor from the per-kind / discovery / top-overall
	// passes. Held symbols ignore the floor (exits always
	// evaluated). Default 0.35.
	CandidateConfidenceFloor float64
	// PoliticianHalfLife applies an age-decay half-life to
	// individual politician trades so the leaderboard churns as
	// trades age out instead of sitting static for the whole
	// 21-day lookback. Zero disables decay.
	PoliticianHalfLife time.Duration

	// Strategies enumerates which signal strategies are allowed to
	// run this process. Values correspond to `domain.SignalKind`
	// constants ("politician", "news", "momentum", ...). Empty
	// slice ⇒ every compiled-in strategy runs (default behaviour).
	// Primarily a circuit breaker for operators: if news ingestion
	// is misbehaving, flip `STRATEGIES=politician,momentum` without
	// a code change.
	Strategies []string

	// --- Wave 3 ops ---

	// OrderStaleTimeout is the max time an order can sit in
	// `submitted` before the reconcile loop cancels it at the
	// broker and marks it cancelled locally. Zero disables the
	// stale-order sweeper. Applies to any non-terminal status
	// (`pending` / `submitted` / `partial`) for a broker that
	// returns an asynchronous fill lifecycle.
	OrderStaleTimeout time.Duration

	// AutoDisableBrokerRejects is the circuit-breaker threshold:
	// if more than this many broker-side rejections occur inside
	// `AutoDisableBrokerWindow`, the engine auto-disables itself
	// and records an audit event. Zero disables the breaker.
	AutoDisableBrokerRejects int
	AutoDisableBrokerWindow  time.Duration

	// AutoDisableDailyLossUSD is a tighter "kill-switch" than the
	// risk engine's per-order daily-loss limit. When realized P&L
	// (since UTC midnight) falls below -AutoDisableDailyLossUSD
	// the engine flips to disabled and writes an audit event. Zero
	// disables this breaker. Setting it somewhat above
	// MAX_DAILY_LOSS_USD is typical — risk blocks the last trade,
	// this one pauses future ticks until a human reviews.
	AutoDisableDailyLossUSD decimal.Decimal
}

// ExitPolicy defines unconditional take-profit / stop-loss thresholds
// evaluated on every decide tick for held positions. Both percentages
// are of average cost; zero disables that leg.
//
// Example: TakeProfitPct=0.25, StopLossPct=0.10 closes a position when
// the mark hits +25% or -10% relative to average cost. This runs
// independently of LLM/signal opinion so the system always has a
// mechanical exit even in quiet news cycles.
type ExitPolicy struct {
	TakeProfitPct float64
	StopLossPct   float64
}

// Evaluate returns (action, true) if mark price crosses either
// threshold, else (_, false). Negative thresholds are clamped to zero
// so a misconfigured env var can't turn a stop-loss into a take-profit.
func (p ExitPolicy) Evaluate(avgCost, mark decimal.Decimal) (domain.DecisionAction, bool) {
	if avgCost.IsZero() || mark.IsZero() {
		return "", false
	}
	// Pct = (mark - avg) / avg.
	pct, _ := mark.Sub(avgCost).Div(avgCost).Float64()
	if p.TakeProfitPct > 0 && pct >= p.TakeProfitPct {
		return domain.DecisionActionSell, true
	}
	if p.StopLossPct > 0 && pct <= -p.StopLossPct {
		return domain.DecisionActionSell, true
	}
	return "", false
}

// Engine coordinates ingestion, decisioning and broker reconciliation.
type Engine struct {
	deps    Deps
	mu      sync.RWMutex
	enabled bool
	// quiverGated memoises which Quiver datasets we've already logged
	// as "not on subscription plan" this process lifetime. Quiver
	// endpoints gated by the caller's subscription return the same
	// gate response on every tick, so without this we'd debug-log it
	// on a loop forever. Once we've noted it, subsequent ticks drop
	// silently and the engine just behaves as though the dataset is
	// empty.
	quiverGated sync.Map // map[string]struct{}
}

// New builds an Engine.
func New(d Deps) *Engine {
	if d.WatchlistCap <= 0 {
		// Sized to comfortably accommodate per-kind quotas (5 × 3
		// kinds = 15) + discovery (5) + a typical open-book of
		// ~8 held positions. The old default of 10 was the root
		// cause of the Wave 2.5 concentration bug — a single
		// strategy's leaderboard could swallow every slot.
		d.WatchlistCap = 30
	}
	if d.CooldownDuration <= 0 {
		d.CooldownDuration = 30 * time.Minute
	}
	// Negative = "use default"; exactly 0 = "feature explicitly
	// disabled". Without this split, tests that want to isolate the
	// per-kind pass from discovery by setting DiscoverySlots = 0
	// would silently get the default (5) back.
	if d.PerKindCap < 0 {
		d.PerKindCap = 5
	}
	if d.DiscoverySlots < 0 {
		d.DiscoverySlots = 5
	}
	if d.CandidateConfidenceFloor < 0 {
		d.CandidateConfidenceFloor = 0.35
	}
	return &Engine{deps: d, enabled: true}
}

// SetEnabled toggles the main loops.
func (e *Engine) SetEnabled(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = v
}

// Enabled reports whether loops are active.
func (e *Engine) Enabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.enabled
}

// Run starts ingestion, decision and reconciliation loops. It returns
// when ctx is done. The reconciliation loop is only started when a
// non-zero ReconcileInterval is configured so tests can opt out.
func (e *Engine) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		e.loop(ctx, "ingest", e.deps.IngestInterval, e.Ingest)
	}()
	go func() {
		defer wg.Done()
		e.loop(ctx, "decide", e.deps.DecideInterval, e.DecideAndTrade)
	}()
	if e.deps.ReconcileInterval > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.loop(ctx, "reconcile", e.deps.ReconcileInterval, e.Reconcile)
		}()
	}
	// Equity-snapshot loop: an independent interval from the other
	// loops because snapshots are cheap (one quote fetch per open
	// position + one INSERT) and want higher resolution than the
	// ingest/decide cadence for smoother charts. Guarded by
	// EquitySnapshotInterval > 0 so tests and tools can opt out.
	if e.deps.EquitySnapshotInterval > 0 && e.deps.Store != nil && e.deps.Store.Equity != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.loop(ctx, "equity-snapshot", e.deps.EquitySnapshotInterval, e.SnapshotEquity)
		}()
		// Retention sweeper runs hourly and purges rows older than
		// EquitySnapshotRetention. Kept in its own goroutine so it
		// doesn't delay a snapshot tick; a delete scanning millions
		// of rows can take a few seconds on a cold SQLite page cache.
		if e.deps.EquitySnapshotRetention > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.loop(ctx, "equity-retention", time.Hour, e.purgeOldSnapshots)
			}()
		}
	}
	wg.Wait()
	return ctx.Err()
}

// purgeOldSnapshots deletes equity snapshots beyond the retention
// window. Exposed as a method (not a closure) so it plays well with
// the Engine's tick-wrapping instrumentation.
func (e *Engine) purgeOldSnapshots(ctx context.Context) error {
	if e.deps.EquitySnapshotRetention <= 0 || e.deps.Store == nil || e.deps.Store.Equity == nil {
		return nil
	}
	cutoff := time.Now().UTC().Add(-e.deps.EquitySnapshotRetention)
	deleted, err := e.deps.Store.Equity.PurgeOlderThan(ctx, cutoff)
	if err != nil {
		return err
	}
	if deleted > 0 {
		e.deps.Log.Info().Int64("deleted", deleted).Time("cutoff", cutoff).Msg("equity snapshots purged")
	}
	return nil
}

func (e *Engine) loop(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	// Run once immediately.
	e.runTick(ctx, name, fn)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !e.Enabled() {
				continue
			}
			e.runTick(ctx, name, fn)
		}
	}
}

// runTick wraps a single ingest/decide invocation with a span + metric
// hooks so every iteration is independently observable. Errors are
// logged but never propagated past the loop.
func (e *Engine) runTick(ctx context.Context, name string, fn func(context.Context) error) {
	tickCtx, span := tracer.Start(ctx, "engine.tick",
		trace.WithAttributes(attribute.String("engine.loop", name)),
	)
	start := time.Now()
	err := fn(tickCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		e.deps.Log.Warn().Err(err).Str("loop", name).Msg("iteration failed")
	}
	telemetry.App.RecordEngineTick(tickCtx, name, start, err)
	span.End()
}

// Ingest pulls politician trades and news, then persists + scores them.
//
// Each stage gets its own child context so a slow upstream (e.g. Quiver's
// /senatetrading endpoint routinely takes 30–40s) cannot starve the ones
// downstream. Previously everything shared a single 60s budget which made
// the pipeline cascade-fail with "context deadline exceeded" the moment
// any one stage went slow.
func (e *Engine) Ingest(ctx context.Context) error {
	// Give the ingest loop most of the interval budget.
	timeout := e.deps.IngestInterval - 10*time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ictx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Politician trades.
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if e.deps.Congress != nil {
		cctx, ccancel := context.WithTimeout(ictx, 90*time.Second)
		trades, err := e.deps.Congress.Fetch(cctx, since)
		if err != nil {
			e.deps.Log.Warn().Err(err).Msg("congress fetch")
		}
		inserted := 0
		for _, t := range trades {
			_, err := e.deps.Store.PTrades.Insert(cctx, &t)
			if err != nil {
				e.deps.Log.Warn().Err(err).Str("politician", t.PoliticianName).Msg("insert ptrade")
				continue
			}
			inserted++
			_ = e.deps.Store.Politicians.Upsert(cctx, &domain.Politician{
				Name:        t.PoliticianName,
				Chamber:     t.Chamber,
				TrackWeight: 1.0,
			})
		}
		e.deps.Log.Info().Int("fetched", len(trades)).Int("inserted", inserted).Msg("congress ingest")
		if telemetry.App.CongressFetched != nil {
			telemetry.App.CongressFetched.Add(cctx, int64(inserted),
				metric.WithAttributes(attribute.String("stage", "inserted")))
			telemetry.App.CongressFetched.Add(cctx, int64(len(trades)-inserted),
				metric.WithAttributes(attribute.String("stage", "deduped")))
		}
		ccancel()
	}

	// News.
	if e.deps.News != nil {
		nctx, ncancel := context.WithTimeout(ictx, 60*time.Second)
		items, err := e.deps.News.Fetch(nctx, time.Now().UTC().Add(-24*time.Hour))
		if err != nil {
			e.deps.Log.Warn().Err(err).Msg("news fetch")
		}
		inserted := 0
		enriched := 0
		enrichErrs := 0
		var firstEnrichErr error
		for _, it := range items {
			ok, err := e.deps.Store.News.Insert(nctx, &it)
			if err != nil {
				continue
			}
			if !ok {
				continue
			}
			inserted++
			if e.deps.LLM != nil && e.deps.LLM.Available() {
				// LLM enrichment gets a deadline derived from the parent
				// request context (ictx), NOT nctx. The news-fetch budget
				// is intentionally tight; enrichment is a slower, best-
				// effort pass that shouldn't inherit its siblings' clock.
				actx, acancel := context.WithTimeout(ictx, 30*time.Second)
				actx = llm.WithOperation(actx, "news.analyse")
				analysis, model, err := e.deps.LLM.AnalyseNews(actx, it.Title, it.Summary)
				acancel()
				if err != nil {
					enrichErrs++
					if firstEnrichErr == nil {
						firstEnrichErr = err
					}
					e.deps.Log.Debug().Err(err).Str("title", truncate(it.Title, 80)).Msg("news enrich failed")
					continue
				}
				if analysis == nil {
					continue
				}
				if err := e.deps.Store.News.UpdateSentiment(ictx, it.ID, analysis.Sentiment, analysis.Relevance, strings.Join(analysis.Symbols, ",")); err != nil {
					e.deps.Log.Debug().Err(err).Msg("news update sentiment")
					continue
				}
				enriched++
				e.deps.Log.Debug().
					Str("model", model).
					Str("title", truncate(it.Title, 80)).
					Float64("sentiment", analysis.Sentiment).
					Float64("relevance", analysis.Relevance).
					Strs("symbols", analysis.Symbols).
					Msg("news enriched")
			}
		}
		evt := e.deps.Log.Info().Int("fetched", len(items)).Int("inserted", inserted)
		if e.deps.LLM != nil && e.deps.LLM.Available() {
			evt = evt.Int("enriched", enriched).Int("enrich_errors", enrichErrs)
		}
		evt.Msg("news ingest")
		if telemetry.App.NewsFetched != nil {
			telemetry.App.NewsFetched.Add(ictx, int64(inserted),
				metric.WithAttributes(attribute.String("stage", "inserted")))
			telemetry.App.NewsEnriched.Add(ictx, int64(enriched),
				metric.WithAttributes(attribute.String("outcome", "ok")))
			if enrichErrs > 0 {
				telemetry.App.NewsEnriched.Add(ictx, int64(enrichErrs),
					metric.WithAttributes(attribute.String("outcome", "error")))
			}
		}
		if enrichErrs > 0 && firstEnrichErr != nil {
			e.deps.Log.Warn().
				Err(firstEnrichErr).
				Int("failed", enrichErrs).
				Int("total", inserted).
				Msg("news LLM enrichment failed for at least one item; set LOG_LEVEL=debug for per-item errors")
		}
		ncancel()
	}

	// Wave 4 — Quiver alt-data: insider Form 4 filings, WSB /
	// Twitter social rollups, corporate lobbying, federal contracts,
	// off-exchange short volume. All five endpoints are gated on a
	// configured Quiver token; without one the block no-ops. Each
	// dataset runs inside its own bounded deadline so one slow
	// endpoint can't starve its siblings.
	if e.deps.Quiver != nil && e.deps.Quiver.Available() {
		e.ingestQuiver(ictx)
	}

	// Regenerate signals from strategies. Independent deadline from the
	// ingest steps above so a slow external fetch cannot starve local DB
	// work. We give it 3 minutes because momentum strategy fetches
	// technicals for the entire universe.
	sctx, scancel := context.WithTimeout(ictx, 3*time.Minute)
	defer scancel()
	return e.regenerateSignals(sctx)
}

// ingestQuiver pulls Wave 4 alternative-data endpoints and persists
// fresh rows. Subscription-gate responses are handled as soft-failures
// (logged at debug, not treated as retryable errors) so an account on
// a limited plan doesn't spam warn-level logs every tick.
func (e *Engine) ingestQuiver(ctx context.Context) {
	q := e.deps.Quiver
	// Each endpoint gets its own deadline so the pipeline stays
	// responsive when one is slow. Values are generous because Quiver
	// Django endpoints routinely take 20-40s on a cold cache.
	run := func(name string, timeout time.Duration, fn func(context.Context) error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := fn(cctx); err != nil {
			if errors.Is(err, quiver.ErrSubscriptionRequired) {
				// Log once per dataset per process lifetime at
				// info level so operators see "this dataset is
				// gated" on startup, then silently drop the
				// per-tick repeats.
				if _, seen := e.quiverGated.LoadOrStore(name, struct{}{}); !seen {
					e.deps.Log.Info().Str("dataset", name).Msg("quiver dataset not on subscription plan; skipping")
				}
				return
			}
			e.deps.Log.Warn().Err(err).Str("dataset", name).Msg("quiver ingest")
		}
	}

	// Insider Form 4 filings. 30-day lookback: stale buys/sells lose
	// their edge quickly, and the InsiderFollow strategy applies its
	// own age-decay on top of what survives.
	run("insiders", 60*time.Second, func(cctx context.Context) error {
		trades, err := q.FetchInsiders(cctx, time.Now().UTC().Add(-30*24*time.Hour))
		if err != nil {
			return err
		}
		inserted := 0
		for i := range trades {
			ok, err := e.deps.Store.Insiders.Insert(cctx, &trades[i])
			if err != nil {
				e.deps.Log.Debug().Err(err).Str("symbol", trades[i].Symbol).Msg("insert insider")
				continue
			}
			if ok {
				inserted++
			}
		}
		e.deps.Log.Info().Int("fetched", len(trades)).Int("inserted", inserted).Msg("insiders ingest")
		return nil
	})

	// WallStreetBets social rollup.
	run("wsb", 30*time.Second, func(cctx context.Context) error {
		posts, err := q.FetchWSB(cctx)
		if err != nil {
			return err
		}
		inserted := 0
		for i := range posts {
			ok, err := e.deps.Store.Social.Insert(cctx, &posts[i])
			if err != nil {
				continue
			}
			if ok {
				inserted++
			}
		}
		e.deps.Log.Info().Int("fetched", len(posts)).Int("inserted", inserted).Msg("wsb ingest")
		return nil
	})

	// Twitter follower rollup. Acts as LLM context, not an own
	// signal kind — the correlation with returns is too weak.
	run("twitter", 30*time.Second, func(cctx context.Context) error {
		posts, err := q.FetchTwitter(cctx)
		if err != nil {
			return err
		}
		inserted := 0
		for i := range posts {
			ok, err := e.deps.Store.Social.Insert(cctx, &posts[i])
			if err != nil {
				continue
			}
			if ok {
				inserted++
			}
		}
		e.deps.Log.Info().Int("fetched", len(posts)).Int("inserted", inserted).Msg("twitter ingest")
		return nil
	})

	// Corporate lobbying. 180-day lookback: slow-moving dataset.
	run("lobbying", 45*time.Second, func(cctx context.Context) error {
		events, err := q.FetchLobbying(cctx, time.Now().UTC().Add(-180*24*time.Hour))
		if err != nil {
			return err
		}
		inserted := 0
		for i := range events {
			ok, err := e.deps.Store.Lobbying.Insert(cctx, &events[i])
			if err != nil {
				continue
			}
			if ok {
				inserted++
			}
		}
		e.deps.Log.Info().Int("fetched", len(events)).Int("inserted", inserted).Msg("lobbying ingest")
		return nil
	})

	// Government contracts. 90-day lookback.
	run("gov_contracts", 45*time.Second, func(cctx context.Context) error {
		contracts, err := q.FetchGovContracts(cctx, time.Now().UTC().Add(-90*24*time.Hour))
		if err != nil {
			return err
		}
		inserted := 0
		for i := range contracts {
			ok, err := e.deps.Store.Contracts.Insert(cctx, &contracts[i])
			if err != nil {
				continue
			}
			if ok {
				inserted++
			}
		}
		e.deps.Log.Info().Int("fetched", len(contracts)).Int("inserted", inserted).Msg("gov contracts ingest")
		return nil
	})

	// Off-exchange short volume. Upsert by (symbol, day) so Quiver's
	// end-of-day restate of a prior row updates in place.
	run("offexchange", 30*time.Second, func(cctx context.Context) error {
		rows, err := q.FetchOffExchange(cctx)
		if err != nil {
			return err
		}
		upserted := 0
		for i := range rows {
			if err := e.deps.Store.Shorts.Upsert(cctx, &rows[i]); err != nil {
				continue
			}
			upserted++
		}
		e.deps.Log.Info().Int("fetched", len(rows)).Int("upserted", upserted).Msg("offexchange ingest")
		return nil
	})
}

func (e *Engine) regenerateSignals(ctx context.Context) error {
	// Purge expired signals before regenerating. Without this the
	// signals table grows unbounded (old rows never get refreshed, so
	// Upsert can't collapse them) and the unique index slowly fills
	// with tombstones.
	if n, err := e.deps.Store.Signals.PurgeExpired(ctx, time.Now().UTC()); err != nil {
		e.deps.Log.Warn().Err(err).Msg("purge expired signals")
	} else if n > 0 {
		e.deps.Log.Info().Int64("removed", n).Msg("purged expired signals")
	}
	// Opportunistic housekeeping: drop long-expired cooldowns so the
	// table doesn't grow unbounded. 24h is much longer than any
	// cooldown we install so it's always safe.
	if _, err := e.deps.Store.Cooldowns.PurgeExpired(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		e.deps.Log.Debug().Err(err).Msg("purge expired cooldowns")
	}

	// Pre-load politician track_weights so PoliticianFollow can apply
	// per-politician influence without hammering the DB once per trade.
	// Unknown names default to 1.0.
	weightBy := map[string]float64{}
	if pols, err := e.deps.Store.Politicians.List(ctx); err == nil {
		for _, p := range pols {
			w := p.TrackWeight
			if w <= 0 {
				w = 1.0
			}
			weightBy[strings.ToLower(strings.TrimSpace(p.Name))] = w
		}
	} else {
		e.deps.Log.Debug().Err(err).Msg("list politicians for weights")
	}

	pf := &strategy.PoliticianFollow{
		Recent: func(c context.Context, since time.Time) ([]domain.PoliticianTrade, error) {
			return e.deps.Store.PTrades.Since(c, since)
		},
		Weight: func(name string) float64 {
			if w, ok := weightBy[strings.ToLower(strings.TrimSpace(name))]; ok {
				return w
			}
			return 1.0
		},
		MinAmount:   1000,
		LookbackDur: 21 * 24 * time.Hour,
		HalfLife:    e.deps.PoliticianHalfLife,
	}
	ns := &strategy.NewsSentiment{
		Recent: func(c context.Context) ([]domain.NewsItem, error) {
			return e.deps.Store.News.Recent(c, 200)
		},
		MinRelevance: 0.3,
	}
	var all []domain.Signal
	if e.strategyEnabled(string(domain.SignalKindPolitician)) {
		if sigs, err := pf.Generate(ctx); err == nil {
			all = append(all, sigs...)
		} else {
			e.deps.Log.Warn().Err(err).Msg("politician strategy")
		}
	}
	if e.strategyEnabled(string(domain.SignalKindNews)) {
		if sigs, err := ns.Generate(ctx); err == nil {
			all = append(all, sigs...)
		} else {
			e.deps.Log.Warn().Err(err).Msg("news strategy")
		}
	}

	// Insider (Form 4) strategy. 30-day lookback matches the
	// ingest window; HalfLife 10d so a CEO buy from last week
	// still outweighs one from 3 weeks ago.
	if e.strategyEnabled(string(domain.SignalKindInsider)) {
		ins := &strategy.InsiderFollow{
			Recent: func(c context.Context, since time.Time) ([]domain.InsiderTrade, error) {
				return e.deps.Store.Insiders.Since(c, since)
			},
			LookbackDur: 30 * 24 * time.Hour,
			HalfLife:    10 * 24 * time.Hour,
			MinValueUSD: 25_000,
		}
		if sigs, err := ins.Generate(ctx); err == nil {
			all = append(all, sigs...)
		} else {
			e.deps.Log.Warn().Err(err).Msg("insider strategy")
		}
	}

	// WSB / social-buzz strategy. Short 24h lookback because the
	// half-life of retail attention is measured in hours, not days.
	if e.strategyEnabled(string(domain.SignalKindSocial)) {
		sb := &strategy.SocialBuzz{
			Recent: func(c context.Context, since time.Time) ([]domain.SocialPost, error) {
				return e.deps.Store.Social.Since(c, since)
			},
			LookbackDur: 24 * time.Hour,
			MinMentions: 100,
		}
		if sigs, err := sb.Generate(ctx); err == nil {
			all = append(all, sigs...)
		} else {
			e.deps.Log.Warn().Err(err).Msg("social strategy")
		}
	}

	// Momentum strategy runs last so its universe can include symbols
	// that the politician / news strategies just surfaced this tick.
	// The universe is the union of:
	//   - open positions (always re-evaluated for exit signals),
	//   - symbols already in `all` (fresh information flow),
	//   - the static watchlist (so we can discover new names without
	//     waiting for a politician disclosure or a news spike).
	if e.strategyEnabled(string(domain.SignalKindMomentum)) &&
		e.deps.MomentumEnabled && e.deps.Technicals != nil {
		universe := buildMomentumUniverse(ctx, e, all)
		mom := &strategy.Momentum{
			Technicals:    e.deps.Technicals,
			Universe:      func(_ context.Context) ([]string, error) { return universe, nil },
			MinConfidence: e.deps.MomentumMinConfidence,
		}
		if sigs, err := mom.Generate(ctx); err == nil {
			e.deps.Log.Debug().Int("universe", len(universe)).Int("emitted", len(sigs)).Msg("momentum generated")
			all = append(all, sigs...)
		} else {
			e.deps.Log.Warn().Err(err).Msg("momentum strategy")
		}
	}
	upserted := 0
	for i := range all {
		if err := e.deps.Store.Signals.Upsert(ctx, &all[i]); err != nil {
			e.deps.Log.Warn().Err(err).Msg("upsert signal")
			continue
		}
		upserted++
		if telemetry.App.SignalsGenerated != nil {
			telemetry.App.SignalsGenerated.Add(ctx, 1,
				metric.WithAttributes(attribute.String("kind", string(all[i].Kind))))
		}
	}
	e.deps.Log.Info().Int("generated", len(all)).Int("upserted", upserted).Msg("signals regenerated")
	return nil
}

// DecideAndTrade looks at active signals, picks top candidates, and trades.
//
// The candidate set is the union of:
//   - the top-N symbols by merged signal strength (new opportunities),
//   - every held position (so we can take profit or cut losers even
//     when no fresh signal points at them).
//
// Before any per-candidate work the loop checks whether the daily
// order cap has already been hit; if so the tick no-ops entirely
// rather than burning LLM tokens on guaranteed-reject evaluations.
func (e *Engine) DecideAndTrade(ctx context.Context) error {
	// Give the decide loop most of the interval budget so it has time
	// to evaluate all candidates through the LLM.
	timeout := e.deps.DecideInterval - 10*time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Daily-loss breaker runs before any work — cheapest way to
	// honour the kill-switch is to short-circuit the tick entirely
	// when the limit is breached. The check is idempotent; running
	// it on an already-disabled engine is a no-op.
	e.maybeTripDailyLossBreaker(dctx, e.deps.PortfolioID)
	if !e.Enabled() {
		e.deps.Log.Debug().Msg("decide skipped: engine disabled")
		return nil
	}

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	riskLimits := e.deps.Risk.GetLimits()
	if cap := riskLimits.MaxDailyOrders; cap > 0 {
		submitted, err := e.deps.Store.Orders.CountSubmittedSince(dctx, e.deps.PortfolioID, dayStart)
		if err != nil {
			e.deps.Log.Debug().Err(err).Msg("count submitted today")
		} else if submitted >= cap {
			e.deps.Log.Info().
				Int("submitted", submitted).
				Int("cap", cap).
				Msg("daily order cap already reached; skipping decide tick")
			return nil
		}
	}

	active, err := e.deps.Store.Signals.Active(dctx, "", time.Now().UTC())
	if err != nil {
		return fmt.Errorf("active signals: %w", err)
	}
	merged := strategy.Merge(active)

	// Apply the concentration penalty before candidate selection so
	// the per-kind / discovery / top-overall passes see the adjusted
	// leaderboard. Near-cap held symbols fall off the top and fresh
	// names rise. Held symbols are still guaranteed inclusion via
	// SelectCandidates' held pass — the penalty just stops them from
	// crowding out other kinds' top picks.
	positions, _ := e.deps.Store.Positions.List(dctx, e.deps.PortfolioID)
	maxPos := riskLimits.MaxPositionUSD
	held := map[string]bool{}
	fill := map[string]float64{}
	for _, p := range positions {
		if p.Quantity.IsZero() {
			continue
		}
		held[p.Symbol] = true
		if !maxPos.IsZero() {
			cost := p.Quantity.Mul(p.AvgCostCents.Dollars())
			frac, _ := cost.Div(maxPos).Float64()
			if frac > 0 {
				fill[p.Symbol] = frac
			}
		}
	}
	merged = strategy.ApplyConcentrationPenalty(merged, fill, 0)

	candidates := strategy.SelectCandidates(strategy.SelectParams{
		Merged:          merged,
		HeldSymbols:     held,
		PerKindCap:      e.deps.PerKindCap,
		DiscoverySlots:  e.deps.DiscoverySlots,
		MaxCandidates:   e.deps.WatchlistCap,
		ConfidenceFloor: e.deps.CandidateConfidenceFloor,
	})

	e.deps.Log.Debug().
		Int("merged", len(merged)).
		Int("candidates", len(candidates)).
		Int("held", len(held)).
		Int("per_kind_cap", e.deps.PerKindCap).
		Int("discovery_slots", e.deps.DiscoverySlots).
		Int("max_candidates", e.deps.WatchlistCap).
		Msg("decide: candidate selection")

	for _, m := range candidates {
		if err := e.evaluate(dctx, m); err != nil {
			e.deps.Log.Warn().Err(err).Str("symbol", m.Symbol).Msg("evaluate")
		}
	}
	return nil
}

// evaluate handles a single symbol end-to-end.
//
// Behaviour differs based on whether we already hold the symbol:
//   - Held positions are always evaluated: a take-profit / stop-loss
//     check runs unconditionally (even through cooldowns), and the
//     LLM/heuristic path sees the current unrealized P&L so it can
//     reason about exits.
//   - Unheld candidates must clear the confidence floor (0.35); weak
//     signals aren't worth a trade.
//
// Symbols inside an active cooldown window are skipped unless the
// exit-policy fires — the cooldown exists precisely because we just
// rejected a trade for this symbol and don't want to re-evaluate it
// every tick.
func (e *Engine) evaluate(ctx context.Context, m strategy.Merged) error {
	if strings.TrimSpace(m.Symbol) == "" {
		return nil
	}
	portfolio, err := e.deps.Store.Portfolios.Get(ctx, e.deps.PortfolioID)
	if err != nil {
		return fmt.Errorf("portfolio: %w", err)
	}
	quote, err := e.deps.Market.Quote(ctx, m.Symbol)
	if err != nil {
		return fmt.Errorf("quote: %w", err)
	}
	pos, err := e.deps.Store.Positions.Get(ctx, portfolio.ID, m.Symbol)
	if err != nil {
		return fmt.Errorf("position: %w", err)
	}
	positionQty := decimal.Zero
	avgCost := decimal.Zero
	held := pos != nil && pos.Quantity.GreaterThan(decimal.Zero)
	if held {
		positionQty = pos.Quantity
		avgCost = pos.AvgCostCents.Dollars()
	}

	// Short-circuit: for held positions check TP/SL first. If a hard
	// threshold is hit we force a sell regardless of signal/LLM
	// opinion OR cooldown state — losing positions must always be
	// cuttable. The risk engine still gets the final say on sizing.
	if held && e.deps.ExitPolicy.TakeProfitPct+e.deps.ExitPolicy.StopLossPct > 0 {
		if action, ok := e.deps.ExitPolicy.Evaluate(avgCost, quote.Price); ok {
			d := &domain.Decision{
				ID:          uuid.NewString(),
				PortfolioID: portfolio.ID,
				Symbol:      m.Symbol,
				Action:      action,
				Score:       -1,
				Confidence:  1,
				TargetUSD:   positionQty.Mul(quote.Price), // close entire position
				Reasoning:   fmt.Sprintf("Exit policy fired: avg=$%s price=$%s", avgCost.StringFixed(2), quote.Price.StringFixed(2)),
				ModelUsed:   "exit_policy",
				SignalIDs:   "[]",
			}
			if err := e.deps.Store.Decisions.Insert(ctx, d); err != nil {
				return err
			}
			if telemetry.App.DecisionsMade != nil {
				telemetry.App.DecisionsMade.Add(ctx, 1, metric.WithAttributes(
					attribute.String("action", string(action)),
					attribute.String("symbol", m.Symbol),
					attribute.String("model", "exit_policy")))
			}
			if err := e.Execute(ctx, d); err != nil && !errors.Is(err, domain.ErrRiskRejected) {
				return err
			}
			return nil
		}
	}

	// Cooldown gate. Installed when we just rejected a trade for this
	// symbol; re-evaluating every tick burns tokens and spams the
	// decision log. ExitPolicy above already ran, so held positions
	// can still be cut even while cooling down.
	if cd, err := e.deps.Store.Cooldowns.ActiveFor(ctx, portfolio.ID, m.Symbol, time.Now().UTC()); err == nil && cd != nil {
		e.deps.Log.Debug().
			Str("symbol", m.Symbol).
			Time("until", cd.Until).
			Str("reason", cd.Reason).
			Msg("skip: symbol in cooldown")
		return nil
	}

	// Candidates we don't already own must clear the confidence gate.
	// For held positions we always keep evaluating so the LLM/heuristic
	// gets a shot at deciding to exit or trim.
	if !held && m.Confidence < 0.35 {
		return nil
	}

	// Short-circuit: if the merged signal is bullish but the position
	// is already full (at MaxPositionUSD), there's nothing to do — the
	// risk engine would reject the buy anyway, and asking the LLM is a
	// guaranteed-waste token spend. We'd still fall through if the
	// dominant side were sell or the signal were weak, because those
	// inform exit decisions. Save the LLM call + a guaranteed rejection.
	//
	// We also treat "near the cap" the same as "at the cap" — if the
	// remaining headroom is below a meaningful trade size the risk
	// engine will just size the order down to $0.x and reject it with
	// "below minimum notional" every 30 minutes forever. The threshold
	// is max($10, 2% of MaxOrderUSD) so tiny wallet configs still work.
	riskLimits := e.deps.Risk.GetLimits()
	if held && m.DominantSide == domain.SideBuy && !riskLimits.MaxPositionUSD.IsZero() {
		currentCost := positionQty.Mul(avgCost)
		minStep := minTradeStep(riskLimits.MaxOrderUSD)
		headroom := riskLimits.MaxPositionUSD.Sub(currentCost)
		if headroom.LessThanOrEqual(minStep) {
			e.deps.Log.Debug().
				Str("symbol", m.Symbol).
				Str("cost_basis", currentCost.StringFixed(2)).
				Str("cap", riskLimits.MaxPositionUSD.StringFixed(2)).
				Str("headroom", headroom.StringFixed(2)).
				Str("min_step", minStep.StringFixed(2)).
				Msg("skip buy: position at or near cap")
			// 6h is short enough to catch intraday signal flips but
			// long enough to stop the per-tick log spam that happens
			// when a position is pinned against MaxPositionUSD.
			_ = e.installCooldown(ctx, portfolio.ID, m.Symbol, 6*time.Hour, "position at max")
			e.recordRejection(ctx, storage.Rejection{
				PortfolioID: portfolio.ID,
				Symbol:      m.Symbol,
				Side:        string(domain.SideBuy),
				Source:      storage.RejectionSourceEngine,
				Reason:      "position at max (near-cap short-circuit)",
			})
			return nil
		}
	}

	// Ask the LLM for a decision when available, else fall back to a
	// deterministic heuristic so the system works end-to-end offline.
	action := heuristicAction(m, held)
	rationaleText := fmt.Sprintf("Heuristic from %d signals; net score %.2f, confidence %.2f", len(m.Rationale), m.Score, m.Confidence)
	modelUsed := "heuristic"
	targetUSD := heuristicTarget(m, riskLimits.MaxOrderUSD)
	// Default sell target is the full position value. Without this the
	// sell sizing would be capped by MaxOrderUSD just like a buy, which
	// tends to leave slow-bleeding positions open for weeks.
	if action == domain.DecisionActionSell && held {
		targetUSD = positionQty.Mul(quote.Price)
	}

	if e.deps.LLM != nil && e.deps.LLM.Available() {
		req := llm.DecideRequest{
			Symbol:           m.Symbol,
			CurrentPrice:     quote.Price.StringFixed(2),
			PositionQty:      positionQty.String(),
			PositionAvgCost:  avgCost.StringFixed(2),
			CashAvailableUSD: portfolio.AvailableCents().Dollars().StringFixed(2),
			MaxOrderUSD:      riskLimits.MaxOrderUSD.StringFixed(2),
			Signals:          m.Rationale,
		}
		recent, _ := e.deps.Store.News.Recent(ctx, 5)
		req.RecentNews = recent
		ptrades, _ := e.deps.Store.PTrades.BySymbol(ctx, m.Symbol, time.Now().Add(-60*24*time.Hour))
		if len(ptrades) > 5 {
			ptrades = ptrades[:5]
		}
		req.RecentPoliticians = ptrades

		// Wave 4 alt-data context. Each lookup has its own bounded
		// fallback: a failed repo call drops only that slice, it
		// never blocks the LLM decision. Caps keep the prompt
		// from ballooning on noisy tickers.
		if e.deps.Store.Insiders != nil {
			if ins, err := e.deps.Store.Insiders.BySymbol(ctx, m.Symbol, time.Now().Add(-60*24*time.Hour)); err == nil {
				if len(ins) > 6 {
					ins = ins[:6]
				}
				req.RecentInsiders = ins
			}
		}
		if e.deps.Store.Social != nil {
			if sp, err := e.deps.Store.Social.BySymbol(ctx, m.Symbol, time.Now().Add(-48*time.Hour)); err == nil {
				if len(sp) > 6 {
					sp = sp[:6]
				}
				req.RecentSocial = sp
			}
		}
		if e.deps.Store.Lobbying != nil {
			if lo, err := e.deps.Store.Lobbying.BySymbol(ctx, m.Symbol, time.Now().Add(-180*24*time.Hour)); err == nil {
				if len(lo) > 4 {
					lo = lo[:4]
				}
				req.RecentLobbying = lo
			}
		}
		if e.deps.Store.Contracts != nil {
			if gc, err := e.deps.Store.Contracts.BySymbol(ctx, m.Symbol, time.Now().Add(-90*24*time.Hour)); err == nil {
				if len(gc) > 4 {
					gc = gc[:4]
				}
				req.RecentContracts = gc
			}
		}
		if e.deps.Store.Shorts != nil {
			if sv, err := e.deps.Store.Shorts.LatestBySymbol(ctx, m.Symbol); err == nil {
				req.ShortInterest = sv
			}
		}

		// Inject a technical snapshot when we can get one so the LLM
		// can weigh price structure alongside sentiment. Failures are
		// silent — a missing technical is strictly less information,
		// never a reason to skip the decision.
		if e.deps.Technicals != nil {
			if snap, err := e.deps.Technicals.Snapshot(ctx, m.Symbol); err == nil && snap != nil {
				tc := &llm.TechnicalContext{Price: snap.Price.StringFixed(2)}
				if snap.HasSMA20 {
					tc.SMA20 = snap.SMA20.StringFixed(2)
				}
				if snap.HasSMA50 {
					tc.SMA50 = snap.SMA50.StringFixed(2)
				}
				if snap.HasRSI14 {
					tc.RSI14 = snap.RSI14
				}
				if snap.Has52wk {
					tc.Hi52 = snap.Hi52.StringFixed(2)
					tc.Lo52 = snap.Lo52.StringFixed(2)
				}
				tc.Chg1d = snap.Chg1d
				tc.Chg5d = snap.Chg5d
				tc.Chg30d = snap.Chg30d
				req.Technicals = tc
			}
		}

		// lctx = LLM context. We give the LLM call a dedicated 30s timeout
		// so it can't eat the entire tick budget if the provider hangs.
		lctx, lcancel := context.WithTimeout(ctx, 30*time.Second)
		lctx = llm.WithOperation(lctx, "engine.decide")
		rat, model, err := e.deps.LLM.Decide(lctx, req)
		lcancel()
		if err != nil {
			// Note: the error here is the aggregated chain across
			// primary + all fallback models. A "model not found" at
			// the tail means every earlier model also failed — check
			// the full chain in the error string, not just the last
			// entry. The deterministic fallback path below still runs.
			e.deps.Log.Warn().Err(err).Msg("llm decide failed across all models; using deterministic fallback")
		} else {
			switch rat.Action {
			case "buy":
				action = domain.DecisionActionBuy
			case "sell":
				action = domain.DecisionActionSell
			default:
				action = domain.DecisionActionHold
			}
			rationaleText = rat.Reasoning
			if rat.TargetUSD > 0 {
				targetUSD = decimal.NewFromFloat(rat.TargetUSD)
			} else if action == domain.DecisionActionSell && held {
				// LLM didn't specify a target — default to full exit so
				// sells aren't silently capped by MaxOrderUSD.
				targetUSD = positionQty.Mul(quote.Price)
			}
			modelUsed = model
		}
	}

	sigIDs := make([]string, 0, len(m.Rationale))
	for _, s := range m.Rationale {
		sigIDs = append(sigIDs, s.ID)
	}
	idJSON, _ := json.Marshal(sigIDs)

	decision := &domain.Decision{
		ID:          uuid.NewString(),
		PortfolioID: portfolio.ID,
		Symbol:      m.Symbol,
		Action:      action,
		Score:       m.Score,
		Confidence:  m.Confidence,
		TargetUSD:   targetUSD,
		Reasoning:   rationaleText,
		ModelUsed:   modelUsed,
		SignalIDs:   string(idJSON),
	}
	if err := e.deps.Store.Decisions.Insert(ctx, decision); err != nil {
		return err
	}
	if telemetry.App.DecisionsMade != nil {
		telemetry.App.DecisionsMade.Add(ctx, 1, metric.WithAttributes(
			attribute.String("action", string(action)),
			attribute.String("symbol", m.Symbol),
			attribute.String("model", modelUsed),
		))
	}
	if action == domain.DecisionActionHold {
		return nil
	}
	if err := e.Execute(ctx, decision); err != nil {
		if errors.Is(err, domain.ErrRiskRejected) {
			// Surface the actual reason ("position at max", "below
			// minimum notional", etc.) so the operator can tell why
			// the engine didn't trade — previously this was a
			// content-free "risk rejected" that hid churn.
			e.deps.Log.Info().
				Str("symbol", m.Symbol).
				Str("action", string(action)).
				Err(err).
				Msg("risk rejected")
			return nil
		}
		return err
	}
	return nil
}

// Execute validates risk and submits the order.
//
// All cash reservation bookkeeping is structured around defer so any
// exit path — risk-rejected, broker-rejected, DB error — releases
// whatever was reserved at the start. Without this the reservation
// slowly leaks and AvailableCents drifts below actual cash.
func (e *Engine) Execute(ctx context.Context, d *domain.Decision) error {
	portfolio, err := e.deps.Store.Portfolios.Get(ctx, d.PortfolioID)
	if err != nil {
		return err
	}
	quote, err := e.deps.Market.Quote(ctx, d.Symbol)
	if err != nil {
		return err
	}
	pos, err := e.deps.Store.Positions.Get(ctx, portfolio.ID, d.Symbol)
	if err != nil {
		return err
	}
	positionQty := decimal.Zero
	avgCost := decimal.Zero
	if pos != nil {
		positionQty = pos.Quantity
		avgCost = pos.AvgCostCents.Dollars()
	}
	// Approx equity: cash + sum(positions * avg). While we're here
	// count the distinct open positions so the risk engine can
	// enforce MAX_CONCURRENT_POSITIONS on new-symbol buys.
	positions, _ := e.deps.Store.Positions.List(ctx, portfolio.ID)
	equity := portfolio.CashCents.Dollars()
	openPositions := 0
	for _, p := range positions {
		if p.Quantity.IsZero() {
			continue
		}
		openPositions++
		equity = equity.Add(p.Quantity.Mul(p.AvgCostCents.Dollars()))
	}
	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	ordersToday, _ := e.deps.Store.Orders.CountSubmittedSince(ctx, portfolio.ID, dayStart)

	// Daily realized P&L. Feeds MAX_DAILY_LOSS_USD — without this the
	// daily-loss circuit breaker was ineffective.
	realizedToday, _ := e.deps.Store.Realized.SumSince(ctx, portfolio.ID, dayStart)

	side := domain.SideBuy
	if d.Action == domain.DecisionActionSell {
		side = domain.SideSell
	}
	req := risk.Request{
		Symbol:           d.Symbol,
		Side:             side,
		TargetUSD:        d.TargetUSD,
		Price:            quote.Price,
		PortfolioCash:    portfolio.AvailableCents().Dollars(),
		PortfolioEquity:  equity,
		PositionQty:      positionQty,
		PositionAvgCost:  avgCost,
		OrdersToday:      ordersToday,
		RealizedPnLToday: realizedToday.Dollars(),
		OpenPositions:    openPositions,
	}
	result := e.deps.Risk.Approve(ctx, req)
	if !result.Approved {
		e.deps.Store.Audit.Record(ctx, "decision", d.ID, "risk_rejected", result.Reason)
		telemetry.App.RecordOrder(ctx, "rejected", d.Symbol, string(side))
		_ = e.installCooldownFor(ctx, portfolio.ID, d.Symbol, result.Reason)
		decisionID := d.ID
		e.recordRejection(ctx, storage.Rejection{
			PortfolioID: portfolio.ID,
			Symbol:      d.Symbol,
			DecisionID:  &decisionID,
			Side:        string(side),
			Source:      storage.RejectionSourceRisk,
			Reason:      result.Reason,
			TargetUSD:   d.TargetUSD,
		})
		return fmt.Errorf("%w: %s", domain.ErrRiskRejected, result.Reason)
	}

	// Reserve buying power up-front; release on every exit path via
	// defer. The defer uses context.Background() so a cancelled
	// request context cannot orphan a reservation.
	//
	// Reservation lifecycle: reserve on submit -> release once the
	// order is terminal (filled / partial / rejected / cancelled).
	// Actual cash debit happens inside applyFill, so reservation and
	// cash are mutually exclusive accounts — a buy moves money from
	// "cash" to "spent", never from "reserved" to "spent".
	var reservedNotional domain.Money
	if side == domain.SideBuy {
		reservedNotional = domain.NewMoneyFromDecimal(result.Notional)
		if err := e.deps.Store.Portfolios.AddReservation(ctx, portfolio.ID, reservedNotional); err != nil {
			return fmt.Errorf("reserve cash: %w", err)
		}
	}
	defer func() {
		if reservedNotional > 0 {
			if err := e.deps.Store.Portfolios.ReleaseReservation(context.Background(), portfolio.ID, reservedNotional); err != nil {
				e.deps.Log.Warn().Err(err).Str("symbol", d.Symbol).Msg("release reservation")
			}
		}
	}()

	order := &domain.Order{
		ID:          uuid.NewString(),
		PortfolioID: portfolio.ID,
		Symbol:      d.Symbol,
		Side:        side,
		Type:        domain.OrderTypeMarket,
		TimeInForce: domain.TIFDay,
		Quantity:    result.Quantity,
		Status:      domain.OrderStatusPending,
		Reason:      d.Reasoning,
		DecisionID:  &d.ID,
	}
	if err := e.deps.Store.Orders.Create(ctx, order); err != nil {
		return err
	}
	bo, err := e.deps.Broker.SubmitOrder(ctx, order)
	if err != nil {
		order.Status = domain.OrderStatusRejected
		order.Reason = err.Error()
		_ = e.deps.Store.Orders.UpdateStatus(ctx, order)
		telemetry.App.RecordOrder(ctx, "rejected", d.Symbol, string(side))
		_ = e.installCooldownFor(ctx, portfolio.ID, d.Symbol, "broker rejected: "+err.Error())
		decisionID := d.ID
		e.recordRejection(ctx, storage.Rejection{
			PortfolioID: portfolio.ID,
			Symbol:      d.Symbol,
			DecisionID:  &decisionID,
			Side:        string(side),
			Source:      storage.RejectionSourceBroker,
			Reason:      err.Error(),
			TargetUSD:   d.TargetUSD,
		})
		return err
	}
	telemetry.App.RecordOrder(ctx, "submitted", d.Symbol, string(side))
	order.BrokerID = bo.BrokerID
	order.Status = bo.Status
	order.FilledQty = bo.FilledQty
	order.FilledAvgCents = domain.NewMoneyFromDecimal(bo.FilledAvg)
	if !bo.SubmittedAt.IsZero() {
		t := bo.SubmittedAt
		order.SubmittedAt = &t
	}
	if bo.FilledAt != nil {
		order.FilledAt = bo.FilledAt
	}
	if err := e.deps.Store.Orders.UpdateStatus(ctx, order); err != nil {
		return err
	}
	// Apply fills to internal wallet/positions. The cash debit happens
	// inside applyFill; reservation release happens unconditionally
	// below via the defer so there is exactly one accounting path for
	// every outcome (fill, partial, terminal rejection).
	if bo.Status == domain.OrderStatusFilled || bo.Status == domain.OrderStatusPartial {
		if err := e.applyFill(ctx, portfolio.ID, order, bo, avgCost); err != nil {
			e.deps.Log.Warn().Err(err).Msg("apply fill")
		}
		telemetry.App.RecordOrder(ctx, "filled", d.Symbol, string(side))
	}
	_ = e.deps.Store.Decisions.SetExecuted(ctx, d.ID, order.ID)
	e.deps.Store.Audit.Record(ctx, "order", order.ID, "submitted", fmt.Sprintf("%s %s qty=%s at %s", side, order.Symbol, order.Quantity, bo.FilledAvg))
	return nil
}

// applyFill mutates cash, positions and the realized-events log for a
// single fill. Extracted from Execute so the reservation bookkeeping
// stays readable. `prevAvgCost` is the position's average cost before
// the fill was applied, used to compute realized P&L on sell fills.
func (e *Engine) applyFill(ctx context.Context, portfolioID string, order *domain.Order, bo *broker.BrokerOrder, prevAvgCost decimal.Decimal) error {
	priceCents := domain.NewMoneyFromDecimal(bo.FilledAvg)
	fillNotional := domain.NewMoneyFromDecimal(bo.FilledQty.Mul(bo.FilledAvg))
	switch order.Side {
	case domain.SideBuy:
		if err := e.deps.Store.Portfolios.UpdateCash(ctx, portfolioID, -fillNotional, 0); err != nil {
			return err
		}
	case domain.SideSell:
		if err := e.deps.Store.Portfolios.UpdateCash(ctx, portfolioID, fillNotional, 0); err != nil {
			return err
		}
		// Realized P&L = (fill_price - pre_trade_avg_cost) * qty.
		// Compute BEFORE Apply mutates the stored avg cost; that's
		// why we capture prevAvgCost at call-site.
		realized := bo.FilledAvg.Sub(prevAvgCost).Mul(bo.FilledQty)
		ev := &storage.RealizedEvent{
			PortfolioID:   portfolioID,
			Symbol:        order.Symbol,
			Quantity:      bo.FilledQty,
			RealizedCents: domain.NewMoneyFromDecimal(realized),
			OrderID:       &order.ID,
		}
		if err := e.deps.Store.Realized.Insert(ctx, ev); err != nil {
			e.deps.Log.Warn().Err(err).Msg("record realized event")
		}
	}
	if _, err := e.deps.Store.Positions.Apply(ctx, portfolioID, order.Symbol, order.Side, bo.FilledQty, priceCents); err != nil {
		return err
	}
	return nil
}

// recordRejection persists one structured rejection row and is a
// strict superset of the free-form audit-log line we already emit.
// The call is best-effort: persist failure must never propagate up
// to kill an execute / reconcile cycle. All fields are optional
// except portfolioID, symbol, source, and reason.
func (e *Engine) recordRejection(ctx context.Context, rj storage.Rejection) {
	if rj.PortfolioID == "" || rj.Symbol == "" || rj.Source == "" || rj.Reason == "" {
		return
	}
	// Detach from caller context so a cancelled request still
	// writes the row — rejection history is a forensic log, we
	// want it even when the request is going away.
	bctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.deps.Store.Rejections.Insert(bctx, &rj); err != nil {
		e.deps.Log.Warn().Err(err).Str("symbol", rj.Symbol).Msg("rejection log insert")
	}
	// Circuit breaker: too many broker rejects in the rolling
	// window auto-disables the engine. Engine / risk rejections
	// don't trip the breaker — those are business-as-usual
	// (blacklist, daily cap, cooldown). Only broker-side failures
	// (5xx, 422, etc) indicate the upstream is sick.
	if rj.Source == storage.RejectionSourceBroker {
		e.maybeTripBrokerBreaker(bctx, rj.PortfolioID)
	}
}

// maybeTripBrokerBreaker counts broker rejections in the breaker
// window and disables the engine when the threshold is exceeded. The
// check is idempotent — tripping on an already-disabled engine is a
// no-op except for the audit line.
func (e *Engine) maybeTripBrokerBreaker(ctx context.Context, portfolioID string) {
	if e.deps.AutoDisableBrokerRejects <= 0 || e.deps.AutoDisableBrokerWindow <= 0 {
		return
	}
	since := time.Now().UTC().Add(-e.deps.AutoDisableBrokerWindow)
	n, err := e.deps.Store.Rejections.CountSince(ctx, portfolioID, storage.RejectionSourceBroker, since)
	if err != nil {
		e.deps.Log.Debug().Err(err).Msg("breaker: count rejections")
		return
	}
	if n <= e.deps.AutoDisableBrokerRejects {
		return
	}
	if !e.Enabled() {
		return
	}
	e.SetEnabled(false)
	reason := fmt.Sprintf("broker-reject circuit breaker tripped: %d rejects in last %s",
		n, e.deps.AutoDisableBrokerWindow)
	e.deps.Log.Error().Str("reason", reason).Msg("engine auto-disabled")
	e.deps.Store.Audit.Record(ctx, "engine", portfolioID, "auto_disabled", reason)
}

// maybeTripDailyLossBreaker flips the engine off when realized P&L
// since UTC midnight falls below -AutoDisableDailyLossUSD. This is a
// softer kill-switch than MAX_DAILY_LOSS_USD: the risk engine blocks
// one particular trade, this one stops the loop entirely until an
// operator re-enables it from the UI.
func (e *Engine) maybeTripDailyLossBreaker(ctx context.Context, portfolioID string) {
	if e.deps.AutoDisableDailyLossUSD.IsZero() {
		return
	}
	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	realized, err := e.deps.Store.Realized.SumSince(ctx, portfolioID, dayStart)
	if err != nil {
		return
	}
	if realized.Dollars().GreaterThanOrEqual(e.deps.AutoDisableDailyLossUSD.Neg()) {
		return
	}
	if !e.Enabled() {
		return
	}
	e.SetEnabled(false)
	reason := fmt.Sprintf("daily-loss circuit breaker tripped: realized=%s vs limit=%s",
		realized.Dollars().StringFixed(2), e.deps.AutoDisableDailyLossUSD.Neg().StringFixed(2))
	e.deps.Log.Error().Str("reason", reason).Msg("engine auto-disabled")
	e.deps.Store.Audit.Record(ctx, "engine", portfolioID, "auto_disabled", reason)
}

// strategyEnabled reports whether the named strategy should run this
// process. Empty `Strategies` slice ⇒ everything enabled (default).
// The comparison is case-insensitive and ignores whitespace so
// operators can write `STRATEGIES="politician, news"`.
func (e *Engine) strategyEnabled(name string) bool {
	if len(e.deps.Strategies) == 0 {
		return true
	}
	for _, s := range e.deps.Strategies {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return true
		}
	}
	return false
}

// installCooldownFor picks a cooldown duration based on the rejection
// reason and installs it. Reasons that are "stop trading for the day"
// (daily cap, daily loss) extend until next UTC midnight; reasons that
// amount to "no room left on this symbol" get a 6-hour cooldown so
// we're not re-evaluating the same capped symbol every decide tick;
// everything else gets the configured default (30 min by default).
func (e *Engine) installCooldownFor(ctx context.Context, portfolioID, symbol, reason string) error {
	lreason := strings.ToLower(reason)
	// Truncate the reason so we don't persist unbounded strings.
	if len(reason) > 256 {
		reason = reason[:256] + "…"
	}
	dur := e.deps.CooldownDuration
	if dur <= 0 {
		dur = 30 * time.Minute
	}
	switch {
	case strings.Contains(lreason, "daily order cap"),
		strings.Contains(lreason, "daily loss"):
		nextMidnight := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		dur = time.Until(nextMidnight)
	case strings.Contains(lreason, "below minimum notional"),
		strings.Contains(lreason, "no headroom"),
		strings.Contains(lreason, "position at max"),
		strings.Contains(lreason, "symbol exposure cap"),
		strings.Contains(lreason, "quantity rounds to zero"):
		// "No room left on this symbol" — the next tick will produce
		// the exact same rejection if we don't back off. 6 hours
		// matches the near-cap short-circuit so behaviour is
		// consistent regardless of which code path caught it.
		if dur < 6*time.Hour {
			dur = 6 * time.Hour
		}
	}
	return e.installCooldown(ctx, portfolioID, symbol, dur, reason)
}

// minTradeStep computes the smallest meaningful headroom for a new
// buy. Anything below this threshold gets bucketed as "at cap" because
// the risk engine's per-order min-notional guard ($1) plus realistic
// fractional-share sizing will just reject it anyway. We pick
// max($10, 2% of MaxOrderUSD) so ridiculously small MaxOrderUSD
// configs don't get stuck in a paradox, and normal configs ($500
// MaxOrderUSD) get a sensible $10 floor.
func minTradeStep(maxOrderUSD decimal.Decimal) decimal.Decimal {
	floor := decimal.NewFromInt(10)
	if maxOrderUSD.IsZero() {
		return floor
	}
	pct := maxOrderUSD.Mul(decimal.NewFromFloat(0.02))
	if pct.GreaterThan(floor) {
		return pct
	}
	return floor
}

// installCooldown persists a cooldown with the given duration. A
// non-positive duration is ignored.
func (e *Engine) installCooldown(ctx context.Context, portfolioID, symbol string, dur time.Duration, reason string) error {
	if dur <= 0 {
		return nil
	}
	return e.deps.Store.Cooldowns.Upsert(ctx, &storage.Cooldown{
		PortfolioID: portfolioID,
		Symbol:      symbol,
		Until:       time.Now().UTC().Add(dur),
		Reason:      reason,
	})
}

// Reconcile polls the broker for open orders + positions and updates
// the DB to match. Pending/submitted orders that have since filled,
// cancelled or been rejected are progressed to their terminal state;
// fills we missed are applied to positions. Safe no-op for brokers
// whose orders fill synchronously (e.g. MockBroker).
func (e *Engine) Reconcile(ctx context.Context) error {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	open, err := e.deps.Store.Orders.ListOpen(rctx, e.deps.PortfolioID, 200)
	if err != nil {
		return fmt.Errorf("list open orders: %w", err)
	}
	updated := 0
	filled := 0
	for i := range open {
		o := &open[i]
		bo, err := e.deps.Broker.GetOrder(rctx, o.BrokerID)
		if err != nil {
			e.deps.Log.Debug().Err(err).Str("order", o.ID).Str("broker_id", o.BrokerID).Msg("reconcile get_order")
			continue
		}
		if bo.Status == o.Status && bo.FilledQty.Equal(o.FilledQty) {
			continue
		}
		prevStatus := o.Status
		prevFilled := o.FilledQty
		o.Status = bo.Status
		o.FilledQty = bo.FilledQty
		o.FilledAvgCents = domain.NewMoneyFromDecimal(bo.FilledAvg)
		if !bo.SubmittedAt.IsZero() && o.SubmittedAt == nil {
			t := bo.SubmittedAt
			o.SubmittedAt = &t
		}
		if bo.FilledAt != nil {
			o.FilledAt = bo.FilledAt
		}
		if err := e.deps.Store.Orders.UpdateStatus(rctx, o); err != nil {
			e.deps.Log.Warn().Err(err).Str("order", o.ID).Msg("reconcile update status")
			continue
		}
		updated++

		// Newly-filled delta. Compute from the qty increment so partial
		// progress is handled correctly across multiple reconcile ticks.
		deltaQty := bo.FilledQty.Sub(prevFilled)
		if deltaQty.GreaterThan(decimal.Zero) && (bo.Status == domain.OrderStatusFilled || bo.Status == domain.OrderStatusPartial) {
			// Capture the pre-trade avg cost for realized P&L on sells.
			pos, _ := e.deps.Store.Positions.Get(rctx, e.deps.PortfolioID, o.Symbol)
			prevAvg := decimal.Zero
			if pos != nil {
				prevAvg = pos.AvgCostCents.Dollars()
			}
			deltaFill := *bo
			deltaFill.FilledQty = deltaQty
			if err := e.applyFill(rctx, e.deps.PortfolioID, o, &deltaFill, prevAvg); err != nil {
				e.deps.Log.Warn().Err(err).Str("order", o.ID).Msg("reconcile apply fill")
			} else {
				filled++
			}
		}

		// Broker-side rejections release the reservation and install
		// a cooldown so we don't instantly resubmit.
		if bo.Status == domain.OrderStatusRejected && prevStatus != domain.OrderStatusRejected {
			reason := bo.Reason
			if reason == "" {
				reason = "broker rejected"
			}
			_ = e.installCooldownFor(rctx, e.deps.PortfolioID, o.Symbol, "broker rejected: "+reason)
			e.recordRejection(rctx, storage.Rejection{
				PortfolioID: e.deps.PortfolioID,
				Symbol:      o.Symbol,
				Side:        string(o.Side),
				Source:      storage.RejectionSourceBroker,
				Reason:      reason,
			})
		}
	}

	// Stale-order sweep: any non-terminal order that has been in
	// flight longer than OrderStaleTimeout is cancelled at the broker
	// (best-effort) and marked cancelled locally. Without this, an
	// order silently stuck on the broker side keeps its cash
	// reserved until a human notices. The sweep runs last so
	// reconcile's normal status-sync path gets first crack at any
	// in-flight updates.
	if e.deps.OrderStaleTimeout > 0 {
		e.sweepStaleOrders(rctx)
	}
	if updated > 0 {
		e.deps.Log.Info().Int("updated", updated).Int("filled", filled).Msg("reconcile orders")
	}

	// Position drift check. Not strictly required but extremely useful
	// as an early warning that DB <-> broker accounting has diverged.
	if positions, err := e.deps.Broker.Positions(rctx); err == nil {
		dbMap := map[string]decimal.Decimal{}
		ps, _ := e.deps.Store.Positions.List(rctx, e.deps.PortfolioID)
		for _, p := range ps {
			dbMap[p.Symbol] = p.Quantity
		}
		for _, bp := range positions {
			dbQty := dbMap[bp.Symbol]
			if !dbQty.Equal(bp.Quantity) {
				e.deps.Log.Warn().
					Str("symbol", bp.Symbol).
					Str("db_qty", dbQty.String()).
					Str("broker_qty", bp.Quantity.String()).
					Msg("position drift detected")
			}
			delete(dbMap, bp.Symbol)
		}
		for sym, qty := range dbMap {
			if qty.GreaterThan(decimal.Zero) {
				e.deps.Log.Warn().
					Str("symbol", sym).
					Str("db_qty", qty.String()).
					Msg("position in DB but not at broker")
			}
		}
	}
	return nil
}

// sweepStaleOrders cancels orders that have been in a non-terminal
// state past `OrderStaleTimeout`. Broker cancel errors are logged
// and otherwise ignored — the DB row is still flipped to cancelled
// so the reservation bookkeeping is released and the decide loop
// can re-attempt the symbol next tick (cooldowns still apply).
//
// Intentionally separate from the main reconcile body so tests can
// exercise it in isolation without having to seed live broker state
// for every order.
func (e *Engine) sweepStaleOrders(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-e.deps.OrderStaleTimeout)
	open, err := e.deps.Store.Orders.ListOpen(ctx, e.deps.PortfolioID, 200)
	if err != nil {
		e.deps.Log.Debug().Err(err).Msg("stale sweep: list open")
		return
	}
	for i := range open {
		o := &open[i]
		// Prefer submitted_at (the broker accepted it), fall back
		// to created_at for pre-submit rows.
		ref := o.CreatedAt
		if o.SubmittedAt != nil && !o.SubmittedAt.IsZero() {
			ref = *o.SubmittedAt
		}
		if ref.After(cutoff) {
			continue
		}
		// Cancel at broker (best-effort); then mark locally.
		if o.BrokerID != "" {
			if cErr := e.deps.Broker.CancelOrder(ctx, o.BrokerID); cErr != nil {
				e.deps.Log.Warn().Err(cErr).
					Str("order", o.ID).
					Str("broker_id", o.BrokerID).
					Msg("stale sweep: broker cancel failed")
			}
		}
		prev := o.Status
		o.Status = domain.OrderStatusCancelled
		o.Reason = fmt.Sprintf("stale: %s in %s for > %s", prev, ref.Format(time.RFC3339), e.deps.OrderStaleTimeout)
		if err := e.deps.Store.Orders.UpdateStatus(ctx, o); err != nil {
			e.deps.Log.Warn().Err(err).Str("order", o.ID).Msg("stale sweep: update status")
			continue
		}
		// Release any outstanding buy-side reservation. We don't
		// have the exact reserved amount on the row but the
		// portfolio level already tracks totals, so a zero-op
		// release is safe: we just install a cooldown on the
		// symbol so the decide loop doesn't instantly re-fire.
		_ = e.installCooldown(ctx, e.deps.PortfolioID, o.Symbol, e.deps.OrderStaleTimeout, "order stale cancelled")
		e.deps.Store.Audit.Record(ctx, "order", o.ID, "cancelled_stale", o.Reason)
		e.recordRejection(ctx, storage.Rejection{
			PortfolioID: e.deps.PortfolioID,
			Symbol:      o.Symbol,
			Side:        string(o.Side),
			Source:      storage.RejectionSourceEngine,
			Reason:      o.Reason,
		})
	}
}

// truncate clamps s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// heuristicAction returns a pragmatic action when no LLM is available.
//
// For positions we already hold the threshold is relaxed so any
// meaningfully bearish merged signal ends the position — the cost of
// being wrong on an exit (sitting in cash) is lower than the cost of
// being wrong on a new entry (missed opportunity).
func heuristicAction(m strategy.Merged, held bool) domain.DecisionAction {
	if held {
		if m.DominantSide == domain.SideSell && m.Confidence >= 0.4 && m.Score <= -0.1 {
			return domain.DecisionActionSell
		}
		if m.DominantSide == domain.SideBuy && m.Confidence >= 0.6 && m.Score >= 0.3 {
			return domain.DecisionActionBuy // add to winner
		}
		return domain.DecisionActionHold
	}
	if m.Confidence < 0.5 {
		return domain.DecisionActionHold
	}
	if m.DominantSide == domain.SideSell {
		return domain.DecisionActionSell
	}
	if m.Score > 0.2 {
		return domain.DecisionActionBuy
	}
	return domain.DecisionActionHold
}

func heuristicTarget(m strategy.Merged, maxOrderUSD decimal.Decimal) decimal.Decimal {
	factor := decimal.NewFromFloat(m.Confidence)
	base := maxOrderUSD
	if base.IsZero() {
		base = decimal.NewFromInt(500)
	}
	return base.Mul(factor).Round(2)
}

// buildMomentumUniverse assembles the symbol universe evaluated by the
// momentum strategy each ingest tick. The caller already holds the
// per-tick signal batch so we piggy-back on it rather than re-querying
// the Signals store. Held positions always appear so exit signals fire
// on anything we own, even when the watchlist rotates.
func buildMomentumUniverse(ctx context.Context, e *Engine, tickSignals []domain.Signal) []string {
	set := make(map[string]struct{})
	add := func(sym string) {
		s := strings.ToUpper(strings.TrimSpace(sym))
		if s == "" {
			return
		}
		set[s] = struct{}{}
	}
	if positions, err := e.deps.Store.Positions.List(ctx, e.deps.PortfolioID); err == nil {
		for _, p := range positions {
			if p.Quantity.IsZero() {
				continue
			}
			add(p.Symbol)
		}
	}
	for _, s := range tickSignals {
		add(s.Symbol)
	}
	for _, s := range e.deps.MomentumWatchlist {
		add(s)
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}
