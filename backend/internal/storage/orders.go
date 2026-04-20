package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nulzo/trader/internal/domain"
)

// OrderRepo persists orders.
type OrderRepo struct{ db *sqlx.DB }

// Create inserts a new order. Zero values are initialised.
func (r *OrderRepo) Create(ctx context.Context, o *domain.Order) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	o.CreatedAt = now
	o.UpdatedAt = now
	if o.Status == "" {
		o.Status = domain.OrderStatusPending
	}
	_, err := r.db.NamedExecContext(ctx, `
        INSERT INTO orders
            (id, portfolio_id, symbol, side, type, time_in_force,
             quantity, limit_price, filled_qty, filled_avg_cents,
             status, broker_id, reason, decision_id, created_at, updated_at,
             submitted_at, filled_at)
        VALUES
            (:id, :portfolio_id, :symbol, :side, :type, :time_in_force,
             :quantity, :limit_price, :filled_qty, :filled_avg_cents,
             :status, :broker_id, :reason, :decision_id, :created_at, :updated_at,
             :submitted_at, :filled_at)
    `, o)
	return err
}

// Get fetches an order by id.
func (r *OrderRepo) Get(ctx context.Context, id string) (*domain.Order, error) {
	var o domain.Order
	if err := r.db.GetContext(ctx, &o, `SELECT * FROM orders WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &o, nil
}

// List returns recent orders for a portfolio, limited to n.
func (r *OrderRepo) List(ctx context.Context, portfolioID string, limit int) ([]domain.Order, error) {
	if limit <= 0 {
		limit = 50
	}
	var os []domain.Order
	if err := r.db.SelectContext(ctx, &os, `
        SELECT * FROM orders WHERE portfolio_id=? ORDER BY created_at DESC LIMIT ?
    `, portfolioID, limit); err != nil {
		return nil, err
	}
	return os, nil
}

// CountSince returns how many orders of any status have been created
// since t for a portfolio. Use CountSubmittedSince when enforcing the
// daily-order risk cap; including rejected rows in that count would
// double-penalise a day in which the risk engine was noisy.
func (r *OrderRepo) CountSince(ctx context.Context, portfolioID string, since time.Time) (int, error) {
	var n int
	err := r.db.GetContext(ctx, &n, `
        SELECT COUNT(*) FROM orders WHERE portfolio_id=? AND created_at >= ?
    `, portfolioID, since)
	return n, err
}

// CountSubmittedSince counts only orders that actually reached the
// broker (submitted / partially_filled / filled). Rejected and
// cancelled orders are excluded so the MAX_DAILY_ORDERS limit reflects
// real broker activity, not risk-engine churn.
func (r *OrderRepo) CountSubmittedSince(ctx context.Context, portfolioID string, since time.Time) (int, error) {
	var n int
	err := r.db.GetContext(ctx, &n, `
        SELECT COUNT(*) FROM orders
        WHERE portfolio_id=? AND created_at >= ?
          AND status IN ('submitted','partially_filled','filled')
    `, portfolioID, since)
	return n, err
}

// ListOpen returns orders that are still working at the broker, for
// the reconciliation loop to poll.
func (r *OrderRepo) ListOpen(ctx context.Context, portfolioID string, limit int) ([]domain.Order, error) {
	if limit <= 0 {
		limit = 100
	}
	var os []domain.Order
	if err := r.db.SelectContext(ctx, &os, `
        SELECT * FROM orders
        WHERE portfolio_id=?
          AND status IN ('pending','submitted','partially_filled')
          AND broker_id <> ''
        ORDER BY created_at DESC
        LIMIT ?
    `, portfolioID, limit); err != nil {
		return nil, err
	}
	return os, nil
}

// UpdateStatus updates status and broker fields.
func (r *OrderRepo) UpdateStatus(ctx context.Context, o *domain.Order) error {
	o.UpdatedAt = time.Now().UTC()
	_, err := r.db.NamedExecContext(ctx, `
        UPDATE orders SET
            status=:status,
            broker_id=:broker_id,
            filled_qty=:filled_qty,
            filled_avg_cents=:filled_avg_cents,
            reason=:reason,
            submitted_at=:submitted_at,
            filled_at=:filled_at,
            updated_at=:updated_at
        WHERE id=:id
    `, o)
	return err
}
