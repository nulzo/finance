package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
)

// TestAPI_EquityLive hits GET /v1/portfolios/:id/equity after seeding
// a position and a matching quote. Verifies the handler returns a
// shape that the frontend can consume directly (matching
// equity.Valuation).
func TestAPI_EquityLive(t *testing.T) {
	te := setup(t)
	ctx := context.Background()

	// Seed: 4 shares AAPL @ $100 avg, quote is $150 (from setup).
	_, err := te.store.Positions.Apply(ctx, te.portfolio.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(4), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)

	rec := te.do(t, http.MethodGet, fmt.Sprintf("/v1/portfolios/%s/equity", te.portfolio.ID), nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())

	var got struct {
		CashCents       int64  `json:"cash_cents"`
		PositionsCost   int64  `json:"positions_cost"`
		PositionsMTM    int64  `json:"positions_mtm"`
		UnrealizedCents int64  `json:"unrealized_cents"`
		EquityCents     int64  `json:"equity_cents"`
		OpenPositions   int    `json:"open_positions"`
		Positions       []struct {
			Symbol           string `json:"symbol"`
			UnrealizedCents  int64  `json:"unrealized_cents"`
			MarketValueCents int64  `json:"market_value_cents"`
			Priced           bool   `json:"priced"`
		} `json:"positions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	// cost = 4 * 100 = 400; mtm = 4 * 150 = 600; unrealised = +200
	assert.EqualValues(t, 40_000, got.PositionsCost)
	assert.EqualValues(t, 60_000, got.PositionsMTM)
	assert.EqualValues(t, 20_000, got.UnrealizedCents)
	// equity = cash (1M cents) + mtm (60k) = 1_060_000
	assert.EqualValues(t, 1_060_000, got.EquityCents)
	require.Len(t, got.Positions, 1)
	assert.Equal(t, "AAPL", got.Positions[0].Symbol)
	assert.True(t, got.Positions[0].Priced)
	assert.EqualValues(t, 20_000, got.Positions[0].UnrealizedCents)
}

// TestAPI_EquityHistory asserts that snapshots written by the engine
// are queryable via the history endpoint and returned oldest-first so
// recharts can consume them without a re-sort.
func TestAPI_EquityHistory(t *testing.T) {
	te := setup(t)
	ctx := context.Background()
	_, err := te.store.Positions.Apply(ctx, te.portfolio.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(2), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)

	// Capture three snapshots. The engine helper is shared by the
	// real loop and HTTP tests, so this is a faithful simulation of
	// what the frontend sees after ~15 minutes of runtime.
	for i := 0; i < 3; i++ {
		require.NoError(t, te.srv.Deps.Engine.SnapshotEquity(ctx))
	}

	rec := te.do(t, http.MethodGet,
		fmt.Sprintf("/v1/portfolios/%s/equity/history?since=1h", te.portfolio.ID), nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())

	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	assert.Len(t, rows, 3)
	// ordering: taken_at ascending
	for i := 1; i < len(rows); i++ {
		prev := rows[i-1]["taken_at"].(string)
		cur := rows[i]["taken_at"].(string)
		assert.LessOrEqual(t, prev, cur, "history must be oldest-first")
	}
}

// TestAPI_EquityHistory_EmptyReturnsEmptyArray guarantees the
// "no snapshots yet" branch returns `[]`, not `null` — the frontend
// treats them differently (null forces an error toast).
func TestAPI_EquityHistory_EmptyReturnsEmptyArray(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodGet,
		fmt.Sprintf("/v1/portfolios/%s/equity/history", te.portfolio.ID), nil)
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "[]", rec.Body.String())
}

// TestAPI_PositionsPnL verifies the per-position unrealised P&L
// endpoint returns the same leg numbers as the live equity handler.
func TestAPI_PositionsPnL(t *testing.T) {
	te := setup(t)
	ctx := context.Background()
	_, err := te.store.Positions.Apply(ctx, te.portfolio.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(1), domain.NewMoneyFromFloat(120))
	require.NoError(t, err)

	rec := te.do(t, http.MethodGet,
		fmt.Sprintf("/v1/portfolios/%s/positions/pnl", te.portfolio.ID), nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var rows []struct {
		Symbol          string `json:"symbol"`
		UnrealizedCents int64  `json:"unrealized_cents"`
		UnrealizedPct   float64 `json:"unrealized_pct"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	// cost = 120, mtm = 150 → +30; pct = 30/120 = 0.25
	assert.EqualValues(t, 3_000, rows[0].UnrealizedCents)
	assert.InDelta(t, 0.25, rows[0].UnrealizedPct, 0.001)
}

// TestAPI_AnalyticsSummary bundles cash, unrealised, realised, and
// day-change into a single payload. This covers the single-
// round-trip contract that powers the Overview header cards.
func TestAPI_AnalyticsSummary(t *testing.T) {
	te := setup(t)
	ctx := context.Background()
	_, err := te.store.Positions.Apply(ctx, te.portfolio.ID, "AAPL", domain.SideBuy,
		decimal.NewFromInt(2), domain.NewMoneyFromFloat(100))
	require.NoError(t, err)

	rec := te.do(t, http.MethodGet,
		fmt.Sprintf("/v1/portfolios/%s/analytics/summary", te.portfolio.ID), nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var out struct {
		CashCents          int64 `json:"cash_cents"`
		UnrealizedCents    int64 `json:"unrealized_cents"`
		EquityCents        int64 `json:"equity_cents"`
		OpenPositions      int   `json:"open_positions"`
		DayChangeAvailable bool  `json:"day_change_available"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.EqualValues(t, 1_000_000, out.CashCents)
	// 2 * 150 − 2 * 100 = 100 → 10_000 cents
	assert.EqualValues(t, 10_000, out.UnrealizedCents)
	assert.EqualValues(t, 1_000_000+30_000, out.EquityCents)
	assert.Equal(t, 1, out.OpenPositions)
	// With zero snapshots the day-change is unavailable — caller
	// UX shows "—" instead of a fake $0 delta.
	assert.False(t, out.DayChangeAvailable)
}
