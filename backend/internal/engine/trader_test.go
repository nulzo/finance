package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/engine"
	"github.com/nulzo/trader/internal/providers/market"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
)

type testEnv struct {
	eng    *engine.Engine
	store  *storage.Store
	mb     *broker.MockBroker
	prices *broker.StaticPrices
	p      *domain.Portfolio
}

func buildEnv(t *testing.T, opts ...func(*engine.Deps)) *testEnv {
	t.Helper()
	s, err := storage.Open(context.Background(), "file::memory:?_time_format=sqlite&cache=shared&_pragma=foreign_keys(on)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	p := &domain.Portfolio{Name: "main", Mode: "mock", CashCents: 1_000_000}
	require.NoError(t, s.Portfolios.Create(context.Background(), p))

	prices := broker.NewStaticPrices(map[string]decimal.Decimal{"AAPL": decimal.NewFromFloat(100)})
	mb := broker.NewMockBroker(prices, decimal.NewFromInt(10_000), 0)
	mp := market.NewCachedProvider(market.BrokerAdapter{B: mb}, nil, 10*time.Millisecond)

	re := risk.NewEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(500)})
	deps := engine.Deps{
		Store: s, Broker: mb, Market: mp, Risk: re, PortfolioID: p.ID, Log: zerolog.Nop(),
		IngestInterval: time.Minute, DecideInterval: time.Minute,
		// Production-like defaults for the candidate selector so
		// tests see the same code path the running daemon does.
		// Individual tests can override via opts.
		PerKindCap:               5,
		DiscoverySlots:           5,
		CandidateConfidenceFloor: 0.3,
		WatchlistCap:             30,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	eng := engine.New(deps)
	return &testEnv{eng: eng, store: s, mb: mb, prices: prices, p: p}
}

func TestEngine_DecideAndTrade_ProducesOrder(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()

	sig := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.9, Confidence: 0.9, Reason: "test", RefID: "unit-test", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, te.store.Signals.Insert(ctx, sig))
	active, err := te.store.Signals.Active(ctx, "AAPL", time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, active, 1)

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, err := te.store.Orders.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, orders)
	assert.Equal(t, "AAPL", orders[0].Symbol)
	assert.Equal(t, domain.OrderStatusFilled, orders[0].Status)

	pos, err := te.store.Positions.Get(ctx, te.p.ID, "AAPL")
	require.NoError(t, err)
	require.NotNil(t, pos)
	assert.True(t, pos.Quantity.GreaterThan(decimal.Zero))

	decisions, err := te.store.Decisions.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, decisions)
	assert.Equal(t, "heuristic", decisions[0].ModelUsed)
	assert.NotEmpty(t, decisions[0].Reasoning)
}

func TestEngine_ExecuteHonoursRisk(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()

	d := &domain.Decision{
		PortfolioID: te.p.ID, Symbol: "BLOCKED", Action: domain.DecisionActionBuy,
		Score: 1, Confidence: 1, TargetUSD: decimal.NewFromInt(100),
	}
	require.NoError(t, te.store.Decisions.Insert(ctx, d))

	err := te.eng.Execute(ctx, d)
	require.Error(t, err)
}

// ExitPolicy should trigger a sell on a held position once the mark
// price crosses the take-profit threshold, regardless of signal
// opinion. This is the mechanical exit that ensures the system always
// eventually takes profit even in a quiet news cycle.
func TestEngine_ExitPolicy_TakeProfitTriggersSell(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.ExitPolicy = engine.ExitPolicy{TakeProfitPct: 0.20}
	})
	ctx := context.Background()

	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(80))
	require.NoError(t, err)

	te.prices.Set("AAPL", decimal.NewFromFloat(100)) // +25% vs $80 avg

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, err := te.store.Orders.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, orders, "expected an exit sell order")
	assert.Equal(t, domain.SideSell, orders[0].Side)
	assert.Equal(t, "AAPL", orders[0].Symbol)

	decisions, err := te.store.Decisions.List(ctx, te.p.ID, 5)
	require.NoError(t, err)
	require.NotEmpty(t, decisions)
	assert.Equal(t, "exit_policy", decisions[0].ModelUsed)
}

func TestEngine_ExitPolicy_StopLossTriggersSell(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.ExitPolicy = engine.ExitPolicy{StopLossPct: 0.10}
	})
	ctx := context.Background()

	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)
	te.prices.Set("AAPL", decimal.NewFromFloat(89))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, err := te.store.Orders.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, orders)
	assert.Equal(t, domain.SideSell, orders[0].Side)
}

// When the mark is still inside the TP/SL band we must NOT exit.
func TestEngine_ExitPolicy_WithinBandHolds(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.ExitPolicy = engine.ExitPolicy{TakeProfitPct: 0.25, StopLossPct: 0.10}
	})
	ctx := context.Background()

	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)
	te.prices.Set("AAPL", decimal.NewFromFloat(105))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, _ := te.store.Orders.List(ctx, te.p.ID, 10)
	for _, o := range orders {
		assert.NotEqual(t, domain.SideSell, o.Side, "no sell should have fired")
	}
}

func TestExitPolicy_Evaluate(t *testing.T) {
	t.Parallel()
	p := engine.ExitPolicy{TakeProfitPct: 0.25, StopLossPct: 0.10}

	act, ok := p.Evaluate(decimal.NewFromFloat(100), decimal.NewFromFloat(125))
	require.True(t, ok)
	assert.Equal(t, domain.DecisionActionSell, act)

	act, ok = p.Evaluate(decimal.NewFromFloat(100), decimal.NewFromFloat(90))
	require.True(t, ok)
	assert.Equal(t, domain.DecisionActionSell, act)

	_, ok = p.Evaluate(decimal.NewFromFloat(100), decimal.NewFromFloat(124.99))
	assert.False(t, ok)

	_, ok = p.Evaluate(decimal.Zero, decimal.NewFromFloat(100))
	assert.False(t, ok)

	disabled := engine.ExitPolicy{}
	_, ok = disabled.Evaluate(decimal.NewFromFloat(100), decimal.NewFromFloat(1000))
	assert.False(t, ok)
}

// Held positions must be evaluated even when they're not in the
// top-N merged watchlist. Previously the decide loop only considered
// the top watchlist candidates, so positions with no fresh signals
// never got a chance to be sold.
func TestEngine_DecideAndTrade_AlwaysEvaluatesHeldPositions(t *testing.T) {
	te := buildEnv(t, func(d *engine.Deps) {
		d.ExitPolicy = engine.ExitPolicy{TakeProfitPct: 0.10}
		d.WatchlistCap = 1
	})
	ctx := context.Background()

	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(50))
	require.NoError(t, err)
	te.prices.Set("AAPL", decimal.NewFromFloat(60))

	sig := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "NVDA", Side: domain.SideBuy,
		Score: 0.99, Confidence: 0.99, RefID: "unit-test", ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, te.store.Signals.Insert(ctx, sig))

	require.NoError(t, te.eng.DecideAndTrade(ctx))

	orders, err := te.store.Orders.List(ctx, te.p.ID, 10)
	require.NoError(t, err)
	var aaplSell *domain.Order
	for i := range orders {
		if orders[i].Symbol == "AAPL" && orders[i].Side == domain.SideSell {
			aaplSell = &orders[i]
			break
		}
	}
	require.NotNil(t, aaplSell, "held position outside watchlist must still be evaluated")
}
