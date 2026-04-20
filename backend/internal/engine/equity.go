package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/nulzo/trader/internal/equity"
	"github.com/nulzo/trader/internal/storage"
)

// SnapshotEquity takes a single equity snapshot and writes it to the
// `equity_snapshots` table. It's split out from the loop body so tests
// can drive snapshots deterministically without spawning a goroutine.
//
// The method is defensive: any single sub-step (quote fetch, realized
// lookup, DB insert) that fails logs a warning but lets the outer
// caller continue — a bad snapshot is worse than no snapshot. The
// returned error is for the caller's observability only; the engine
// loop deliberately swallows it (a failed tick must not crash the
// long-running process).
func (e *Engine) SnapshotEquity(ctx context.Context) error {
	if e.deps.Store == nil || e.deps.Store.Equity == nil {
		return nil
	}
	port, err := e.deps.Store.Portfolios.Get(ctx, e.deps.PortfolioID)
	if err != nil {
		return fmt.Errorf("load portfolio: %w", err)
	}
	if port == nil {
		return fmt.Errorf("portfolio %q not found", e.deps.PortfolioID)
	}
	positions, err := e.deps.Store.Positions.List(ctx, e.deps.PortfolioID)
	if err != nil {
		return fmt.Errorf("list positions: %w", err)
	}
	// Realised since inception: time.Time{} is treated by SQLite as
	// "1 AD", so the filter is effectively "no lower bound" and we
	// get a cumulative total.
	realized, err := e.deps.Store.Realized.SumSince(ctx, e.deps.PortfolioID, time.Time{})
	if err != nil {
		// Non-fatal: a broken realised lookup shouldn't kill the
		// rest of the snapshot. Chart will just undercount until the
		// next tick succeeds.
		e.deps.Log.Warn().Err(err).Msg("snapshot: realized sum failed; assuming 0")
		realized = 0
	}
	v, err := equity.Compute(ctx, port, positions, realized, e.deps.Market)
	if err != nil {
		return fmt.Errorf("compute: %w", err)
	}
	snap := &storage.EquitySnapshot{
		PortfolioID:      v.PortfolioID,
		TakenAt:          v.TakenAt,
		CashCents:        v.CashCents,
		PositionsCost:    v.PositionsCost,
		PositionsMTM:     v.PositionsMTM,
		RealizedCents:    v.RealizedCents,
		UnrealizedCents:  v.UnrealizedCents,
		EquityCents:      v.EquityCents,
		OpenPositions:    v.OpenPositions,
		PricedPositions:  v.PricedPositions,
	}
	if err := e.deps.Store.Equity.Insert(ctx, snap); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	e.deps.Log.Debug().
		Int64("equity_cents", int64(v.EquityCents)).
		Int64("unrealized_cents", int64(v.UnrealizedCents)).
		Int("open", v.OpenPositions).
		Int("priced", v.PricedPositions).
		Msg("equity snapshot")
	return nil
}

// LiveEquity computes the current valuation without persisting it.
// Used by the HTTP handler for `GET /v1/portfolios/:id/equity` so a
// user-facing request gets the freshest possible answer — identical
// arithmetic to the snapshot loop, just not written to the DB.
func (e *Engine) LiveEquity(ctx context.Context) (*equity.Valuation, error) {
	port, err := e.deps.Store.Portfolios.Get(ctx, e.deps.PortfolioID)
	if err != nil {
		return nil, err
	}
	if port == nil {
		return nil, fmt.Errorf("portfolio %q not found", e.deps.PortfolioID)
	}
	positions, err := e.deps.Store.Positions.List(ctx, e.deps.PortfolioID)
	if err != nil {
		return nil, err
	}
	realized, err := e.deps.Store.Realized.SumSince(ctx, e.deps.PortfolioID, time.Time{})
	if err != nil {
		realized = 0
	}
	return equity.Compute(ctx, port, positions, realized, e.deps.Market)
}
