package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/engine"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
)

// When the daily order cap has already been reached, DecideAndTrade
// must no-op entirely — no LLM calls, no risk evaluations, and no new
// orders. This is the hot-path short-circuit that quiets the "daily
// order cap reached" spam we saw in production logs.
func TestEngine_DecideAndTrade_ShortCircuitsOnDailyCap(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Risk = risk.NewEngine(risk.Limits{
			MaxOrderUSD:    decimal.NewFromInt(500),
			MaxDailyOrders: 2,
		})
	})
	ctx := context.Background()

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 0; i < 2; i++ {
		o := &domain.Order{
			ID:          uuid.NewString(),
			PortfolioID: te.p.ID,
			Symbol:      "SEED",
			Side:        domain.SideBuy,
			Type:        domain.OrderTypeMarket,
			TimeInForce: domain.TIFDay,
			Quantity:    decimal.NewFromInt(1),
			Status:      domain.OrderStatusPending,
			CreatedAt:   dayStart.Add(time.Duration(i) * time.Minute),
			UpdatedAt:   dayStart.Add(time.Duration(i) * time.Minute),
		}
		require.NoError(t, te.store.Orders.Create(ctx, o))
		o.Status = domain.OrderStatusFilled
		require.NoError(t, te.store.Orders.UpdateStatus(ctx, o))
	}

	sig := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.9, Confidence: 0.9, RefID: "unit-test",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, te.store.Signals.Insert(ctx, sig))

	before, err := te.store.Orders.CountSince(ctx, te.p.ID, dayStart)
	require.NoError(t, err)
	require.NoError(t, te.eng.DecideAndTrade(ctx))
	after, err := te.store.Orders.CountSince(ctx, te.p.ID, dayStart)
	require.NoError(t, err)
	assert.Equal(t, before, after, "no new orders should have been attempted")

	decisions, err := te.store.Decisions.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, decisions, "no decisions should have been recorded on the short-circuit path")
}

// Buy attempts on blacklisted symbols must be risk-rejected, must NOT
// leave a cash reservation behind, and must install a cooldown so the
// next tick skips the symbol.
func TestEngine_Execute_RejectionReleasesReservationAndCoolsDown(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Risk = risk.NewEngine(risk.Limits{
			MaxOrderUSD: decimal.NewFromInt(500),
			Blacklist:   []string{"BADCO"},
		})
	})
	ctx := context.Background()

	pBefore, err := te.store.Portfolios.Get(ctx, te.p.ID)
	require.NoError(t, err)

	// Risk rejection happens after we fetch a quote, so the symbol
	// must be known to the price source even if we never trade it.
	te.prices.Set("BADCO", decimal.NewFromFloat(50))

	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "BADCO", Action: domain.DecisionActionBuy,
		Score: 1, Confidence: 1, TargetUSD: decimal.NewFromInt(100),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))
	err = te.eng.Execute(ctx, d)
	require.Error(t, err)

	pAfter, _ := te.store.Portfolios.Get(ctx, te.p.ID)
	assert.Equal(t, pBefore.ReservedCents, pAfter.ReservedCents,
		"reservation must be released on risk rejection")
	assert.Equal(t, pBefore.CashCents, pAfter.CashCents,
		"cash balance must be untouched by a risk rejection")

	cd, err := te.store.Cooldowns.ActiveFor(ctx, te.p.ID, "BADCO", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, cd, "cooldown should be installed after risk rejection")
	assert.Contains(t, cd.Reason, "blacklist")
}

// A successful buy must:
// 1. net-debit cash by the fill notional
// 2. leave reserved_cents at zero (no leak)
// 3. record no realized event (only sells produce realized rows)
func TestEngine_Execute_BuyClearsReservation(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()

	te.prices.Set("AAPL", decimal.NewFromFloat(100))
	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "AAPL", Action: domain.DecisionActionBuy,
		Score: 1, Confidence: 1, TargetUSD: decimal.NewFromInt(300),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))
	require.NoError(t, te.eng.Execute(ctx, d))

	p, _ := te.store.Portfolios.Get(ctx, te.p.ID)
	assert.Equal(t, int64(0), p.ReservedCents.Cents(), "reservation must be fully released after fill")
	assert.Less(t, p.CashCents.Cents(), int64(1_000_000), "cash should have decreased")

	evs, err := te.store.Realized.ListSince(ctx, te.p.ID, time.Now().UTC().Add(-time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, evs)
}

// A sell fill must record a realized_event whose realized_cents equals
// (fill_price - pre_trade_avg_cost) * qty. This is what feeds the
// daily-loss circuit breaker.
func TestEngine_Execute_SellRecordsRealizedPnL(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		// Default MaxOrderUSD=$500 would cap the sell to ~5 shares out
		// of 10; bump it so the entire position liquidates in one fill
		// and the realized P&L is (100-80)*10 = $200.
		d.Risk = risk.NewEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(5000)})
	})
	ctx := context.Background()

	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(80))
	require.NoError(t, err)
	// The mock broker tracks positions independently of the DB;
	// hydrate it so the sell isn't rejected for "insufficient position".
	te.mb.Hydrate(decimal.NewFromInt(10_000), map[string]broker.BrokerPosition{
		"AAPL": {Symbol: "AAPL", Quantity: decimal.NewFromInt(10), AvgPrice: decimal.NewFromInt(80)},
	})
	te.prices.Set("AAPL", decimal.NewFromFloat(100))

	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "AAPL", Action: domain.DecisionActionSell,
		Score: -1, Confidence: 1, TargetUSD: decimal.NewFromInt(1000),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))
	require.NoError(t, te.eng.Execute(ctx, d))

	evs, err := te.store.Realized.ListSince(ctx, te.p.ID, time.Now().UTC().Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	// (100 - 80) * 10 = 200.
	assert.Equal(t, int64(20_000), evs[0].RealizedCents.Cents())

	// Daily sum mirrors the event value.
	got, err := te.store.Realized.SumSince(ctx, te.p.ID, time.Now().UTC().Truncate(24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(20_000), got.Cents())
}

// evaluate() must skip cooldown'd symbols entirely — no decisions
// inserted, no orders placed. Exit-policy-driven sells are exercised
// elsewhere and explicitly bypass cooldowns.
func TestEngine_Evaluate_SkipsCooldownSymbols(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()

	sig := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.9, Confidence: 0.9, RefID: "unit-test",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, te.store.Signals.Insert(ctx, sig))

	require.NoError(t, te.store.Cooldowns.Upsert(ctx, &storage.Cooldown{
		PortfolioID: te.p.ID,
		Symbol:      "AAPL",
		Until:       time.Now().UTC().Add(time.Hour),
		Reason:      "test",
	}))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, _ := te.store.Orders.List(ctx, te.p.ID, 10)
	assert.Empty(t, orders, "cooldown'd symbol should not produce orders")
	decisions, _ := te.store.Decisions.List(ctx, te.p.ID, 10)
	assert.Empty(t, decisions, "cooldown'd symbol should not produce decisions")
}

// A held position pinned just under MaxPositionUSD must NOT generate a
// new buy decision — the remaining headroom is smaller than any
// tradable order and every decide tick would otherwise emit a buy that
// the risk engine would reject for "below minimum notional". The
// near-cap short-circuit also installs a long cooldown so the next
// tick skips the symbol entirely instead of re-asking the LLM.
func TestEngine_Evaluate_NearCapInstallsCooldown(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Risk = risk.NewEngine(risk.Limits{
			MaxOrderUSD:    decimal.NewFromInt(500),
			MaxPositionUSD: decimal.NewFromInt(2500),
		})
	})
	ctx := context.Background()

	te.prices.Set("AAPL", decimal.NewFromFloat(100))
	// Seed a position at cost basis $2499.50 — inside the 2% (== $10)
	// near-cap envelope, so the engine should treat it as fully
	// allocated and skip the buy entirely.
	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromFloat(24.995), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)

	sig := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.95, Confidence: 0.95, RefID: "near-cap-test",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, te.store.Signals.Insert(ctx, sig))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, err := te.store.Orders.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, orders, "near-cap symbol must not trigger a new buy order")

	decisions, err := te.store.Decisions.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, decisions, "no decision record when position is at or near cap")

	cd, err := te.store.Cooldowns.ActiveFor(ctx, te.p.ID, "AAPL", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, cd, "near-cap short-circuit must install a cooldown")
	// 6h cooldown; be lenient and just assert at least 1h to avoid
	// flakiness at clock boundaries.
	assert.True(t, cd.Until.After(time.Now().UTC().Add(time.Hour)),
		"near-cap cooldown should be long (got %s)", time.Until(cd.Until))
	assert.Contains(t, cd.Reason, "position at max")
}

// "below minimum notional" risk rejections share the same root cause
// as position-at-max (no room left). They must install the same long
// cooldown; otherwise the decide loop re-evaluates the same symbol
// every 30 minutes in perpetuity.
func TestEngine_Execute_MinNotionalRejectionLongCooldown(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		// $2500 MaxPosition + $2499 existing cost basis leaves $1 of
		// headroom; the risk engine sizes the order down to $1, then
		// rejects it with "below minimum notional" because its own
		// floor is strictly less-than $1. Exactly reproduces prod.
		d.Risk = risk.NewEngine(risk.Limits{
			MaxOrderUSD:    decimal.NewFromInt(500),
			MaxPositionUSD: decimal.NewFromInt(2500),
		})
	})
	ctx := context.Background()

	te.prices.Set("AAPL", decimal.NewFromFloat(100))
	// Cost basis $2499.50 → headroom $0.50 → risk sizes order to
	// $0.50 (< $1 minimum) → "below minimum notional". Exactly the
	// loop we saw in prod for NDAQ / BBIO / TSCO.
	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromFloat(24.995), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)
	te.mb.Hydrate(decimal.NewFromInt(10_000), map[string]broker.BrokerPosition{
		"AAPL": {Symbol: "AAPL", Quantity: decimal.NewFromFloat(24.995), AvgPrice: decimal.NewFromInt(100)},
	})

	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "AAPL", Action: domain.DecisionActionBuy,
		Score: 1, Confidence: 1, TargetUSD: decimal.NewFromInt(100),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))
	// Execute should error (risk rejected) and leave cash + reservations intact.
	err = te.eng.Execute(ctx, d)
	require.Error(t, err)

	cd, err := te.store.Cooldowns.ActiveFor(ctx, te.p.ID, "AAPL", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, cd, "min-notional rejection must install a cooldown")
	assert.True(t, cd.Until.After(time.Now().UTC().Add(time.Hour)),
		"min-notional cooldown must be long (got %s)", time.Until(cd.Until))
	assert.Contains(t, cd.Reason, "below minimum notional")
}

// DecideAndTrade must evaluate candidates across multiple signal
// kinds, not just the top-N by raw score. With 6 strong politician
// BUY signals + 1 news BUY + held positions, the decide tick must
// still produce a decision for the news name even when politician
// would monopolise a flat top-N cap. This guards the Wave 2.5
// concentration fix: before the change, `N_NEWS` would never appear
// in the candidate set.
func TestEngine_DecideAndTrade_DiversifiesByKind(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Risk = risk.NewEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(500)})
		d.PerKindCap = 2
		d.DiscoverySlots = 0
		d.WatchlistCap = 10
		d.CandidateConfidenceFloor = 0.3
	})
	ctx := context.Background()

	// Six politician buys, all stronger than the single news buy.
	// A flat top-2 would pick only politician symbols.
	politicians := []string{"P1", "P2", "P3", "P4", "P5", "P6"}
	scores := []float64{0.98, 0.95, 0.90, 0.85, 0.80, 0.75}
	for i, sym := range politicians {
		te.prices.Set(sym, decimal.NewFromInt(50))
		require.NoError(t, te.store.Signals.Insert(ctx, &domain.Signal{
			Kind: domain.SignalKindPolitician, Symbol: sym, Side: domain.SideBuy,
			Score: scores[i], Confidence: 0.95, RefID: "p-" + sym,
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		}))
	}
	te.prices.Set("NEWS1", decimal.NewFromInt(50))
	require.NoError(t, te.store.Signals.Insert(ctx, &domain.Signal{
		Kind: domain.SignalKindNews, Symbol: "NEWS1", Side: domain.SideBuy,
		Score: 0.6, Confidence: 0.9, RefID: "n-NEWS1",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	decs, err := te.store.Decisions.List(ctx, te.p.ID, 100)
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, d := range decs {
		seen[d.Symbol] = true
	}
	// Politician cap=2 → only 2 politician symbols evaluated.
	politicianCount := 0
	for _, p := range politicians {
		if seen[p] {
			politicianCount++
		}
	}
	assert.LessOrEqual(t, politicianCount, 2, "per-kind cap=2 must bound politician evaluations")
	assert.True(t, seen["NEWS1"], "news symbol must earn an evaluation even against stronger politician scores")
}

// When the only merged signals are sells on symbols we don't own,
// DecideAndTrade must not produce any decisions — they are
// guaranteed HOLDs and the LLM call is a waste.
func TestEngine_DecideAndTrade_SkipsSellOnUnowned(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.PerKindCap = 5
		d.DiscoverySlots = 5
		d.WatchlistCap = 10
	})
	ctx := context.Background()

	te.prices.Set("NOPOS", decimal.NewFromInt(50))
	require.NoError(t, te.store.Signals.Insert(ctx, &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "NOPOS", Side: domain.SideSell,
		Score: -0.9, Confidence: 0.95, RefID: "sell-nopos",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	decs, err := te.store.Decisions.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	for _, d := range decs {
		assert.NotEqual(t, "NOPOS", d.Symbol, "sell on unheld must never produce a decision record")
	}
}

// Reconcile flips a stale `submitted` DB order to `filled` when the
// broker reports the fill, applies the fill to the position, and
// records realized P&L on the sell path.
func TestEngine_Reconcile_FillsStaleSubmittedOrders(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()

	// Build a submitted-but-unfilled order in the DB, pointing at a
	// broker ID we'll manufacture in the mock broker below.
	o := &domain.Order{
		ID:          uuid.NewString(),
		PortfolioID: te.p.ID,
		Symbol:      "AAPL",
		Side:        domain.SideBuy,
		Type:        domain.OrderTypeMarket,
		TimeInForce: domain.TIFDay,
		Quantity:    decimal.NewFromInt(5),
		Status:      domain.OrderStatusPending,
	}
	require.NoError(t, te.store.Orders.Create(ctx, o))
	// Upgrade to submitted with a broker ID that the mock broker knows.
	bo, err := te.mb.SubmitOrder(ctx, o)
	require.NoError(t, err)
	o.BrokerID = bo.BrokerID
	// Pretend the order is still submitted (not filled) in the DB so
	// reconcile has work to do.
	o.Status = domain.OrderStatusSubmitted
	o.FilledQty = decimal.Zero
	o.FilledAvgCents = 0
	require.NoError(t, te.store.Orders.UpdateStatus(ctx, o))

	require.NoError(t, te.eng.Reconcile(ctx))

	got, err := te.store.Orders.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusFilled, got.Status)
	assert.True(t, got.FilledQty.GreaterThan(decimal.Zero))

	pos, err := te.store.Positions.Get(ctx, te.p.ID, "AAPL")
	require.NoError(t, err)
	require.NotNil(t, pos)
	assert.True(t, pos.Quantity.GreaterThan(decimal.Zero), "reconcile should have applied the fill")
}
