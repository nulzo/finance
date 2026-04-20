package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/engine"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
)

// A risk-engine-rejected trade must leave a structured rejection row
// alongside the existing cooldown and audit entry. This is the
// primary affordance the Wave 3 Rejections page reads from.
func TestEngine_RiskRejection_PersistsRejectionRow(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Risk = risk.NewEngine(risk.Limits{Blacklist: []string{"BLOCKED"}})
	})
	ctx := context.Background()
	te.prices.Set("BLOCKED", decimal.NewFromInt(10))

	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "BLOCKED", Action: domain.DecisionActionBuy,
		Score: 1, Confidence: 1, TargetUSD: decimal.NewFromInt(100),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))
	require.Error(t, te.eng.Execute(ctx, d), "blacklisted symbols must be rejected")

	rows, err := te.store.Rejections.ListSince(ctx, te.p.ID, time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, storage.RejectionSourceRisk, r.Source)
	assert.Equal(t, "BLOCKED", r.Symbol)
	require.NotNil(t, r.DecisionID)
	assert.Equal(t, d.ID, *r.DecisionID)
	assert.Contains(t, r.Reason, "blacklist")
	assert.Equal(t, string(domain.SideBuy), r.Side)
}

// The near-cap short-circuit in evaluate() must record an
// engine-source rejection — this is a "we didn't even try" event
// that would otherwise be invisible on the Rejections page.
func TestEngine_NearCapShortCircuit_PersistsEngineRejection(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Risk = risk.NewEngine(risk.Limits{
			MaxPositionUSD: decimal.NewFromInt(1000),
			MaxOrderUSD:    decimal.NewFromInt(500),
		})
	})
	ctx := context.Background()

	// Seed a position pinned very near the cap: cost basis $999.5
	// leaves $0.50 of headroom, well below the minTradeStep.
	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(99.95))
	require.NoError(t, err)
	te.prices.Set("AAPL", decimal.NewFromInt(100))

	require.NoError(t, te.store.Signals.Insert(ctx, &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.9, Confidence: 0.9, RefID: "near-cap",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	rows, err := te.store.Rejections.ListSince(ctx, te.p.ID, time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "near-cap short-circuit must persist an engine-source rejection")
	found := false
	for _, r := range rows {
		if r.Source == storage.RejectionSourceEngine && r.Symbol == "AAPL" {
			found = true
			assert.Contains(t, r.Reason, "position at max")
			break
		}
	}
	assert.True(t, found)
}

// Strategy gating: an empty `Strategies` slice runs every strategy
// (default); a non-empty slice only runs the listed ones.
func TestEngine_StrategyGating_PoliticianOnly(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.Strategies = []string{"politician"}
	})
	ctx := context.Background()

	// Seed both politician trades and news so both strategies
	// would ordinarily produce signals.
	_, err := te.store.PTrades.Insert(ctx, &domain.PoliticianTrade{
		PoliticianName: "Rep A", Chamber: "house", Symbol: "AAPL",
		Side: domain.SideBuy, AmountMinUSD: 10_000, AmountMaxUSD: 50_000,
		TradedAt: time.Now().UTC(), RawHash: "t1",
	})
	require.NoError(t, err)
	_, err = te.store.News.Insert(ctx, &domain.NewsItem{
		Source: "test", URL: "https://example.com/n1", Title: "AAPL beats earnings",
		Summary: "very positive", Symbols: "AAPL",
		Sentiment: 0.9, Relevance: 0.9, PubAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	require.NoError(t, te.eng.Ingest(ctx))

	sigs, err := te.store.Signals.Active(ctx, "", time.Now().UTC())
	require.NoError(t, err)
	for _, s := range sigs {
		assert.NotEqual(t, domain.SignalKindNews, s.Kind,
			"STRATEGIES=politician must suppress news signals, got %+v", s)
	}
}

// The stale-order sweep cancels any non-terminal order whose
// `submitted_at` is older than `OrderStaleTimeout`. The test seeds
// a pending order with an old submitted_at, runs Reconcile, and
// expects a cancelled order + an engine-source rejection row.
func TestEngine_Reconcile_CancelsStaleOrders(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.OrderStaleTimeout = time.Minute
	})
	ctx := context.Background()

	old := time.Now().UTC().Add(-10 * time.Minute)
	o := &domain.Order{
		PortfolioID: te.p.ID, Symbol: "AAPL", Side: domain.SideBuy,
		Type: domain.OrderTypeMarket, TimeInForce: domain.TIFDay,
		Quantity: decimal.NewFromInt(1), Status: domain.OrderStatusSubmitted,
		BrokerID:    "unknown-id",
		SubmittedAt: &old,
	}
	require.NoError(t, te.store.Orders.Create(ctx, o))

	require.NoError(t, te.eng.Reconcile(ctx))

	reloaded, err := te.store.Orders.Get(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusCancelled, reloaded.Status,
		"stale order must be marked cancelled")

	rejs, err := te.store.Rejections.ListSince(ctx, te.p.ID, time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	found := false
	for _, r := range rejs {
		if r.Symbol == "AAPL" && r.Source == storage.RejectionSourceEngine {
			found = true
			assert.Contains(t, r.Reason, "stale")
		}
	}
	assert.True(t, found, "stale sweep must persist an engine rejection row")

	// A cooldown should also be installed so the decide loop
	// doesn't instantly resubmit.
	cd, err := te.store.Cooldowns.ActiveFor(ctx, te.p.ID, "AAPL", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, cd)
	assert.Contains(t, cd.Reason, "stale")
}

// The daily-loss circuit breaker disables the engine when realized
// P&L since UTC midnight drops below the configured threshold.
func TestEngine_DailyLossBreaker_DisablesEngine(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.AutoDisableDailyLossUSD = decimal.NewFromInt(100)
	})
	ctx := context.Background()

	// Seed a realized event of -$150 since midnight.
	require.NoError(t, te.store.Realized.Insert(ctx, &storage.RealizedEvent{
		PortfolioID: te.p.ID, Symbol: "AAPL",
		Quantity:      decimal.NewFromInt(1),
		RealizedCents: domain.Money(-15000),
		CreatedAt:     time.Now().UTC(),
	}))

	require.True(t, te.eng.Enabled())
	require.NoError(t, te.eng.DecideAndTrade(ctx))
	assert.False(t, te.eng.Enabled(), "engine must auto-disable past the daily-loss breaker")
}

// alwaysFailBroker wraps a MockBroker but always fails on
// SubmitOrder, simulating a broker that's consistently 5xx-ing. Used
// to drive the broker-reject circuit breaker past its threshold.
type alwaysFailBroker struct{ *broker.MockBroker }

func (a *alwaysFailBroker) SubmitOrder(_ context.Context, _ *domain.Order) (*broker.BrokerOrder, error) {
	return nil, fmt.Errorf("%w: simulated broker outage", domain.ErrBrokerRejected)
}

// The broker-reject circuit breaker trips once N+1 broker-source
// rejections are recorded inside the rolling window. The test seeds
// N existing broker rejections, then triggers one fresh Execute that
// fails at the broker, pushing the count over the threshold.
func TestEngine_BrokerRejectBreaker_TripsAfterThreshold(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.AutoDisableBrokerRejects = 2
		d.AutoDisableBrokerWindow = time.Hour
		// Swap in a broker that guarantees SubmitOrder errors.
		d.Broker = &alwaysFailBroker{MockBroker: d.Broker.(*broker.MockBroker)}
	})
	ctx := context.Background()

	// Seed 2 historical broker rejections — at the threshold.
	for i := 0; i < 2; i++ {
		require.NoError(t, te.store.Rejections.Insert(ctx, &storage.Rejection{
			PortfolioID: te.p.ID, Symbol: "OLD",
			Source: storage.RejectionSourceBroker, Reason: "past",
			CreatedAt: time.Now().UTC().Add(-time.Minute),
		}))
	}

	// One fresh Execute that fails at the broker → breaker trips.
	te.prices.Set("AAPL", decimal.NewFromInt(100))
	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "AAPL", Action: domain.DecisionActionBuy,
		Score: 0.9, Confidence: 0.9, TargetUSD: decimal.NewFromInt(100),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))
	require.True(t, te.eng.Enabled())
	require.Error(t, te.eng.Execute(ctx, d), "broker wrapper must reject")
	assert.False(t, te.eng.Enabled(), "broker breaker must auto-disable past the threshold")

	// A subsequent DecideAndTrade must short-circuit.
	require.NoError(t, te.eng.DecideAndTrade(ctx), "disabled engine still returns nil from DecideAndTrade")
}
