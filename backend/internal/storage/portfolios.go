package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nulzo/trader/internal/domain"
)

// PortfolioRepo persists portfolios.
type PortfolioRepo struct{ db *sqlx.DB }

// Create inserts a portfolio.
func (r *PortfolioRepo) Create(ctx context.Context, p *domain.Portfolio) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	_, err := r.db.NamedExecContext(ctx, `
        INSERT INTO portfolios (id, name, mode, cash_cents, reserved_cents, created_at, updated_at)
        VALUES (:id, :name, :mode, :cash_cents, :reserved_cents, :created_at, :updated_at)
    `, p)
	return err
}

// Get returns a portfolio by id.
func (r *PortfolioRepo) Get(ctx context.Context, id string) (*domain.Portfolio, error) {
	var p domain.Portfolio
	if err := r.db.GetContext(ctx, &p, `SELECT * FROM portfolios WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByName returns a portfolio matching name.
func (r *PortfolioRepo) GetByName(ctx context.Context, name string) (*domain.Portfolio, error) {
	var p domain.Portfolio
	if err := r.db.GetContext(ctx, &p, `SELECT * FROM portfolios WHERE name=?`, name); err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns every portfolio.
func (r *PortfolioRepo) List(ctx context.Context) ([]domain.Portfolio, error) {
	var ps []domain.Portfolio
	if err := r.db.SelectContext(ctx, &ps, `SELECT * FROM portfolios ORDER BY created_at`); err != nil {
		return nil, err
	}
	return ps, nil
}

// UpdateCash atomically mutates the cash/reserved balances.
func (r *PortfolioRepo) UpdateCash(ctx context.Context, id string, deltaCash, deltaReserved domain.Money) error {
	_, err := r.db.ExecContext(ctx, `
        UPDATE portfolios
           SET cash_cents = cash_cents + ?,
               reserved_cents = reserved_cents + ?,
               updated_at = ?
         WHERE id = ?
    `, int64(deltaCash), int64(deltaReserved), time.Now().UTC(), id)
	return err
}

// AddReservation increments reserved cash without touching the cash
// balance. Use when a buy is about to be submitted so available funds
// correctly reflect the in-flight commitment.
func (r *PortfolioRepo) AddReservation(ctx context.Context, id string, amount domain.Money) error {
	if amount.IsZero() {
		return nil
	}
	return r.UpdateCash(ctx, id, 0, amount)
}

// ReleaseReservation decrements reserved cash by `amount`, clamping
// at zero so a double-release or out-of-order fill can't flip the
// column negative. The clamping is enforced at the SQL layer for
// atomicity across concurrent goroutines.
func (r *PortfolioRepo) ReleaseReservation(ctx context.Context, id string, amount domain.Money) error {
	if amount.IsZero() {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
        UPDATE portfolios
           SET reserved_cents = MAX(0, reserved_cents - ?),
               updated_at = ?
         WHERE id = ?
    `, int64(amount), time.Now().UTC(), id)
	return err
}

// SetCash sets absolute values of cash and reserved.
func (r *PortfolioRepo) SetCash(ctx context.Context, id string, cash, reserved domain.Money) error {
	_, err := r.db.ExecContext(ctx, `
        UPDATE portfolios SET cash_cents=?, reserved_cents=?, updated_at=? WHERE id=?
    `, int64(cash), int64(reserved), time.Now().UTC(), id)
	return err
}
