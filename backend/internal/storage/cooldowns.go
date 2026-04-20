package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// Cooldown is a per-(portfolio, symbol) suspension window. While a
// cooldown is active the decide loop skips the symbol without issuing
// any LLM call or broker request, except for ExitPolicy-driven sells
// which intentionally bypass cooldowns so losing positions can always
// be cut.
type Cooldown struct {
	PortfolioID string    `db:"portfolio_id" json:"portfolio_id"`
	Symbol      string    `db:"symbol"       json:"symbol"`
	Until       time.Time `db:"until_ts"     json:"until_ts"`
	Reason      string    `db:"reason"       json:"reason"`
	UpdatedAt   time.Time `db:"updated_at"   json:"updated_at"`
}

// Active reports whether this cooldown is still in effect at `now`.
func (c Cooldown) Active(now time.Time) bool { return now.Before(c.Until) }

// CooldownRepo persists cooldown rows.
type CooldownRepo struct{ db *sqlx.DB }

// Upsert sets the cooldown for a symbol. An earlier expiry is never
// overwritten by a later one with a shorter horizon — we always keep
// the later of the two, so a "daily cap" cooldown (until midnight) is
// not shortened by a subsequent generic 30-minute rejection.
func (r *CooldownRepo) Upsert(ctx context.Context, c *Cooldown) error {
	now := time.Now().UTC()
	c.UpdatedAt = now
	if c.Until.Before(now) {
		// A cooldown in the past is a no-op; don't persist it.
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO cooldowns (portfolio_id, symbol, until_ts, reason, updated_at)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(portfolio_id, symbol) DO UPDATE SET
            until_ts = CASE
                WHEN excluded.until_ts > cooldowns.until_ts THEN excluded.until_ts
                ELSE cooldowns.until_ts
            END,
            reason     = excluded.reason,
            updated_at = excluded.updated_at
    `, c.PortfolioID, c.Symbol, c.Until, c.Reason, c.UpdatedAt)
	return err
}

// Get returns the cooldown for a symbol or nil if none. Expired rows
// are returned as-is so callers can distinguish "no cooldown" from
// "expired cooldown still in the table"; most callers will prefer
// ActiveFor below.
func (r *CooldownRepo) Get(ctx context.Context, portfolioID, symbol string) (*Cooldown, error) {
	var c Cooldown
	err := r.db.GetContext(ctx, &c, `
        SELECT * FROM cooldowns WHERE portfolio_id = ? AND symbol = ?
    `, portfolioID, symbol)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ActiveFor returns the cooldown for a symbol only if still in effect.
// nil is returned when there's no row or the existing row has expired.
func (r *CooldownRepo) ActiveFor(ctx context.Context, portfolioID, symbol string, now time.Time) (*Cooldown, error) {
	c, err := r.Get(ctx, portfolioID, symbol)
	if err != nil {
		return nil, err
	}
	if c == nil || !c.Active(now) {
		return nil, nil
	}
	return c, nil
}

// ListActive returns all currently-active cooldowns for a portfolio,
// most-recently-set first. Useful for the UI.
func (r *CooldownRepo) ListActive(ctx context.Context, portfolioID string, now time.Time) ([]Cooldown, error) {
	var rows []Cooldown
	if err := r.db.SelectContext(ctx, &rows, `
        SELECT * FROM cooldowns
        WHERE portfolio_id = ? AND until_ts > ?
        ORDER BY updated_at DESC
    `, portfolioID, now); err != nil {
		return nil, err
	}
	return rows, nil
}

// PurgeExpired removes rows whose cooldown lapsed before `before`. The
// engine calls this periodically to keep the table compact.
func (r *CooldownRepo) PurgeExpired(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM cooldowns WHERE until_ts < ?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Clear explicitly removes a cooldown (e.g. when an operator resumes a
// symbol from the UI).
func (r *CooldownRepo) Clear(ctx context.Context, portfolioID, symbol string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cooldowns WHERE portfolio_id = ? AND symbol = ?`, portfolioID, symbol)
	return err
}
