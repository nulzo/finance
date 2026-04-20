package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// RealizedEvent records the realized P&L delta produced by a single
// sell-side fill. The risk engine sums these since UTC midnight to
// evaluate the MAX_DAILY_LOSS_USD limit — aggregating from an
// append-only event log is simpler and more correct across restarts
// than snapshotting the cumulative `positions.realized_cents` column.
type RealizedEvent struct {
	ID            string          `db:"id"             json:"id"`
	PortfolioID   string          `db:"portfolio_id"   json:"portfolio_id"`
	Symbol        string          `db:"symbol"         json:"symbol"`
	Quantity      decimal.Decimal `db:"quantity"       json:"quantity"`
	RealizedCents domain.Money    `db:"realized_cents" json:"realized_cents"`
	OrderID       *string         `db:"order_id"       json:"order_id,omitempty"`
	CreatedAt     time.Time       `db:"created_at"     json:"created_at"`
}

// RealizedEventRepo persists realized-PnL events.
type RealizedEventRepo struct{ db *sqlx.DB }

// Insert records one fill's realized delta. The delta may be positive
// (profit) or negative (loss). Zero-value deltas are persisted too so
// the event log is a complete record of activity, not just P&L moves.
func (r *RealizedEventRepo) Insert(ctx context.Context, e *RealizedEvent) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO realized_events
            (id, portfolio_id, symbol, quantity, realized_cents, order_id, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, e.ID, e.PortfolioID, e.Symbol, e.Quantity.String(), int64(e.RealizedCents), e.OrderID, e.CreatedAt)
	return err
}

// SumSince returns the realized P&L (in cents) for a portfolio since t.
// A missing or empty window returns zero.
func (r *RealizedEventRepo) SumSince(ctx context.Context, portfolioID string, since time.Time) (domain.Money, error) {
	var cents int64
	err := r.db.GetContext(ctx, &cents, `
        SELECT COALESCE(SUM(realized_cents), 0) FROM realized_events
        WHERE portfolio_id = ? AND created_at >= ?
    `, portfolioID, since)
	if err != nil {
		return 0, err
	}
	return domain.Money(cents), nil
}

// DailyPnL is a single day's net realized P&L bucket. The `Day`
// field is normalised to UTC midnight so the frontend can rely on
// a consistent granularity regardless of the caller's timezone.
type DailyPnL struct {
	Day           time.Time    `db:"day"            json:"day"`
	RealizedCents domain.Money `db:"realized_cents" json:"realized_cents"`
	EventCount    int          `db:"event_count"    json:"event_count"`
}

// DailySince returns a zero-filled daily P&L series for [since, now]
// where each bucket is one UTC day. Days with no events are still
// included with zero P&L and zero event count so the frontend chart
// renders a continuous x-axis. Callers typically pass `since` as
// 00:00 UTC of the earliest day they want to render.
//
// Aggregation is done in Go (not SQL) because SQLite's date(...)
// function handles TIMESTAMP formatting inconsistently across
// embedded drivers; pulling rows and bucketing locally avoids that
// portability trap and costs nothing for typical window sizes
// (worst case a few thousand rows over a year).
func (r *RealizedEventRepo) DailySince(ctx context.Context, portfolioID string, since time.Time) ([]DailyPnL, error) {
	since = since.UTC().Truncate(24 * time.Hour)
	var rows []RealizedEvent
	if err := r.db.SelectContext(ctx, &rows, `
        SELECT * FROM realized_events
        WHERE portfolio_id = ? AND created_at >= ?
        ORDER BY created_at ASC
    `, portfolioID, since); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	days := int(now.Sub(since)/(24*time.Hour)) + 1
	if days <= 0 {
		days = 1
	}
	series := make([]DailyPnL, days)
	for i := 0; i < days; i++ {
		series[i].Day = since.Add(time.Duration(i) * 24 * time.Hour)
	}
	for _, ev := range rows {
		idx := int(ev.CreatedAt.UTC().Truncate(24*time.Hour).Sub(since) / (24 * time.Hour))
		if idx < 0 || idx >= days {
			continue
		}
		series[idx].RealizedCents += ev.RealizedCents
		series[idx].EventCount++
	}
	return series, nil
}

// ListSince returns raw rows for reporting / UI.
func (r *RealizedEventRepo) ListSince(ctx context.Context, portfolioID string, since time.Time, limit int) ([]RealizedEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []RealizedEvent
	if err := r.db.SelectContext(ctx, &rows, `
        SELECT * FROM realized_events
        WHERE portfolio_id = ? AND created_at >= ?
        ORDER BY created_at DESC
        LIMIT ?
    `, portfolioID, since, limit); err != nil {
		return nil, err
	}
	return rows, nil
}
