# Trader — Engineering Roadmap

A living plan for evolving `trader` into a holy-robust, highly-testable, fully-autonomous equity trading platform. We update this file as waves land; completed items get `[x]`, in-progress `[~]`, pending `[ ]`.

> Philosophy: **every change must be idempotent, composable, observable, and independently testable.** Prefer small, narrowly-scoped modules over god-objects. Prefer explicit state in SQLite over implicit state in memory. Prefer graceful degradation over hard failure.

---

## Status at a glance

| Wave | Scope                                    | State      |
| ---- | ---------------------------------------- | ---------- |
| 1    | Correctness, noise reduction, safety    | `[x]` complete |
| 2    | Breadth of signal (momentum, earnings, macro, Form 4, social) | `[~]` in progress |
| 2.5  | Diversification & discovery (force breadth, break concentration loop) | `[~]` in progress |
| 3    | Robustness, ops, circuit breakers, UI for rejections | `[x]` complete |
| 4    | Alternative data (Quiver) + layered LLM grounding | `[x]` complete |
| 5    | Polish, docs, backtesting, dark-mode refresh | `[~]` in progress |

---

## Invariants we must never break

1. **Money math is decimal only.** No floats in accounting paths. `domain.Money` (int64 cents) + `shopspring/decimal` for fractional prices.
2. **Every write is idempotent.** Re-running the same tick produces the same DB state. Use `ON CONFLICT DO UPDATE` or natural-key dedupe (hashes, ref_ids, URLs).
3. **No loop aborts on a single upstream failure.** Providers fail independently; aggregators return partials.
4. **Every external call has a deadline.** `context.WithTimeout` at every I/O boundary, per source.
5. **Rejections are observable.** Every risk/broker reject persists a reason. No content-free `"rejected"` log lines.
6. **Cash accounting can't leak.** Reservations must release on every exit path (defer-release pattern).
7. **Tests at every layer.** Storage repos, risk engine, strategies, and engine orchestration each have dedicated tests. New features ship with coverage.

---

## Wave 1 — Correctness & noise reduction (complete)

Every item in this wave is a bug fix or a safety guarantee. No new data sources, no new strategies. Target: quiet the log spam, eliminate accounting drift, make the platform survive restarts cleanly.

- [x] **W1.1 — Realized P&L feeds the risk engine.**
  New `realized_events` table (`004_wave1_accounting.sql`). `engine.applyFill` inserts one row per sell-side fill with the realized cents delta; `risk.Request.RealizedPnLToday` is populated from `RealizedEventRepo.SumSince(portfolio, midnight UTC)` so `MaxDailyLossUSD` now actually halts trading on a red day.
- [x] **W1.2 — `hydrateMockBroker` uses live quotes.**
  `cmd/trader/main.go` passes the cached `PriceSource` fallback chain to `hydrateMockBroker`; `MarketVal` = `qty * live quote` with a graceful fallback to avg cost when the quote call errors or times out.
- [x] **W1.3 — Cash reservation is defer-released.**
  `engine.Execute` now uses `defer func(){ ReleaseReservation(reservedNotional) }()` with `context.Background()` so a cancelled request context cannot orphan a reservation. New `Portfolios.AddReservation` / `ReleaseReservation` (clamp-at-zero) helpers own the math; `applyFill` only moves cash, never touches reservations.
- [x] **W1.4 — Per-symbol cooldowns.**
  New `cooldowns(portfolio_id, symbol, until_ts, reason)` table + `CooldownRepo`. Risk-reject and broker-reject install a cooldown (daily-cap/loss reasons → next UTC midnight, everything else → `CooldownDuration`, default 30m). `evaluate` skips cooled symbols, **unless** an ExitPolicy triggers a sell (losers can always be cut). `Upsert` keeps the later expiry, so a 30m reject cannot shorten a midnight cap.
- [x] **W1.5 — Short-circuit on daily cap.**
  `DecideAndTrade` reads `Orders.CountSubmittedSince(midnight)` once per tick; if `>= MaxDailyOrders`, the entire decide loop no-ops with a single info log and zero LLM calls.
- [x] **W1.6 — `Orders.CountSubmittedSince` excludes rejected.**
  Added `CountSubmittedSince` (status in `submitted|partially_filled|filled`) alongside the old `CountSince`. The risk cap uses the new one so rejected rows do not double-penalise a noisy tick.
- [x] **W1.7 — Broker reconciliation loop.**
  `engine.Reconcile(ctx)` polls `Broker.GetOrder` for every DB row still in `submitted/partially_filled`, flips the status, applies any fill we missed, and logs position drift between DB and broker. Runs on `ReconcileInterval` (default 60s); safe no-op for mock.
- [x] **W1.8 — PoliticianFollow consumes DB `track_weight`.**
  `Ingest` builds a `name → track_weight` map from `Politicians.List` once per tick and hands it to `strategy.PoliticianFollow.Weight`. Operator-set weights (via API PATCH) now actually influence signal strength.
- [x] **W1.9 — Fix `dctx` shadowing.**
  Inner LLM context renamed to `lctx`. `go vet ./...` is clean.
- [x] **W1.10 — Per-source exponential backoff.**
  New `httpx.SourceBreaker` with unit tests. Wired into `congress.Aggregator` (30s→10m) and `news.Aggregator` (15s→5m). CapitolTrades 503s and other persistently-dead sources now cost one check per back-off window instead of one HTTP call per tick.
- [x] **W1.11 — Frontend: surface rejections + cooldowns.**
  New `GET /v1/portfolios/:id/cooldowns` endpoint. Overview gains a two-column "Recent rejections / Active cooldowns" row; new `/cooldowns` page with searchable table. Sidebar has a new Cooldowns item under Trading.
- [x] **W1.12 — Tests.**
  `internal/httpx/backoff_test.go`, `internal/storage/wave1_test.go`, and `internal/engine/wave1_test.go` cover realized-events sum, cooldown upsert/active/clear, `CountSubmittedSince`, `ListOpen`, reservation helpers, daily-cap short-circuit, reservation release on both rejection and fill, realized-P&L recording on sell, cooldown-bypass for exit policy, and reconcile status transitions.

**Definition of done (met):** `go test ./...` and `go vet ./...` are green; frontend `npm run typecheck` and `npm run build` are green; the reservation/cooldown/daily-loss paths all have direct test coverage.

---

## Wave 2 — Breadth of signal (next)

Each item is a new `strategy.Strategy` and/or `providers.Source` plus the LLM-context surface area to make it useful. Each must ship with:
- provider(s) + fixtures + decode tests,
- DB schema,
- strategy with unit tests,
- engine wiring + metrics,
- a frontend view of the raw data.

- [x] **W2.1 — Technical momentum strategy.**
  New `market.BarProvider` interface + `StooqBars` daily OHLCV implementation with a 15-minute cached fallback chain (`CachedBarProvider`). New `market.TechnicalProvider` computes `{SMA20, SMA50, RSI14 (Wilder), 52w hi/lo, 1d/5d/30d returns}` with its own 5-minute cache. `strategy.Momentum` classifies each symbol as one of `uptrend` (buy) / `oversold` (buy) / `overextended` (sell) / `breakdown` (sell) and emits `SignalKindMomentum` with stable per-day RefIDs. The ingest loop builds a universe from `{held positions} ∪ {symbols surfaced this tick by other strategies} ∪ MOMENTUM_WATCHLIST` so new tickers get discovered and held names always get exit signals. `DecideAndTrade` injects the same Technical snapshot into the LLM prompt (`llm.TechnicalContext`) so the model can weigh price structure next to sentiment. Env: `MOMENTUM_ENABLED`, `MOMENTUM_WATCHLIST`, `MOMENTUM_MIN_CONFIDENCE`. Tests cover SMA/RSI/52wk math, each momentum classification, dedupe, short-history safety, and the cached-bar fallback chain. (Bonus fix shipped with this wave: missing JSON tags on `storage.Cooldown` / `storage.RealizedEvent` that caused `undefined` columns in the frontend.)
- [ ] **W2.2 — Earnings calendar.**
  Finnhub `calendar/earnings` provider → `earnings(symbol, report_date, pre_post, eps_estimate, eps_actual)`. Inject next-earnings into LLM context. Optional auto-flatten N days before print via `AUTO_FLATTEN_PRE_EARNINGS_DAYS`.
- [ ] **W2.3 — Macro regime indicators.**
  Daily FRED/Stooq poll for `^VIX`, `^TNX`, `DXY`, `SPY` → `macro_snapshots`. Compute regime (risk-on/risk-off) and inject into LLM context.
- [ ] **W2.4 — SEC Form 4 (insider trades).**
  EDGAR `browse-edgar` atom feed → `insider_transactions(cik, symbol, insider, role, side, qty, value, filed)`. New `SignalKindInsider`.
- [ ] **W2.5 — Social sentiment.**
  Reddit `new.json` for `r/wallstreetbets`, `r/stocks`, `r/investing`; upvote-weighted ticker counts with velocity. New `SignalKindSocial`. Rate-limited with ETag caching.
- [ ] **W2.6 — Analyst rating changes.**
  Finnhub `stock/recommendation` + `stock/upgrade-downgrade`. Treat as news-style signals with sentiment = `buy > hold > sell`.
- [ ] **W2.7 — Options flow (unusual volume).**
  Best available free tier (Nasdaq `options-chain`, CBOE daily). Aggregate unusual-volume + put/call ratio per symbol. Optional, gated on env.
- [ ] **W2.8 — LLM prompt v2.**
  Decide prompt now includes: portfolio weights, sector concentration, open orders, time-of-day (market session), VIX regime, next-earnings date, current cooldowns. Extensive test fixtures.

---

## Wave 2.5 — Diversification & discovery

**Why this wave exists.** After the first production-ish run (~2 hours) the engine held exactly 6 positions, and those 6 positions were exactly the 6 highest-scoring politician BUY signals (MSFT, NDAQ, SGI, BBIO, TSCO, EME). Meanwhile 337 distinct symbols had active signals — only 6-10 of them were ever reaching the candidate set. The concentration is structural, not a bug:

1. `WATCHLIST_CAP=10` picks the top 10 symbols by merged signal strength. Politician signals dominate the top of that leaderboard.
2. Once a symbol is bought it stays in the candidate set as a **held position** (so we can still exit), AND it often remains in the top-10 by score, so every decide tick re-considers it forever.
3. For held positions pinned just under `MAX_POSITION_USD`, the risk engine sizes the next buy down to a few cents, rejects it for "below minimum notional", and the 30-minute cooldown lets it right back in. The same symbols get 28 duplicate buy decisions over 2 hours.
4. Politician signals don't decay inside their 21-day lookback window, so the top-of-leaderboard doesn't churn on its own.

Every item in this wave tackles one of those feedback loops. The goal is a book that *spreads* across the richest signals we have, not one that pyramids the top-6.

- [x] **W2.5.1 — Near-cap short-circuit + long cooldown on "no room" rejects.**
  The "position at max" short-circuit in `engine.evaluate` now fires when remaining headroom is below `minTradeStep(MaxOrderUSD) = max($10, 2% of MaxOrderUSD)`, not just when `currentCost >= MaxPositionUSD`. That catches the NDAQ/BBIO/TSCO pattern where cost basis is $2499 against a $2500 cap. `installCooldownFor` now buckets all "no room left on this symbol" risk rejections (`below minimum notional`, `no headroom`, `position at max`, `symbol exposure cap`, `quantity rounds to zero`) into the same 6-hour cooldown as the short-circuit, so stuck symbols can't reappear every 30 minutes.
- [x] **W2.5.2 — Diversified candidate selection.**
  New `strategy.SelectCandidates` replaces the flat top-N-by-merged-score picker. Every held symbol is guaranteed inclusion (so exit logic always runs); a per-kind cap (`PER_KIND_CAP=5`) bounds how many symbols each signal kind can contribute so politician no longer monopolises; `DISCOVERY_SLOTS=5` reserves slots for unheld symbols by top score so new names earn evaluation. There is intentionally no top-overall catch-all — that would let the dominant kind reclaim the slots the per-kind cap just blocked. `WATCHLIST_CAP` default bumped from 10 → 30 to comfortably accommodate the new passes. Configurable via `PER_KIND_CAP`, `DISCOVERY_SLOTS`, `CANDIDATE_CONFIDENCE_FLOOR`.
- [x] **W2.5.3 — Concentration penalty in `strategy.Merge`.**
  New `strategy.ApplyConcentrationPenalty` scales BUY-side merged entries by `max(floor, 1 - fill*1.1)` where `fill = cost_basis / MaxPositionUSD`. A 95%-full MSFT gets score/confidence multiplied by ~0 and falls off the per-kind leaderboard; a 0%-full NVDA at the same raw score rises past it. SELL-side entries are never penalised — a near-capped position is exactly where we want the fastest exit-signal evaluation. Applied automatically in `DecideAndTrade` before `SelectCandidates` sees the merged list.
- [x] **W2.5.4 — `MAX_CONCURRENT_POSITIONS` hard cap.**
  New `risk.Limits.MaxConcurrentPositions` + `OpenPositions` request field. Buys that would open a NEW symbol are rejected once the book is at the cap; re-buys on existing positions and all sells are unaffected. Default 15 via `MAX_CONCURRENT_POSITIONS`. The engine counts distinct non-zero positions at execute time and populates the risk request; the risk engine enforces it before any downsize logic so a fresh symbol can't sneak in under a cash / per-order cap.
- [x] **W2.5.5 — Politician signal age-decay.**
  `strategy.PoliticianFollow.HalfLife` adds a `2^(-age/HalfLife)` multiplier to each trade's weight before bucket aggregation, clamped at ≥ 0.01. A 7-day half-life (default via `POLITICIAN_HALFLIFE=168h`) means a week-old trade counts for half; two weeks old is a quarter. The top-of-leaderboard now churns naturally as trades age out instead of pinning the same 6 names for the full 21-day lookback. Back-compat preserved: `HalfLife=0` disables decay, keeping the Wave 1 behaviour for tests that pre-date this knob.
- [x] **W2.5.6 — Sell-on-unowned filter.**
  `SelectCandidates` drops `DominantSide == sell` entries on symbols we don't hold at the very top of selection — they're guaranteed HOLDs and the LLM call to produce the HOLD is a pure token burn. Saved by explicit test (`TestEngine_DecideAndTrade_SkipsSellOnUnowned`).
- [ ] **W2.5.7 — Sector / industry cap.**
  New risk limit `MAX_SECTOR_EXPOSURE_PCT`. Symbol metadata table (`symbols(symbol, sector, industry, market_cap_tier)`) hydrated from Finnhub or a bundled seed. Prevents the portfolio from going 80% tech by accident.

---

## Wave 3 — Robustness & ops  `[x] complete`

- [x] **W3.1 — Structured risk-rejection persistence.**
  New `rejections(id, portfolio_id, symbol, decision_id, side, source, reason, target_usd, created_at)` table with indexes on `(portfolio_id, created_at)`, `(symbol, created_at)`, and `(source)`. Populated on every risk-engine rejection, every broker submit failure, every near-cap short-circuit in `evaluate`, and every stale-order cancellation in `Reconcile`. Exposed via `GET /v1/portfolios/:id/rejections?since=&source=&limit=`. Frontend ships a dedicated Rejections page with stat cards (per-source counts), source/window filters, and a top-symbols cheat sheet — "why we didn't trade X" is now one click from the sidebar.
- [x] **W3.2 — Engine circuit breakers.**
  Two independent breakers, both triggered lazily from `recordRejection`/`DecideAndTrade` so they cost nothing when quiet: `AUTO_DISABLE_BROKER_REJECTS` trips when broker-source rejections exceed the threshold within `AUTO_DISABLE_BROKER_WINDOW`; `AUTO_DISABLE_DAILY_LOSS_USD` trips when `Realized.SumSince(UTC midnight)` ≤ `−N`. Both call `Engine.Disable()`, emit a structured audit event, and the next `DecideAndTrade` no-ops. Tests: `TestEngine_DailyLossBreaker_DisablesEngine`, `TestEngine_BrokerRejectBreaker_TripsAfterThreshold`.
- [x] **W3.3 — Partial-fill timeout policy.**
  `Reconcile` now ends every loop with `sweepStaleOrders`: any non-terminal order whose `submitted_at` is older than `ORDER_STALE_TIMEOUT` (default 15m) is cancelled at the broker, marked `cancelled` locally, has its cash reservation released, receives a long cooldown (`stale_order`), and persists an engine-source rejection row. Added a `CancelOrder` method to the `Broker` interface (implemented for both `MockBroker` and `AlpacaBroker`) and a `idx_orders_status_submitted_asc` index for cheap sweeps. Test: `TestEngine_Reconcile_CancelsStaleOrders`.
- [x] **W3.4 — Configurable strategy set.**
  `STRATEGIES=politician,news,momentum` env gates which strategies `regenerateSignals` actually runs. Empty (default) = all strategies active. Enables A/B and "turn off the broken news feed without a rebuild" workflows. Test: `TestEngine_StrategyGating_PoliticianOnly`.
- [x] **W3.5 — Risk/engine control APIs + UI.**
  New `GET /v1/risk/limits` and `PATCH /v1/risk/limits` with a mutex-safe `risk.Engine` (added `sync.RWMutex`, `GetLimits`, `UpdateLimits`, `AddBlacklist`, `RemoveBlacklist` — concurrent tests in `internal/risk/wave3_test.go`). PATCH accepts pointer-valued JSON fields so callers can update just a subset without accidentally zeroing others. `DELETE /v1/portfolios/:id/cooldowns/:symbol` releases a single cooldown on demand. Frontend ships a full Risk Limits editor page (diff-based PATCH, validation, add/remove blacklist chips, require-approval toggle) and a trash-can button on the Cooldowns row. All mutations write an audit row.
- [x] **W3.6 — P&L time-series.**
  `RealizedEventRepo.DailySince` buckets events in Go (portable across SQLite builds) and returns a zero-filled `[]DailyPnL`. Exposed via `GET /v1/portfolios/:id/pnl?since=` (default 30d). Overview page now renders a combo chart: green/red daily bars for the day's realised P&L + an indigo area line for cumulative, with a 30-day cumulative total callout at the top-right of the card. Test: `TestRealized_DailySinceZeroFills`.

---

## Wave 4 — Alternative Data & Layered Grounding  `[x] complete`

- [x] **W4.1 — LLM Web Search Extensions.**
  Wired `prism:web_search` and `prism:datetime` extensions into the LLM client. `AnalyseNews` and `Decide` now instruct the model to actively search the web for deeper context, recent news, and market sentiment, providing "layered grounding" rather than just reacting to headlines in a vacuum.
- [x] **W4.2 — Quiver Insider Trading.**
  New `providers/quiver` client (token + CSRF, retry/backoff, graceful subscription-gate handling) fetches SEC Form 4 filings via `/beta/bulk/insiders`. Data lands in `insider_trades` (migration 006) with `raw_hash` dedupe and `idx_insider_trades_symbol_ts`. `strategy.InsiderFollow` emits `SignalKindInsider` with age-decay, a `MIN_VALUE_USD` floor, +50% weighting for C-suite officers, and suppression of single-insider sells. Frontend: new `/insiders` page with stat cards (buy $ vs sell $ net), symbol/side/window filters, and a top-buy-tickers cheat sheet. API: `GET /v1/insiders?symbol=&side=&since=&limit=`. Tests: `TestFetchInsiders_DecodesQuiverShape`, `TestGet_SubscriptionGateIsSoftFailure`, `TestInsiders_InsertDedupAndQuery`, `TestInsiderFollow_*` (CEO cluster high-confidence buy, single-seller suppression, tiny-buy filter, age decay).
- [x] **W4.3 — Quiver WallStreetBets & Social Sentiment.**
  Quiver `/beta/bulk/wallstreetbets` and `/beta/bulk/twitter` hourly/daily rollups → `social_posts` (migration 006). `strategy.SocialBuzz` emits `SignalKindSocial` combining mention volume (gated by `MIN_MENTIONS`) with sentiment sign; Twitter follower-count rows are skipped (they are reach, not buzz). Frontend: new `/social` page with platform filter, volume-weighted sentiment stat, colour-coded WSB vs Twitter badges, and a top-mentions cheat sheet. API: `GET /v1/social?symbol=&platform=&since=&limit=`. Tests: `TestFetchWSB_SkipsZeroMentionRows`, `TestSocial_InsertAndBySymbol`, `TestSocialBuzz_*` (bullish chorus buy, neutral skip, below-floor skip, Twitter-only rows don't count).
- [x] **W4.4 — Quiver Corporate Lobbying & Contracts.**
  Quiver `/beta/bulk/lobbying` + `/beta/bulk/govcontractsall` → `lobbying_events` and `gov_contracts` tables (migration 006). Engine `ingestQuiver` persists both, and `evaluate` hydrates `RecentLobbying` / `RecentContracts` into `llm.DecideRequest` so the LLM sees regulatory-tailwind and contract-award context alongside price action. Frontend: `/lobbying` and `/contracts` pages, each with window + symbol filters, top-spend / top-award cheat sheets, and full export. API: `GET /v1/lobbying`, `GET /v1/contracts`. Tests: `TestFetchLobbying_SinceFilter`, `TestFetchGovContracts_ParsesDollarAmount`, `TestLobbyingAndContracts_BasicRoundTrip`.
- [x] **W4.5 — Quiver Off-Exchange Short Volume.**
  Quiver `/beta/bulk/offexchange` → `short_volume(symbol, day, short_volume, total_volume, short_exempt_volume, short_ratio)` upsert with defensive `short_ratio` clamping to [0,1]. `evaluate` pulls the latest snapshot into the LLM prompt as `ShortInterest` context so the model can flag squeeze-setup and heavy-distribution regimes. API: `GET /v1/short-volume/:symbol` returns the daily time-series for chart drill-downs. Tests: `TestFetchOffExchange_ComputesShortRatio`, `TestShortVolume_UpsertClampsRatio`.

---

## Wave 5 — Polish

- [x] **W5.0 — Portfolio analytics: unrealised P&L + equity curve.**
  New `equity_snapshots` table (migration 007) captures a full valuation every `EQUITY_SNAPSHOT_INTERVAL` (default 5m) — cash, cost basis, mark-to-market, realised, unrealised, equity, plus open/priced counts. Pure `internal/equity.Compute` is shared by the snapshot loop and the HTTP handlers so they can never drift; unquoted positions mark at cost with `priced=false` so the UI can flag stale rows instead of silently pretending "no move". Engine grew two new loops (`equity-snapshot`, `equity-retention`) driven off `EquitySnapshotInterval` and `EquitySnapshotRetention` (default 90d). API: `GET /v1/portfolios/:id/{equity,equity/history,positions/pnl,analytics/summary}`. Frontend: new `/analytics` page with an equity-curve chart, realised-vs-unrealised stacked area, positions leaderboard, and a full open-positions P&L table; Overview header swapped to Total Equity / Unrealised P/L / Day Change stat cards and gained its own equity-curve panel. Tests: `internal/equity/equity_test.go` (mark-to-market math, unquoted fallback, quote-error resilience, pct rounding), `internal/engine/equity_test.go` (persists snapshot, LiveEquity matches snapshot, accumulates history), `internal/api/equity_test.go` (live endpoint, history ordering, empty-array contract, summary).
- [ ] **W5.1 — "Why" drawer on every decision/order row.**
- [ ] **W5.2 — Backtest page** seeded from persisted historical signals.
- [ ] **W5.3 — Dark-mode refresh + skeleton-loading polish pass.**
- [ ] **W5.4 — Live-trading runbook + env reference + populated `.env.example`.**

---

## Change log

- _2026-04-20_: Initial roadmap drafted. Wave 1 kicked off.
- _2026-04-20_: Wave 1 landed (all 12 items). Wave 2.1 (Momentum) landed with bar provider, indicator math, classifier, LLM prompt enrichment, and full unit coverage. Also fixed missing JSON tags on Cooldown/RealizedEvent that caused `undefined` in the frontend.
- _2026-04-20_: Post-run portfolio audit revealed the concentration loop (6 positions = top 6 politician BUY signals; 337 other symbols ignored). Opened Wave 2.5 to track diversification work. Landed W2.5.1: near-cap short-circuit (`minTradeStep` = max($10, 2% of MaxOrderUSD)) + long cooldown for all "no room left on this symbol" risk rejections. Tests: `TestEngine_Evaluate_NearCapInstallsCooldown`, `TestEngine_Execute_MinNotionalRejectionLongCooldown`.
- _2026-04-20_: Landed **Wave 3 — Robustness & ops** end to end. Backend: migration 005 adds a `rejections` table + `idx_orders_status_submitted_asc`; `storage.RejectionRepo` with `Insert/ListSince/CountSince`; `RealizedEventRepo.DailySince` zero-fills daily buckets in Go; `risk.Engine` made mutex-safe with public `GetLimits/UpdateLimits/Add|RemoveBlacklist`; `broker.Broker` grew `CancelOrder` (mock + Alpaca). Engine grew `recordRejection`, `sweepStaleOrders`, `maybeTripBrokerBreaker`, `maybeTripDailyLossBreaker`, `strategyEnabled`, all wired into `Execute`, `evaluate`, `Reconcile`, `regenerateSignals`, and `DecideAndTrade`. API: `GET /v1/portfolios/:id/rejections`, `DELETE /v1/portfolios/:id/cooldowns/:symbol`, `GET /v1/portfolios/:id/pnl`, `GET/PATCH /v1/risk/limits`. Frontend: new Rejections and Risk Limits pages, Clear buttons on Cooldowns, P&L combo chart on Overview; sidebar + router + types updated. New env knobs: `STRATEGIES`, `ORDER_STALE_TIMEOUT`, `AUTO_DISABLE_BROKER_REJECTS`, `AUTO_DISABLE_BROKER_WINDOW`, `AUTO_DISABLE_DAILY_LOSS_USD`. Tests added: `internal/risk/wave3_test.go` (limits snapshotting, blacklist helpers, concurrent access), `internal/storage/wave3_test.go` (rejections repo + `DailySince` zero-fill), `internal/engine/wave3_test.go` (rejection persistence, near-cap engine source, strategy gating, stale-order cancel, both breakers). Full `go test ./...` + `go vet ./...` + frontend `tsc -b` and `vite build` green.
- _2026-04-20_: Landed W2.5.2–W2.5.6 together. New `strategy.SelectCandidates` (per-kind quotas + reserved discovery slots + held-guarantee + sell-on-unowned filter), `strategy.ApplyConcentrationPenalty` (BUY-side demotion proportional to fill against `MaxPositionUSD`), `risk.Limits.MaxConcurrentPositions` (hard cap on new-symbol buys, re-buys & sells unaffected), and `PoliticianFollow.HalfLife` (2^(-age/HalfLife) decay per trade). `WatchlistCap` default 10 → 30. New env knobs: `PER_KIND_CAP`, `DISCOVERY_SLOTS`, `CANDIDATE_CONFIDENCE_FLOOR`, `MAX_CONCURRENT_POSITIONS`, `POLITICIAN_HALFLIFE`. Tests added: `TestApplyConcentrationPenalty_{BuysDemotedSellsUntouched,RespectsFloor}`, `TestSelectCandidates_{PerKindQuotasPreventMonopoly,DiscoverySlotsReserveUnheld,DropsSellOnUnowned,IncludesHeldWithoutSignals,ConfidenceFloorExcludesWeakNonHeld,MaxCandidatesIsHardCap,Deterministic}`, `TestPoliticianFollow_AgeDecay`, `TestRisk_MaxConcurrentPositions_BlocksNewSymbolOnly`, `TestEngine_DecideAndTrade_{DiversifiesByKind,SkipsSellOnUnowned}`. Only W2.5.7 (sector cap) still open — deferred behind symbol-metadata backfill.
- _2026-04-20_: Landed **W5.0 — portfolio analytics**. New `equity_snapshots` table (migration 007) + `storage.EquitySnapshotRepo` (Insert/Latest/ListSince/PurgeOlderThan); pure `internal/equity.Compute` shared by engine snapshot loop and HTTP; engine grew `SnapshotEquity`, `LiveEquity`, `purgeOldSnapshots`, two new loops (`equity-snapshot`, `equity-retention`), and config knobs `EQUITY_SNAPSHOT_INTERVAL`/`EQUITY_SNAPSHOT_RETENTION`. API: `GET /v1/portfolios/:id/{equity,equity/history,positions/pnl,analytics/summary}`. Frontend: new `/analytics` page (equity curve, realised-vs-unrealised area, positions leaderboard, open-positions P&L table), Overview header upgraded to Total Equity / Unrealised / Day-change cards with its own equity-curve panel; new `signedCents`/`signedPercent` formatters. Full Go test suite, `go vet`, and `tsc --noEmit` + `eslint` all green.
- _2026-04-20_: Landed **Wave 4 — Alternative Data & Layered Grounding** end to end. W4.1 (LLM web-search + datetime extensions) wired into `AnalyseNews` and `Decide`. W4.2–W4.5 shipped together: new `providers/quiver` client with shared token+CSRF auth, retry/backoff, and a soft-fail path for Quiver's subscription gate; migration 006 adds five tables (`insider_trades`, `social_posts`, `lobbying_events`, `gov_contracts`, `short_volume`) each with `raw_hash` dedupe and symbol/time indexes; `storage.{InsiderRepo,SocialRepo,LobbyingRepo,GovContractRepo,ShortVolumeRepo}` provide `Since`/`BySymbol`/`ListRecent`/`LatestBySymbol`/upsert. Two new strategies — `InsiderFollow` (age decay, executive weighting, min-value floor, single-seller suppression) emitting `SignalKindInsider`, and `SocialBuzz` (mention + sentiment combo, Twitter-follower rows rejected) emitting `SignalKindSocial` — plug into `regenerateSignals` via the existing `STRATEGIES` gate. Engine: new `ingestQuiver` loop persists all five feeds per tick, graceful on `ErrSubscriptionRequired`; `evaluate` injects `RecentInsiders`, `RecentSocial`, `RecentLobbying`, `RecentContracts`, and `ShortInterest` into `llm.DecideRequest`, and `llm.Decide` formats them into the layered-grounding prompt alongside the web-search extensions. API: `GET /v1/{insiders,social,lobbying,contracts,short-volume/:symbol}` with window/limit/symbol/side/platform filters. Frontend: four new Intelligence pages (`/insiders`, `/social`, `/lobbying`, `/contracts`) each with stat cards, filter bar, click-to-filter cheat sheet, and CSV export; sidebar + router + `types/api.ts` + `config/paths.ts` all updated; `SignalKind` union now includes `"insider"` and `"social"`. Tests added across four packages: `internal/providers/quiver/quiver_test.go` (mock HTTP decode tests for every endpoint + subscription-gate soft-fail), `internal/storage/wave4_test.go` (insert/dedupe/query round-trips, short-ratio clamping), `internal/strategy/wave4_test.go` (per-strategy behaviour + age decay). Full `go test ./... -count=1`, `go vet ./...`, `tsc -b`, `vite build`, and `eslint` all green.
