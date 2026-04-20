package risk_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/risk"
)

func newEngine(l risk.Limits) *risk.Engine { return risk.NewEngine(l) }

func TestRisk_BlacklistRejects(t *testing.T) {
	e := newEngine(risk.Limits{Blacklist: []string{"GME"}})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "gme", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(50), PortfolioCash: decimal.NewFromInt(10_000),
	})
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "blacklist")
}

func TestRisk_CapsPerOrder(t *testing.T) {
	e := newEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(500)})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(2000),
		Price: decimal.NewFromInt(200), PortfolioCash: decimal.NewFromInt(10_000),
	})
	assert.True(t, r.Approved)
	assert.Equal(t, "500", r.Notional.String())
}

func TestRisk_InsufficientCashDownsizes(t *testing.T) {
	e := newEngine(risk.Limits{})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(1000),
		Price: decimal.NewFromInt(100), PortfolioCash: decimal.NewFromInt(50),
	})
	// Sized to available cash.
	assert.True(t, r.Approved)
	assert.Equal(t, "50", r.Notional.String())
}

func TestRisk_NoCashRejects(t *testing.T) {
	e := newEngine(risk.Limits{})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(1000),
		Price: decimal.NewFromInt(100), PortfolioCash: decimal.NewFromFloat(0.50),
	})
	assert.False(t, r.Approved)
}

func TestRisk_PerSymbolPositionCap(t *testing.T) {
	e := newEngine(risk.Limits{MaxPositionUSD: decimal.NewFromInt(1000)})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(500),
		Price:          decimal.NewFromInt(100),
		PortfolioCash:  decimal.NewFromInt(10_000),
		PositionQty:    decimal.NewFromInt(9),
		PositionAvgCost: decimal.NewFromInt(100),
	})
	assert.True(t, r.Approved)
	assert.Equal(t, "100", r.Notional.String()) // only $100 headroom
}

func TestRisk_PerSymbolPositionFull(t *testing.T) {
	e := newEngine(risk.Limits{MaxPositionUSD: decimal.NewFromInt(1000)})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(500),
		Price:           decimal.NewFromInt(100),
		PortfolioCash:   decimal.NewFromInt(10_000),
		PositionQty:     decimal.NewFromInt(10),
		PositionAvgCost: decimal.NewFromInt(100),
	})
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "position at max")
}

func TestRisk_DailyOrdersCap(t *testing.T) {
	e := newEngine(risk.Limits{MaxDailyOrders: 3})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
		OrdersToday: 3,
	})
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "daily order")
}

func TestRisk_DailyLossLimit(t *testing.T) {
	e := newEngine(risk.Limits{MaxDailyLossUSD: decimal.NewFromInt(200)})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
		RealizedPnLToday: decimal.NewFromInt(-201),
	})
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "loss")
}

func TestRisk_SellWithoutPosition(t *testing.T) {
	e := newEngine(risk.Limits{})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideSell, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
	})
	assert.False(t, r.Approved)
}

func TestRisk_SellDownsizesToPosition(t *testing.T) {
	e := newEngine(risk.Limits{})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideSell, TargetUSD: decimal.NewFromInt(1000),
		Price: decimal.NewFromInt(100), PortfolioCash: decimal.NewFromInt(10_000),
		PositionQty: decimal.NewFromInt(3),
	})
	assert.True(t, r.Approved)
	assert.Equal(t, "300", r.Notional.String())
}

func TestRisk_FractionalExposureCap(t *testing.T) {
	e := newEngine(risk.Limits{MaxSymbolExposure: decimal.NewFromFloat(0.1)})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(5000),
		Price: decimal.NewFromInt(100), PortfolioCash: decimal.NewFromInt(10_000),
		PortfolioEquity: decimal.NewFromInt(10_000),
	})
	assert.True(t, r.Approved)
	// 10% of $10k = $1000
	assert.Equal(t, "1000", r.Notional.String())
}

// Buys that would open a NEW symbol when the portfolio already holds
// MaxConcurrentPositions distinct names must be rejected. Re-buys on
// an existing position are unaffected, and sells are never capped.
func TestRisk_MaxConcurrentPositions_BlocksNewSymbolOnly(t *testing.T) {
	e := newEngine(risk.Limits{MaxConcurrentPositions: 3})
	// New-symbol buy when book is already full → rejected.
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
		OpenPositions: 3,
	})
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "concurrent positions")

	// Re-buy on an EXISTING symbol (non-zero PositionQty) at the cap:
	// still allowed because we're not widening the book.
	r = e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
		PositionQty: decimal.NewFromInt(5), PositionAvgCost: decimal.NewFromInt(10),
		OpenPositions: 3,
	})
	assert.True(t, r.Approved, "re-buy on an existing position must not be blocked by concurrent cap")

	// Sell at the cap: always allowed.
	r = e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideSell, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
		PositionQty:   decimal.NewFromInt(5),
		OpenPositions: 3,
	})
	assert.True(t, r.Approved)

	// New-symbol buy UNDER the cap → allowed.
	r = e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
		OpenPositions: 2,
	})
	assert.True(t, r.Approved)
}

func TestRisk_RequireApproval(t *testing.T) {
	e := newEngine(risk.Limits{RequireApproval: true})
	r := e.Approve(context.Background(), risk.Request{
		Symbol: "AAPL", Side: domain.SideBuy, TargetUSD: decimal.NewFromInt(100),
		Price: decimal.NewFromInt(10), PortfolioCash: decimal.NewFromInt(10_000),
	})
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "approval")
	assert.Equal(t, "10", r.Quantity.String())
}
