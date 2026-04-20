package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
)

// TestEngine_SnapshotEquity_PersistsMarkToMarket asserts that a
// SnapshotEquity call produces a row with the expected cash, cost,
// mark-to-market, and unrealised values for a seeded portfolio.
func TestEngine_SnapshotEquity_PersistsMarkToMarket(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()

	// Seed a 10-share AAPL position at $100 avg, then move the
	// market to $110 so unrealised = +$100.
	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(10), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)
	te.prices.Set("AAPL", decimal.NewFromFloat(110))

	require.NoError(t, te.eng.SnapshotEquity(ctx))

	latest, err := te.store.Equity.Latest(ctx, te.p.ID)
	require.NoError(t, err)
	require.NotNil(t, latest, "snapshot should exist")

	assert.EqualValues(t, 1_000_000, int64(latest.CashCents), "cash preserved from seed")
	assert.EqualValues(t, 100_000, int64(latest.PositionsCost), "cost = 10 * $100 = $1000")
	assert.EqualValues(t, 110_000, int64(latest.PositionsMTM), "mtm = 10 * $110 = $1100")
	assert.EqualValues(t, 10_000, int64(latest.UnrealizedCents), "+$100 unrealised")
	// Equity = cash + mtm = $10,000 + $1,100 = $11,100
	assert.EqualValues(t, 1_000_000+110_000, int64(latest.EquityCents))
	assert.Equal(t, 1, latest.OpenPositions)
	assert.Equal(t, 1, latest.PricedPositions)
}

// TestEngine_SnapshotEquity_MultipleSnapshotsBuildHistory checks that
// successive calls accumulate rows — the charting contract depends on
// this. Each row must be strictly distinct (unique ID) so
// `ListSince` returns them all.
func TestEngine_SnapshotEquity_MultipleSnapshotsBuildHistory(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()
	te.prices.Set("AAPL", decimal.NewFromFloat(100))

	for i := 0; i < 3; i++ {
		require.NoError(t, te.eng.SnapshotEquity(ctx))
		time.Sleep(5 * time.Millisecond)
	}
	rows, err := te.store.Equity.ListSince(ctx, te.p.ID, time.Now().Add(-time.Hour), 0)
	require.NoError(t, err)
	assert.Len(t, rows, 3, "three snapshots should have been persisted")
}

// TestEngine_LiveEquity_MatchesSnapshot ensures the LiveEquity helper
// (used by the GET /v1/portfolios/:id/equity handler) produces
// identical totals to the persisted snapshot — the whole point of the
// shared equity.Compute function is that the two never diverge.
func TestEngine_LiveEquity_MatchesSnapshot(t *testing.T) {
	te := buildEnv(t)
	ctx := context.Background()
	_, err := te.store.Positions.Apply(ctx, te.p.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(5), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)
	te.prices.Set("AAPL", decimal.NewFromFloat(120))

	live, err := te.eng.LiveEquity(ctx)
	require.NoError(t, err)
	require.NoError(t, te.eng.SnapshotEquity(ctx))
	snap, err := te.store.Equity.Latest(ctx, te.p.ID)
	require.NoError(t, err)

	// Values should match exactly; timing fields differ slightly.
	assert.Equal(t, live.CashCents, snap.CashCents)
	assert.Equal(t, live.PositionsCost, snap.PositionsCost)
	assert.Equal(t, live.PositionsMTM, snap.PositionsMTM)
	assert.Equal(t, live.UnrealizedCents, snap.UnrealizedCents)
	assert.Equal(t, live.EquityCents, snap.EquityCents)
}
