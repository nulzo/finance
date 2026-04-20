package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
)

// RejectionSource classifies where a rejection originated. The
// frontend uses it to filter (e.g. "risk rejections only") and to
// colour-code rows.
type RejectionSource string

const (
	// RejectionSourceRisk is a pre-submit rejection by the risk
	// engine (blacklist, daily cap, exposure cap, below minimum
	// notional, etc).
	RejectionSourceRisk RejectionSource = "risk"
	// RejectionSourceBroker is a post-submit rejection coming back
	// from the broker (e.g. Alpaca 422 insufficient buying power,
	// asset not tradable, etc).
	RejectionSourceBroker RejectionSource = "broker"
	// RejectionSourceEngine is a pre-decide short-circuit inside
	// the engine itself — cooldown active, daily order cap reached,
	// position pinned near `MaxPositionUSD`. No decision row is ever
	// produced for these.
	RejectionSourceEngine RejectionSource = "engine"
)

// Rejection is a structured audit of a single trade that was not
// made. Compared to the free-form `audit_log` row that the engine
// has always emitted, Rejection is queryable: the UI filters by
// portfolio / symbol / source / time window without LIKE-scanning
// reason strings.
type Rejection struct {
	ID          string          `db:"id"           json:"id"`
	PortfolioID string          `db:"portfolio_id" json:"portfolio_id"`
	Symbol      string          `db:"symbol"       json:"symbol"`
	DecisionID  *string         `db:"decision_id"  json:"decision_id,omitempty"`
	Side        string          `db:"side"         json:"side"`
	Source      RejectionSource `db:"source"       json:"source"`
	Reason      string          `db:"reason"       json:"reason"`
	TargetUSD   decimal.Decimal `db:"target_usd"   json:"target_usd"`
	CreatedAt   time.Time       `db:"created_at"   json:"created_at"`
}

// RejectionRepo persists rejection rows.
type RejectionRepo struct{ db *sqlx.DB }

// Insert records a rejection. ID and CreatedAt are populated if
// missing so the engine can call this with a zero-value struct and
// the right defaults appear. Insert is best-effort from the caller's
// point of view — a failed Insert must never take down an execute /
// reconcile cycle.
func (r *RejectionRepo) Insert(ctx context.Context, rj *Rejection) error {
	if rj.ID == "" {
		rj.ID = uuid.NewString()
	}
	if rj.CreatedAt.IsZero() {
		rj.CreatedAt = time.Now().UTC()
	}
	if rj.Source == "" {
		rj.Source = RejectionSourceRisk
	}
	if len(rj.Reason) > 512 {
		rj.Reason = rj.Reason[:512] + "…"
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO rejections
            (id, portfolio_id, symbol, decision_id, side, source, reason, target_usd, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, rj.ID, rj.PortfolioID, rj.Symbol, rj.DecisionID, rj.Side, string(rj.Source),
		rj.Reason, rj.TargetUSD.String(), rj.CreatedAt)
	return err
}

// ListSince returns rejections for a portfolio newer than `since`,
// most recent first. limit<=0 defaults to 200.
func (r *RejectionRepo) ListSince(ctx context.Context, portfolioID string, since time.Time, limit int) ([]Rejection, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows []Rejection
	if err := r.db.SelectContext(ctx, &rows, `
        SELECT * FROM rejections
        WHERE portfolio_id = ? AND created_at >= ?
        ORDER BY created_at DESC
        LIMIT ?
    `, portfolioID, since, limit); err != nil {
		return nil, err
	}
	return rows, nil
}

// CountSince counts rejections matching the given source (use empty
// string for any source) for a portfolio since `since`. The engine's
// circuit breaker uses this to count broker rejects per window.
func (r *RejectionRepo) CountSince(ctx context.Context, portfolioID string, source RejectionSource, since time.Time) (int, error) {
	q := `SELECT COUNT(*) FROM rejections WHERE portfolio_id = ? AND created_at >= ?`
	args := []any{portfolioID, since}
	if source != "" {
		q += ` AND source = ?`
		args = append(args, string(source))
	}
	var n int
	if err := r.db.GetContext(ctx, &n, q, args...); err != nil {
		return 0, err
	}
	return n, nil
}
