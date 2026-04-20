package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/storage"
)

// newPortfolio seeds a portfolio row so FK constraints on
// rejections/realized_events don't trip the inserts.
func newPortfolio(t *testing.T, s *storage.Store) *domain.Portfolio {
	t.Helper()
	p := &domain.Portfolio{Name: "w3", Mode: "mock", CashCents: 1_000_000}
	require.NoError(t, s.Portfolios.Create(context.Background(), p))
	return p
}

// Insert + ListSince + CountSince work in combination, including the
// source-filtered count that the circuit breaker uses.
func TestRejections_InsertListCount(t *testing.T) {
	s := openMem(t)
	p := newPortfolio(t, s)
	ctx := context.Background()
	now := time.Now().UTC()

	decID := "dec-1"
	// 3 risk, 2 broker, 1 engine, spread across 2 symbols.
	rows := []storage.Rejection{
		{PortfolioID: p.ID, Symbol: "AAPL", DecisionID: &decID, Side: "buy",
			Source: storage.RejectionSourceRisk, Reason: "below minimum notional",
			TargetUSD: decimal.NewFromInt(2), CreatedAt: now.Add(-30 * time.Minute)},
		{PortfolioID: p.ID, Symbol: "MSFT", Source: storage.RejectionSourceRisk,
			Reason: "daily cap", CreatedAt: now.Add(-20 * time.Minute)},
		{PortfolioID: p.ID, Symbol: "AAPL", Source: storage.RejectionSourceRisk,
			Reason: "max concurrent positions", CreatedAt: now.Add(-5 * time.Minute)},
		{PortfolioID: p.ID, Symbol: "AAPL", Source: storage.RejectionSourceBroker,
			Reason: "insufficient buying power", CreatedAt: now.Add(-10 * time.Minute)},
		{PortfolioID: p.ID, Symbol: "NVDA", Source: storage.RejectionSourceBroker,
			Reason: "asset not tradable", CreatedAt: now.Add(-3 * time.Minute)},
		{PortfolioID: p.ID, Symbol: "NVDA", Source: storage.RejectionSourceEngine,
			Reason: "position at max", CreatedAt: now.Add(-1 * time.Minute)},
	}
	for i := range rows {
		require.NoError(t, s.Rejections.Insert(ctx, &rows[i]))
		assert.NotEmpty(t, rows[i].ID, "Insert should populate an ID")
	}

	// ListSince returns in DESC order.
	all, err := s.Rejections.ListSince(ctx, p.ID, now.Add(-time.Hour), 100)
	require.NoError(t, err)
	require.Len(t, all, 6)
	for i := 1; i < len(all); i++ {
		assert.False(t, all[i].CreatedAt.After(all[i-1].CreatedAt), "must be newest-first")
	}

	// CountSince by source.
	nBroker, err := s.Rejections.CountSince(ctx, p.ID, storage.RejectionSourceBroker, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 2, nBroker)
	nRisk, err := s.Rejections.CountSince(ctx, p.ID, storage.RejectionSourceRisk, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, nRisk)
	nAny, err := s.Rejections.CountSince(ctx, p.ID, "", now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 6, nAny)

	// Windowing: broker count for the last 4 minutes excludes the
	// older "insufficient buying power" row.
	recent, err := s.Rejections.CountSince(ctx, p.ID, storage.RejectionSourceBroker, now.Add(-4*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, recent)
}

// DailySince zero-fills every day in the window even when no events
// were recorded on that day; the number of buckets is inclusive of
// both endpoints (today + N past days).
func TestRealized_DailySinceZeroFills(t *testing.T) {
	s := openMem(t)
	p := newPortfolio(t, s)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(24 * time.Hour)

	// Event 3 days ago: +$12.50. Event today: -$4. No events on the
	// middle two days. Expected series: 4 buckets with values
	// [12.50, 0, 0, -4.00].
	insert := func(at time.Time, cents int64) {
		orderID := "o-" + at.Format("20060102")
		require.NoError(t, s.Realized.Insert(ctx, &storage.RealizedEvent{
			PortfolioID: p.ID, Symbol: "AAPL",
			Quantity:      decimal.NewFromInt(1),
			RealizedCents: domain.Money(cents),
			OrderID:       &orderID,
			CreatedAt:     at,
		}))
	}
	insert(now.Add(-3*24*time.Hour).Add(9*time.Hour), 1250)
	insert(now.Add(2*time.Hour), -400)

	series, err := s.Realized.DailySince(ctx, p.ID, now.Add(-3*24*time.Hour))
	require.NoError(t, err)
	require.Len(t, series, 4, "inclusive of today + 3 past days")
	assert.Equal(t, int64(1250), series[0].RealizedCents.Cents())
	assert.Equal(t, 1, series[0].EventCount)
	assert.Equal(t, int64(0), series[1].RealizedCents.Cents())
	assert.Equal(t, 0, series[1].EventCount)
	assert.Equal(t, int64(0), series[2].RealizedCents.Cents())
	assert.Equal(t, int64(-400), series[3].RealizedCents.Cents())
	assert.Equal(t, 1, series[3].EventCount)

	// Days before the window are not leaked in.
	insert(now.Add(-30*24*time.Hour), 9999)
	series, err = s.Realized.DailySince(ctx, p.ID, now.Add(-3*24*time.Hour))
	require.NoError(t, err)
	var sum int64
	for _, d := range series {
		sum += d.RealizedCents.Cents()
	}
	assert.Equal(t, int64(1250-400), sum, "out-of-window events must be excluded")
}
