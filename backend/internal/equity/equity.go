// Package equity computes portfolio valuations (cash, cost basis,
// mark-to-market, realised and unrealised P&L) in a way that's both
// usable by the engine's periodic snapshot loop and by live HTTP
// requests.
//
// The package is deliberately free of DB concerns: it takes an
// already-loaded portfolio + positions + quote lookup and produces a
// value object. That keeps it unit-testable without spinning up SQLite
// and lets the snapshot loop and API handlers share identical
// accounting — if they drift, they do so in one place only.
package equity

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// PositionValuation describes a single open position marked to market.
// Every field except `Quote*` is derived from the position row alone;
// `Quote*` fields are zero when a live quote was unavailable, in which
// case the row is marked at cost (so `UnrealizedCents == 0`) and the
// `Priced` flag is false. The UI uses `Priced` to annotate stale
// valuations instead of silently pretending a stock didn't move.
type PositionValuation struct {
	Symbol           string          `json:"symbol"`
	Quantity         decimal.Decimal `json:"quantity"`
	AvgCostCents     domain.Money    `json:"avg_cost_cents"`
	MarkCents        domain.Money    `json:"mark_cents"`
	CostBasisCents   domain.Money    `json:"cost_basis_cents"`
	MarketValueCents domain.Money    `json:"market_value_cents"`
	RealizedCents    domain.Money    `json:"realized_cents"`
	UnrealizedCents  domain.Money    `json:"unrealized_cents"`
	// UnrealizedPct is the unrealized return on cost basis, expressed
	// as a decimal (e.g. 0.1234 = +12.34%). Zero when cost basis is
	// zero (which shouldn't happen for an open position but we guard
	// anyway). Rounded to four decimals so float JSON noise doesn't
	// flap the UI.
	UnrealizedPct float64   `json:"unrealized_pct"`
	QuoteAt       time.Time `json:"quote_at,omitempty"`
	Priced        bool      `json:"priced"`
}

// Valuation is a complete portfolio valuation at a single point in
// time. The shape mirrors storage.EquitySnapshot so the two can be
// converted 1:1 without information loss — except that Valuation
// carries the per-position breakdown, which the snapshot table doesn't
// persist (too expensive for a time-series table; reconstructible on
// demand from positions + quotes).
type Valuation struct {
	PortfolioID     string              `json:"portfolio_id"`
	TakenAt         time.Time           `json:"taken_at"`
	CashCents       domain.Money        `json:"cash_cents"`
	PositionsCost   domain.Money        `json:"positions_cost"`
	PositionsMTM    domain.Money        `json:"positions_mtm"`
	RealizedCents   domain.Money        `json:"realized_cents"`
	UnrealizedCents domain.Money        `json:"unrealized_cents"`
	EquityCents     domain.Money        `json:"equity_cents"`
	OpenPositions   int                 `json:"open_positions"`
	PricedPositions int                 `json:"priced_positions"`
	Positions       []PositionValuation `json:"positions"`
}

// QuoteLookup fetches a single live quote. The snapshot loop and the
// live endpoint both call through this interface so we can back it
// with the cached market provider in prod and a deterministic map in
// tests. Errors for individual symbols should never fail the whole
// valuation — the caller falls back to cost for that leg.
type QuoteLookup interface {
	Quote(ctx context.Context, symbol string) (*domain.Quote, error)
}

// QuoteLookupFunc adapts a plain function to QuoteLookup.
type QuoteLookupFunc func(ctx context.Context, symbol string) (*domain.Quote, error)

// Quote implements QuoteLookup.
func (f QuoteLookupFunc) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	return f(ctx, symbol)
}

// Compute returns a fully-populated Valuation for a portfolio.
//
// `portfolio` supplies the cash leg; `positions` supplies cost basis
// and per-symbol realised P&L; `realized` is the cumulative realised
// P&L since inception (typically `Store.Realized.SumSince(..., zero)`).
// `quotes` is consulted once per symbol; a nil lookup is permitted
// (everything will be marked at cost).
//
// Errors are returned only for unrecoverable programmer mistakes
// (e.g. nil portfolio); per-symbol quote failures degrade gracefully
// to cost-basis marks with Priced=false.
func Compute(ctx context.Context, portfolio *domain.Portfolio, positions []domain.Position, realized domain.Money, quotes QuoteLookup) (*Valuation, error) {
	if portfolio == nil {
		return nil, errors.New("equity: portfolio is nil")
	}
	v := &Valuation{
		PortfolioID:   portfolio.ID,
		TakenAt:       time.Now().UTC(),
		CashCents:     portfolio.CashCents,
		RealizedCents: realized,
		Positions:     make([]PositionValuation, 0, len(positions)),
	}
	for _, p := range positions {
		if p.Quantity.IsZero() {
			continue
		}
		v.OpenPositions++
		cost := domain.Money(p.AvgCostCents.Dollars().Mul(p.Quantity).Mul(decimal.NewFromInt(100)).Round(0).IntPart())
		pv := PositionValuation{
			Symbol:         p.Symbol,
			Quantity:       p.Quantity,
			AvgCostCents:   p.AvgCostCents,
			CostBasisCents: cost,
			RealizedCents:  p.RealizedCents,
			// Default mark/market-value to cost so a missing quote
			// produces a zero unrealised leg (not a phantom loss).
			MarkCents:        p.AvgCostCents,
			MarketValueCents: cost,
		}
		if quotes != nil {
			if q, err := quotes.Quote(ctx, p.Symbol); err == nil && q != nil && q.Price.GreaterThan(decimal.Zero) {
				mark := domain.NewMoneyFromDecimal(q.Price)
				mtm := domain.Money(q.Price.Mul(p.Quantity).Mul(decimal.NewFromInt(100)).Round(0).IntPart())
				pv.MarkCents = mark
				pv.MarketValueCents = mtm
				pv.QuoteAt = q.Timestamp
				pv.Priced = true
				v.PricedPositions++
			}
		}
		pv.UnrealizedCents = pv.MarketValueCents - pv.CostBasisCents
		if !cost.IsZero() {
			frac, _ := pv.UnrealizedCents.Dollars().Div(cost.Dollars()).Float64()
			// Round to 4dp so JSON round-trips stay stable.
			pv.UnrealizedPct = roundTo(frac, 4)
		}
		v.PositionsCost += pv.CostBasisCents
		v.PositionsMTM += pv.MarketValueCents
		v.Positions = append(v.Positions, pv)
	}
	v.UnrealizedCents = v.PositionsMTM - v.PositionsCost
	v.EquityCents = v.CashCents + v.PositionsMTM
	return v, nil
}

// roundTo rounds v to `decimals` decimal places. Kept local so the
// package has no dependency beyond decimal + domain.
func roundTo(v float64, decimals int) float64 {
	pow := 1.0
	for i := 0; i < decimals; i++ {
		pow *= 10
	}
	if v >= 0 {
		return float64(int64(v*pow+0.5)) / pow
	}
	return float64(int64(v*pow-0.5)) / pow
}
