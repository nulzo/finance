package equity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

func newPos(sym string, qty float64, avgDollars float64, realizedCents int64) domain.Position {
	return domain.Position{
		PortfolioID:   "p",
		Symbol:        sym,
		Quantity:      decimal.NewFromFloat(qty),
		AvgCostCents:  domain.NewMoneyFromFloat(avgDollars),
		RealizedCents: domain.Money(realizedCents),
	}
}

// quoteMap is a deterministic lookup: nil quote -> "not priced", error
// -> "lookup failed", present -> priced.
type quoteMap struct {
	prices map[string]decimal.Decimal
	errs   map[string]error
}

func (q quoteMap) Quote(_ context.Context, sym string) (*domain.Quote, error) {
	if err, ok := q.errs[sym]; ok {
		return nil, err
	}
	p, ok := q.prices[sym]
	if !ok {
		return nil, nil
	}
	return &domain.Quote{Symbol: sym, Price: p, Timestamp: time.Now().UTC()}, nil
}

func TestCompute_BasicMarkToMarket(t *testing.T) {
	port := &domain.Portfolio{ID: "p", CashCents: 100_000_00} // $100k cash
	positions := []domain.Position{
		newPos("AAPL", 10, 150.0, 0), // cost $1500
		newPos("MSFT", 5, 300.0, 0),  // cost $1500
	}
	quotes := quoteMap{
		prices: map[string]decimal.Decimal{
			"AAPL": decimal.NewFromFloat(175.0), // +$250 unrealised
			"MSFT": decimal.NewFromFloat(290.0), // -$50  unrealised
		},
	}
	v, err := Compute(context.Background(), port, positions, 50_00, quotes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if v.CashCents != 100_000_00 {
		t.Errorf("cash: got %d", v.CashCents)
	}
	if v.PositionsCost != 3_000_00 {
		t.Errorf("cost: got %d want 300000", v.PositionsCost)
	}
	// MTM = 10*175 + 5*290 = 1750 + 1450 = 3200
	if v.PositionsMTM != 3_200_00 {
		t.Errorf("mtm: got %d want 320000", v.PositionsMTM)
	}
	if v.UnrealizedCents != 200_00 {
		t.Errorf("unrealized: got %d want 20000", v.UnrealizedCents)
	}
	// Equity = cash + mtm (realised doesn't count — it's already in cash).
	if v.EquityCents != 103_200_00 {
		t.Errorf("equity: got %d want 10320000", v.EquityCents)
	}
	if v.RealizedCents != 50_00 {
		t.Errorf("realized echoed: got %d", v.RealizedCents)
	}
	if v.OpenPositions != 2 || v.PricedPositions != 2 {
		t.Errorf("counts: open=%d priced=%d", v.OpenPositions, v.PricedPositions)
	}
}

// TestCompute_UnquotedPositionsMarkAtCost guards the "no phantom loss"
// contract: a position with no live quote must appear with
// MarketValue == CostBasis, Unrealized == 0, and Priced == false so
// the UI can explicitly flag the stale row.
func TestCompute_UnquotedPositionsMarkAtCost(t *testing.T) {
	port := &domain.Portfolio{ID: "p", CashCents: 10_000_00}
	positions := []domain.Position{newPos("NONE", 3, 50.0, 0)}
	quotes := quoteMap{prices: map[string]decimal.Decimal{}} // empty
	v, err := Compute(context.Background(), port, positions, 0, quotes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(v.Positions) != 1 || v.Positions[0].Priced {
		t.Fatalf("expected unpriced position, got %+v", v.Positions)
	}
	if v.Positions[0].UnrealizedCents != 0 {
		t.Errorf("unrealized should be zero for unpriced, got %d", v.Positions[0].UnrealizedCents)
	}
	if v.PositionsMTM != v.PositionsCost {
		t.Errorf("unpriced: mtm should equal cost, got %d vs %d", v.PositionsMTM, v.PositionsCost)
	}
	if v.PricedPositions != 0 {
		t.Errorf("priced count: %d", v.PricedPositions)
	}
	if v.OpenPositions != 1 {
		t.Errorf("open count: %d", v.OpenPositions)
	}
}

// TestCompute_QuoteErrorDoesNotFailWholeValuation ensures that a
// per-symbol fetch error degrades gracefully; the rest of the
// portfolio still marks to market.
func TestCompute_QuoteErrorDoesNotFailWholeValuation(t *testing.T) {
	port := &domain.Portfolio{ID: "p", CashCents: 0}
	positions := []domain.Position{
		newPos("OK", 2, 100, 0),
		newPos("FAIL", 1, 50, 0),
	}
	quotes := quoteMap{
		prices: map[string]decimal.Decimal{"OK": decimal.NewFromFloat(110)},
		errs:   map[string]error{"FAIL": errors.New("provider down")},
	}
	v, err := Compute(context.Background(), port, positions, 0, quotes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if v.OpenPositions != 2 || v.PricedPositions != 1 {
		t.Errorf("counts: open=%d priced=%d", v.OpenPositions, v.PricedPositions)
	}
	// OK: cost 200, mtm 220 → +20
	// FAIL: cost 50, mtm 50 (fallback) → 0
	if v.UnrealizedCents != 20_00 {
		t.Errorf("unrealized: got %d want 2000", v.UnrealizedCents)
	}
}

// TestCompute_ZeroQuantityPositionsSkipped covers the case where a
// position row was not yet deleted after a full sell — quantity is 0
// but the row still exists for a beat. It should not contribute to
// any aggregate and should not appear in the positions breakdown.
func TestCompute_ZeroQuantityPositionsSkipped(t *testing.T) {
	port := &domain.Portfolio{ID: "p", CashCents: 0}
	positions := []domain.Position{
		newPos("ZERO", 0, 100, 0),
		newPos("LIVE", 1, 10, 0),
	}
	quotes := quoteMap{prices: map[string]decimal.Decimal{"LIVE": decimal.NewFromFloat(15)}}
	v, err := Compute(context.Background(), port, positions, 0, quotes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if v.OpenPositions != 1 {
		t.Errorf("zero-qty should be skipped, got open=%d", v.OpenPositions)
	}
	if len(v.Positions) != 1 || v.Positions[0].Symbol != "LIVE" {
		t.Errorf("positions: %+v", v.Positions)
	}
}

// TestCompute_UnrealizedPctRoundedAndSigned sanity-checks the
// percentage computation, including negative returns.
func TestCompute_UnrealizedPctRoundedAndSigned(t *testing.T) {
	port := &domain.Portfolio{ID: "p"}
	positions := []domain.Position{newPos("X", 1, 100, 0)}
	tests := []struct {
		mark float64
		want float64
	}{
		{110, 0.1000},
		{90, -0.1000},
		{100, 0.0},
		{133.3333, 0.3333},
	}
	for _, tc := range tests {
		q := quoteMap{prices: map[string]decimal.Decimal{"X": decimal.NewFromFloat(tc.mark)}}
		v, err := Compute(context.Background(), port, positions, 0, q)
		if err != nil {
			t.Fatalf("compute: %v", err)
		}
		got := v.Positions[0].UnrealizedPct
		if diff := got - tc.want; diff < -0.0001 || diff > 0.0001 {
			t.Errorf("mark=%v: pct got %v want %v", tc.mark, got, tc.want)
		}
	}
}

// TestCompute_NilPortfolioIsProgrammerError guards the invariant —
// callers must resolve the portfolio before calling Compute.
func TestCompute_NilPortfolioIsProgrammerError(t *testing.T) {
	if _, err := Compute(context.Background(), nil, nil, 0, nil); err == nil {
		t.Fatalf("expected error for nil portfolio")
	}
}
