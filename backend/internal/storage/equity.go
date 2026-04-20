package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nulzo/trader/internal/domain"
)

// EquitySnapshot is a single point-in-time valuation of a portfolio.
// All monetary columns are stored as integer cents (domain.Money) for
// exact arithmetic; float conversion happens only at the JSON boundary
// when the frontend renders charts. See migration 007 for a detailed
// rationale for each column.
type EquitySnapshot struct {
	ID               string       `db:"id"                json:"id"`
	PortfolioID      string       `db:"portfolio_id"      json:"portfolio_id"`
	TakenAt          time.Time    `db:"taken_at"          json:"taken_at"`
	CashCents        domain.Money `db:"cash_cents"        json:"cash_cents"`
	PositionsCost    domain.Money `db:"positions_cost"    json:"positions_cost"`
	PositionsMTM     domain.Money `db:"positions_mtm"     json:"positions_mtm"`
	RealizedCents    domain.Money `db:"realized_cents"    json:"realized_cents"`
	UnrealizedCents  domain.Money `db:"unrealized_cents"  json:"unrealized_cents"`
	EquityCents      domain.Money `db:"equity_cents"      json:"equity_cents"`
	OpenPositions    int          `db:"open_positions"    json:"open_positions"`
	PricedPositions  int          `db:"priced_positions"  json:"priced_positions"`
}

// EquitySnapshotRepo persists point-in-time portfolio valuations.
type EquitySnapshotRepo struct{ db *sqlx.DB }

// Insert writes a snapshot. IDs and timestamps are generated when
// missing so callers can write the cheapest possible struct literal at
// the snapshot site.
func (r *EquitySnapshotRepo) Insert(ctx context.Context, s *EquitySnapshot) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.TakenAt.IsZero() {
		s.TakenAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO equity_snapshots
            (id, portfolio_id, taken_at,
             cash_cents, positions_cost, positions_mtm,
             realized_cents, unrealized_cents, equity_cents,
             open_positions, priced_positions)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `,
		s.ID, s.PortfolioID, s.TakenAt,
		int64(s.CashCents), int64(s.PositionsCost), int64(s.PositionsMTM),
		int64(s.RealizedCents), int64(s.UnrealizedCents), int64(s.EquityCents),
		s.OpenPositions, s.PricedPositions,
	)
	return err
}

// Latest returns the most recent snapshot, or nil when none exists.
// The caller should fall back to a live computation in that case so
// the UI isn't blank immediately after the first deploy.
func (r *EquitySnapshotRepo) Latest(ctx context.Context, portfolioID string) (*EquitySnapshot, error) {
	var s EquitySnapshot
	err := r.db.GetContext(ctx, &s, `
        SELECT * FROM equity_snapshots
        WHERE portfolio_id = ?
        ORDER BY taken_at DESC
        LIMIT 1
    `, portfolioID)
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// ListSince returns every snapshot at-or-after `since`, oldest first,
// which is the direction recharts expects for line/area charts. The
// result is capped at `limit` rows so a pathological retention window
// doesn't swamp the API response; limit ≤ 0 defaults to 5000 (~4 days
// at 1-minute granularity, or ~17 days at 5-minute granularity — far
// more than any existing chart renders).
func (r *EquitySnapshotRepo) ListSince(ctx context.Context, portfolioID string, since time.Time, limit int) ([]EquitySnapshot, error) {
	if limit <= 0 {
		limit = 5000
	}
	var rows []EquitySnapshot
	err := r.db.SelectContext(ctx, &rows, `
        SELECT * FROM equity_snapshots
        WHERE portfolio_id = ? AND taken_at >= ?
        ORDER BY taken_at ASC
        LIMIT ?
    `, portfolioID, since, limit)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// PurgeOlderThan deletes snapshots with taken_at < cutoff so the table
// doesn't grow unboundedly. The snapshot loop calls this with a
// configurable retention window (default 90 days). Returns the number
// of rows deleted for observability.
func (r *EquitySnapshotRepo) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
        DELETE FROM equity_snapshots WHERE taken_at < ?
    `, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
