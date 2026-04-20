package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// PositionRepo tracks per-symbol exposure.
type PositionRepo struct{ db *sqlx.DB }

// Get returns the open position for a symbol, or nil if none.
func (r *PositionRepo) Get(ctx context.Context, portfolioID, symbol string) (*domain.Position, error) {
	var p domain.Position
	err := r.db.GetContext(ctx, &p, `
        SELECT * FROM positions WHERE portfolio_id=? AND symbol=?
    `, portfolioID, symbol)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns every position in a portfolio.
func (r *PositionRepo) List(ctx context.Context, portfolioID string) ([]domain.Position, error) {
	var ps []domain.Position
	if err := r.db.SelectContext(ctx, &ps, `
        SELECT * FROM positions WHERE portfolio_id=? ORDER BY symbol
    `, portfolioID); err != nil {
		return nil, err
	}
	return ps, nil
}

// Upsert writes a position, merging quantity and avg cost.
// The caller provides the final (merged) values already computed.
func (r *PositionRepo) Upsert(ctx context.Context, p *domain.Position) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	p.UpdatedAt = now
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO positions (id, portfolio_id, symbol, quantity, avg_cost_cents, realized_cents, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(portfolio_id, symbol) DO UPDATE SET
            quantity = excluded.quantity,
            avg_cost_cents = excluded.avg_cost_cents,
            realized_cents = excluded.realized_cents,
            updated_at = excluded.updated_at
    `, p.ID, p.PortfolioID, p.Symbol,
		p.Quantity.String(), int64(p.AvgCostCents), int64(p.RealizedCents),
		p.CreatedAt, p.UpdatedAt)
	return err
}

// Delete removes a position row (typically when quantity == 0).
func (r *PositionRepo) Delete(ctx context.Context, portfolioID, symbol string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM positions WHERE portfolio_id=? AND symbol=?`, portfolioID, symbol)
	return err
}

// Apply updates (or creates) a position based on a fill. It returns the
// updated position after applying. It is the caller's responsibility to
// call it within a transaction if atomicity with other writes is required.
func (r *PositionRepo) Apply(ctx context.Context, portfolioID, symbol string, side domain.Side, qty decimal.Decimal, priceCents domain.Money) (*domain.Position, error) {
	pos, err := r.Get(ctx, portfolioID, symbol)
	if err != nil {
		return nil, err
	}
	if pos == nil {
		pos = &domain.Position{
			PortfolioID: portfolioID,
			Symbol:      symbol,
		}
	}
	switch side {
	case domain.SideBuy:
		// New average cost = (existing value + fill value) / (existing qty + fill qty)
		existingValue := pos.Quantity.Mul(pos.AvgCostCents.Dollars())
		fillValue := qty.Mul(priceCents.Dollars())
		newQty := pos.Quantity.Add(qty)
		if newQty.IsZero() {
			pos.AvgCostCents = 0
		} else {
			avg := existingValue.Add(fillValue).Div(newQty)
			pos.AvgCostCents = domain.NewMoneyFromDecimal(avg)
		}
		pos.Quantity = newQty
	case domain.SideSell:
		// Realise gains at average cost.
		realized := priceCents.Dollars().Sub(pos.AvgCostCents.Dollars()).Mul(qty)
		pos.RealizedCents += domain.NewMoneyFromDecimal(realized)
		pos.Quantity = pos.Quantity.Sub(qty)
		if pos.Quantity.IsNegative() {
			pos.Quantity = decimal.Zero
		}
	}
	if pos.Quantity.IsZero() {
		if pos.ID != "" {
			if err := r.Delete(ctx, portfolioID, symbol); err != nil {
				return nil, err
			}
		}
		pos.AvgCostCents = 0
		return pos, nil
	}
	if err := r.Upsert(ctx, pos); err != nil {
		return nil, err
	}
	return pos, nil
}
