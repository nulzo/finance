package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nulzo/trader/internal/domain"
)

// NewsRepo persists news items.
type NewsRepo struct{ db *sqlx.DB }

// Insert adds a new item, deduplicated by URL. Returns true if inserted.
func (r *NewsRepo) Insert(ctx context.Context, n *domain.NewsItem) (bool, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	res, err := r.db.ExecContext(ctx, `
        INSERT INTO news_items (id, source, url, title, summary, symbols, sentiment, relevance, pub_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(url) DO NOTHING
    `, n.ID, n.Source, n.URL, n.Title, n.Summary, n.Symbols, n.Sentiment, n.Relevance, n.PubAt, n.CreatedAt)
	if err != nil {
		return false, err
	}
	c, _ := res.RowsAffected()
	return c > 0, nil
}

// Recent returns the newest items capped at limit.
func (r *NewsRepo) Recent(ctx context.Context, limit int) ([]domain.NewsItem, error) {
	if limit <= 0 {
		limit = 50
	}
	var ns []domain.NewsItem
	if err := r.db.SelectContext(ctx, &ns, `SELECT * FROM news_items ORDER BY pub_at DESC LIMIT ?`, limit); err != nil {
		return nil, err
	}
	return ns, nil
}

// UpdateSentiment stores LLM-generated sentiment/relevance scores.
func (r *NewsRepo) UpdateSentiment(ctx context.Context, id string, sentiment, relevance float64, symbols string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE news_items SET sentiment=?, relevance=?, symbols=? WHERE id=?`,
		sentiment, relevance, symbols, id)
	return err
}

// GetByURL fetches by canonical URL, or nil if missing.
func (r *NewsRepo) GetByURL(ctx context.Context, url string) (*domain.NewsItem, error) {
	var n domain.NewsItem
	err := r.db.GetContext(ctx, &n, `SELECT * FROM news_items WHERE url=?`, url)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// SignalRepo persists normalized signals.
type SignalRepo struct{ db *sqlx.DB }

// Insert a signal. Prefer Upsert for ingest loops — Insert is retained
// for one-off seeds (e.g. tests, manual signals).
func (r *SignalRepo) Insert(ctx context.Context, s *domain.Signal) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = s.CreatedAt.Add(24 * time.Hour)
	}
	_, err := r.db.NamedExecContext(ctx, `
        INSERT INTO signals (id, kind, symbol, side, score, confidence, reason, ref_id, expires_at, created_at)
        VALUES (:id, :kind, :symbol, :side, :score, :confidence, :reason, :ref_id, :expires_at, :created_at)
    `, s)
	return err
}

// Upsert writes a signal keyed on (kind, symbol, side, ref_id). When a
// row with the same key already exists it is refreshed in place — this
// prevents the ingest loop from stacking thousands of duplicate rows
// per symbol over a day of ticks.
//
// RefID must be non-empty for Upsert to dedupe correctly. An empty
// RefID still inserts but the row will only be unique against other
// empty-RefID rows of the same (kind, symbol, side), matching the
// legacy Insert behaviour.
func (r *SignalRepo) Upsert(ctx context.Context, s *domain.Signal) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = s.CreatedAt.Add(24 * time.Hour)
	}
	_, err := r.db.NamedExecContext(ctx, `
        INSERT INTO signals (id, kind, symbol, side, score, confidence, reason, ref_id, expires_at, created_at)
        VALUES (:id, :kind, :symbol, :side, :score, :confidence, :reason, :ref_id, :expires_at, :created_at)
        ON CONFLICT(kind, symbol, side, ref_id) DO UPDATE SET
            score       = excluded.score,
            confidence  = excluded.confidence,
            reason      = excluded.reason,
            expires_at  = excluded.expires_at,
            created_at  = excluded.created_at
    `, s)
	return err
}

// Active returns unexpired signals. Optionally filtered by symbol.
func (r *SignalRepo) Active(ctx context.Context, symbol string, now time.Time) ([]domain.Signal, error) {
	var rows []domain.Signal
	query := `SELECT * FROM signals WHERE expires_at >= ?`
	args := []any{now}
	if symbol != "" {
		query += ` AND symbol = ?`
		args = append(args, symbol)
	}
	query += ` ORDER BY created_at DESC LIMIT 500`
	if err := r.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

// PurgeExpired deletes any rows whose expires_at is older than cutoff.
// It returns the number of rows removed so the caller can log
// cleanup progress.
//
// The engine should call this every ingest tick; without it the signals
// table grows unbounded and the unique index slowly fills with
// tombstones.
func (r *SignalRepo) PurgeExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM signals WHERE expires_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DecisionRepo persists decisions.
type DecisionRepo struct{ db *sqlx.DB }

// Insert a decision.
func (r *DecisionRepo) Insert(ctx context.Context, d *domain.Decision) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.SignalIDs == "" {
		d.SignalIDs = "[]"
	}
	_, err := r.db.NamedExecContext(ctx, `
        INSERT INTO decisions (id, portfolio_id, symbol, action, score, confidence, target_usd, reasoning, model_used, signal_ids, executed_id, created_at)
        VALUES (:id, :portfolio_id, :symbol, :action, :score, :confidence, :target_usd, :reasoning, :model_used, :signal_ids, :executed_id, :created_at)
    `, d)
	return err
}

// List decisions for a portfolio.
func (r *DecisionRepo) List(ctx context.Context, portfolioID string, limit int) ([]domain.Decision, error) {
	if limit <= 0 {
		limit = 50
	}
	var ds []domain.Decision
	if err := r.db.SelectContext(ctx, &ds, `
        SELECT * FROM decisions WHERE portfolio_id=? ORDER BY created_at DESC LIMIT ?
    `, portfolioID, limit); err != nil {
		return nil, err
	}
	return ds, nil
}

// SetExecuted records which order executed a decision.
func (r *DecisionRepo) SetExecuted(ctx context.Context, decisionID, orderID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE decisions SET executed_id=? WHERE id=?`, orderID, decisionID)
	return err
}

// AuditRepo keeps a durable audit trail.
type AuditRepo struct{ db *sqlx.DB }

// Record an audit event.
func (r *AuditRepo) Record(ctx context.Context, entity, entityID, action, details string) {
	_, _ = r.db.ExecContext(ctx, `
        INSERT INTO audit_log (id, entity, entity_id, action, details, created_at)
        VALUES (?, ?, ?, ?, ?, ?)
    `, uuid.NewString(), entity, entityID, action, details, time.Now().UTC())
}

// List returns recent audit rows.
func (r *AuditRepo) List(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryxContext(ctx, `SELECT * FROM audit_log ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0, limit)
	for rows.Next() {
		m := map[string]any{}
		if err := rows.MapScan(m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
