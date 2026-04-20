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

func openMem(t *testing.T) *storage.Store {
	t.Helper()
	// Unique in-memory db per test (shared cache for connection reuse under sqlx).
	url := "file::memory:?_time_format=sqlite&cache=shared&_pragma=foreign_keys(on)"
	s, err := storage.Open(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_PortfolioLifecycle(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := &domain.Portfolio{Name: "main", Mode: "mock", CashCents: 1_000_000}
	require.NoError(t, s.Portfolios.Create(ctx, p))
	got, err := s.Portfolios.Get(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "main", got.Name)
	assert.Equal(t, int64(1_000_000), got.CashCents.Cents())

	require.NoError(t, s.Portfolios.UpdateCash(ctx, p.ID, domain.Money(500), domain.Money(200)))
	got, _ = s.Portfolios.Get(ctx, p.ID)
	assert.Equal(t, int64(1_000_500), got.CashCents.Cents())
	assert.Equal(t, int64(200), got.ReservedCents.Cents())
	assert.Equal(t, int64(1_000_300), int64(got.AvailableCents()))
}

func TestStore_PositionsApplyBuyAndSell(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := &domain.Portfolio{Name: "main", Mode: "mock", CashCents: 10_000_00}
	require.NoError(t, s.Portfolios.Create(ctx, p))

	// Buy 10 @ $100
	pos, err := s.Positions.Apply(ctx, p.ID, "AAPL", domain.SideBuy, decimal.NewFromInt(10), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)
	assert.Equal(t, "10", pos.Quantity.String())
	assert.Equal(t, int64(10000), pos.AvgCostCents.Cents())

	// Buy 10 @ $120, avg should be 110
	pos, err = s.Positions.Apply(ctx, p.ID, "AAPL", domain.SideBuy, decimal.NewFromInt(10), domain.NewMoneyFromFloat(120))
	require.NoError(t, err)
	assert.Equal(t, "20", pos.Quantity.String())
	assert.Equal(t, int64(11000), pos.AvgCostCents.Cents())

	// Sell 5 @ $130; realised pnl = (130-110) * 5 = 100
	pos, err = s.Positions.Apply(ctx, p.ID, "AAPL", domain.SideSell, decimal.NewFromInt(5), domain.NewMoneyFromFloat(130))
	require.NoError(t, err)
	assert.Equal(t, "15", pos.Quantity.String())
	assert.Equal(t, int64(10000), pos.RealizedCents.Cents())
}

func TestStore_PTradeDeduplicates(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	trade := &domain.PoliticianTrade{
		PoliticianName: "Nancy Pelosi", Chamber: "house", Symbol: "NVDA", Side: domain.SideBuy,
		AmountMinUSD: 1_000_000, AmountMaxUSD: 5_000_000,
		TradedAt: time.Now(), DisclosedAt: time.Now(), Source: "test", RawHash: "abcd",
	}
	inserted, err := s.PTrades.Insert(ctx, trade)
	require.NoError(t, err)
	assert.True(t, inserted)
	trade.ID = "" // new id but same raw_hash
	inserted, err = s.PTrades.Insert(ctx, trade)
	require.NoError(t, err)
	assert.False(t, inserted)
}

// Politician trades are ingested without a resolved politician FK, so the
// politician_id column is NULL. Reading them back must not explode.
func TestStore_PTrade_NullPoliticianIDScansCleanly(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	trade := &domain.PoliticianTrade{
		PoliticianName: "Jane Doe", Chamber: "senate", Symbol: "AAPL", Side: domain.SideBuy,
		AmountMinUSD: 15_001, AmountMaxUSD: 50_000,
		TradedAt: time.Now(), DisclosedAt: time.Now(), Source: "test", RawHash: "null-fk-1",
		// PoliticianID intentionally left nil.
	}
	_, err := s.PTrades.Insert(ctx, trade)
	require.NoError(t, err)

	got, err := s.PTrades.ListRecent(ctx, 10)
	require.NoError(t, err, "scanning trades with NULL politician_id must not fail")
	require.Len(t, got, 1)
	assert.Nil(t, got[0].PoliticianID, "NULL column should scan as nil *string")
	assert.Equal(t, "AAPL", got[0].Symbol)

	// Also exercise the BySymbol and Since query paths.
	bySym, err := s.PTrades.BySymbol(ctx, "AAPL", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, bySym, 1)

	since, err := s.PTrades.Since(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, since, 1)
}

// When a politician row has been resolved upstream, the FK must round-trip.
func TestStore_PTrade_NonNullPoliticianIDRoundTrips(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := &domain.Politician{Name: "Foo Bar", Chamber: "house", TrackWeight: 1.0}
	require.NoError(t, s.Politicians.Upsert(ctx, p))

	trade := &domain.PoliticianTrade{
		PoliticianID:   &p.ID,
		PoliticianName: p.Name, Chamber: "house", Symbol: "MSFT", Side: domain.SideSell,
		AmountMinUSD: 1_001, AmountMaxUSD: 15_000,
		TradedAt: time.Now(), DisclosedAt: time.Now(), Source: "test", RawHash: "fk-present",
	}
	_, err := s.PTrades.Insert(ctx, trade)
	require.NoError(t, err)

	got, err := s.PTrades.ListRecent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].PoliticianID)
	assert.Equal(t, p.ID, *got[0].PoliticianID)
}

func TestStore_SignalActiveFilter(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	sig := &domain.Signal{Kind: domain.SignalKindNews, Symbol: "AAPL", Side: domain.SideBuy, Score: 0.5, Confidence: 0.7, RefID: "news:AAPL:buy:a", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, s.Signals.Insert(ctx, sig))
	expired := &domain.Signal{Kind: domain.SignalKindNews, Symbol: "AAPL", Side: domain.SideBuy, Score: 0.5, Confidence: 0.7, RefID: "news:AAPL:buy:b", ExpiresAt: time.Now().Add(-time.Hour)}
	require.NoError(t, s.Signals.Insert(ctx, expired))
	active, err := s.Signals.Active(ctx, "AAPL", time.Now())
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, sig.ID, active[0].ID)
}

// Upsert must refresh an existing row in place when the composite key
// (kind, symbol, side, ref_id) matches. This is the core dedup
// behaviour that keeps the signals table from growing unbounded every
// ingest tick.
func TestStore_SignalUpsertDedupesByKey(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	sig := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.5, Confidence: 0.7, RefID: "politician:AAPL:buy:20260420",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, s.Signals.Upsert(ctx, sig))

	// Second upsert with the same key but a different score + a fresh
	// UUID must NOT create a new row — it must overwrite the existing
	// one (keyed by the unique index on kind/symbol/side/ref_id).
	sig2 := &domain.Signal{
		Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
		Score: 0.8, Confidence: 0.9, RefID: "politician:AAPL:buy:20260420",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	require.NoError(t, s.Signals.Upsert(ctx, sig2))

	rows, err := s.Signals.Active(ctx, "AAPL", time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 1, "upsert must collapse duplicates by composite key")
	assert.InDelta(t, 0.8, rows[0].Score, 1e-9)
	assert.InDelta(t, 0.9, rows[0].Confidence, 1e-9)
}

// Different RefIDs on the same (kind, symbol, side) must coexist —
// that's how we keep multiple days' aggregations visible.
func TestStore_SignalUpsertDistinctRefIDsCoexist(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	for _, day := range []string{"20260420", "20260421"} {
		sig := &domain.Signal{
			Kind: domain.SignalKindPolitician, Symbol: "AAPL", Side: domain.SideBuy,
			Score: 0.5, Confidence: 0.7,
			RefID: "politician:AAPL:buy:" + day, ExpiresAt: time.Now().Add(time.Hour),
		}
		require.NoError(t, s.Signals.Upsert(ctx, sig))
	}
	rows, err := s.Signals.Active(ctx, "AAPL", time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

// PurgeExpired sweeps older-than-cutoff rows — the ingest loop relies
// on this to keep the table bounded.
func TestStore_SignalPurgeExpired(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	now := time.Now()
	for i, dur := range []time.Duration{-2 * time.Hour, -time.Hour, time.Hour, 2 * time.Hour} {
		sig := &domain.Signal{
			Kind: domain.SignalKindNews, Symbol: "AAPL", Side: domain.SideBuy,
			Score: 0.1, Confidence: 0.1,
			RefID:     "bucket-" + string(rune('a'+i)),
			ExpiresAt: now.Add(dur),
		}
		require.NoError(t, s.Signals.Upsert(ctx, sig))
	}
	n, err := s.Signals.PurgeExpired(ctx, now)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "exactly the 2 already-expired rows should be removed")

	active, err := s.Signals.Active(ctx, "AAPL", now)
	require.NoError(t, err)
	assert.Len(t, active, 2)
}

func TestStore_DecisionPersists(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := &domain.Portfolio{Name: "main", Mode: "mock", CashCents: 100}
	require.NoError(t, s.Portfolios.Create(ctx, p))
	d := &domain.Decision{
		PortfolioID: p.ID, Symbol: "AAPL", Action: domain.DecisionActionBuy,
		Score: 0.5, Confidence: 0.8, TargetUSD: decimal.NewFromInt(100), Reasoning: "because",
	}
	require.NoError(t, s.Decisions.Insert(ctx, d))
	list, err := s.Decisions.List(ctx, p.ID, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "AAPL", list[0].Symbol)
}

func TestStore_OrdersCountSince(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := &domain.Portfolio{Name: "main", Mode: "mock", CashCents: 100}
	require.NoError(t, s.Portfolios.Create(ctx, p))
	for i := 0; i < 3; i++ {
		o := &domain.Order{PortfolioID: p.ID, Symbol: "AAPL", Side: domain.SideBuy, Type: domain.OrderTypeMarket, TimeInForce: domain.TIFDay, Quantity: decimal.NewFromInt(1)}
		require.NoError(t, s.Orders.Create(ctx, o))
	}
	n, err := s.Orders.CountSince(ctx, p.ID, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}
