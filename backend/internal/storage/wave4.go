package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nulzo/trader/internal/domain"
)

// InsiderRepo persists Form 4 insider transactions.
type InsiderRepo struct{ db *sqlx.DB }

// Insert adds a new insider trade, ignoring duplicates by raw_hash.
// Returns true if a new row was inserted.
func (r *InsiderRepo) Insert(ctx context.Context, t *domain.InsiderTrade) (bool, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.Source == "" {
		t.Source = "quiver"
	}
	res, err := r.db.ExecContext(ctx, `
        INSERT INTO insider_trades
            (id, symbol, insider_name, insider_title, side, shares,
             price_cents, value_usd, transacted_at, filed_at,
             source, raw_hash, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(raw_hash) DO NOTHING
    `, t.ID, t.Symbol, t.InsiderName, t.InsiderTitle, t.Side, t.Shares,
		t.PriceCents, t.ValueUSD, t.TransactedAt, t.FiledAt,
		t.Source, t.RawHash, t.CreatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Since returns insider trades filed on/after t, newest first.
func (r *InsiderRepo) Since(ctx context.Context, t time.Time) ([]domain.InsiderTrade, error) {
	var ts []domain.InsiderTrade
	if err := r.db.SelectContext(ctx, &ts, `
        SELECT * FROM insider_trades WHERE filed_at >= ? ORDER BY filed_at DESC, transacted_at DESC
    `, t); err != nil {
		return nil, err
	}
	return ts, nil
}

// BySymbol returns recent insider trades for a symbol since `since`.
func (r *InsiderRepo) BySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.InsiderTrade, error) {
	var ts []domain.InsiderTrade
	if err := r.db.SelectContext(ctx, &ts, `
        SELECT * FROM insider_trades WHERE symbol=? AND transacted_at >= ? ORDER BY transacted_at DESC
    `, symbol, since); err != nil {
		return nil, err
	}
	return ts, nil
}

// ListRecent returns the newest insider trades for the UI.
func (r *InsiderRepo) ListRecent(ctx context.Context, limit int) ([]domain.InsiderTrade, error) {
	if limit <= 0 {
		limit = 100
	}
	var ts []domain.InsiderTrade
	if err := r.db.SelectContext(ctx, &ts, `
        SELECT * FROM insider_trades ORDER BY filed_at DESC LIMIT ?
    `, limit); err != nil {
		return nil, err
	}
	return ts, nil
}

// SocialRepo persists social-media (WSB, Twitter) mention rollups.
type SocialRepo struct{ db *sqlx.DB }

// Insert adds a new social bucket row, ignoring duplicates by raw_hash.
func (r *SocialRepo) Insert(ctx context.Context, p *domain.SocialPost) (bool, error) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	if p.Source == "" {
		p.Source = "quiver"
	}
	res, err := r.db.ExecContext(ctx, `
        INSERT INTO social_posts
            (id, symbol, platform, mentions, sentiment, followers,
             bucket_at, source, raw_hash, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(raw_hash) DO NOTHING
    `, p.ID, p.Symbol, p.Platform, p.Mentions, p.Sentiment, p.Followers,
		p.BucketAt, p.Source, p.RawHash, p.CreatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Since returns social posts bucketed on/after t, newest first.
func (r *SocialRepo) Since(ctx context.Context, t time.Time) ([]domain.SocialPost, error) {
	var ps []domain.SocialPost
	if err := r.db.SelectContext(ctx, &ps, `
        SELECT * FROM social_posts WHERE bucket_at >= ? ORDER BY bucket_at DESC
    `, t); err != nil {
		return nil, err
	}
	return ps, nil
}

// BySymbol returns recent social buckets for a symbol.
func (r *SocialRepo) BySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.SocialPost, error) {
	var ps []domain.SocialPost
	if err := r.db.SelectContext(ctx, &ps, `
        SELECT * FROM social_posts WHERE symbol=? AND bucket_at >= ? ORDER BY bucket_at DESC
    `, symbol, since); err != nil {
		return nil, err
	}
	return ps, nil
}

// ListRecent returns the newest social posts for the UI.
func (r *SocialRepo) ListRecent(ctx context.Context, limit int) ([]domain.SocialPost, error) {
	if limit <= 0 {
		limit = 100
	}
	var ps []domain.SocialPost
	if err := r.db.SelectContext(ctx, &ps, `
        SELECT * FROM social_posts ORDER BY bucket_at DESC LIMIT ?
    `, limit); err != nil {
		return nil, err
	}
	return ps, nil
}

// LobbyingRepo persists corporate lobbying events.
type LobbyingRepo struct{ db *sqlx.DB }

// Insert upserts an event by raw_hash.
func (r *LobbyingRepo) Insert(ctx context.Context, e *domain.LobbyingEvent) (bool, error) {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.Source == "" {
		e.Source = "quiver"
	}
	res, err := r.db.ExecContext(ctx, `
        INSERT INTO lobbying_events
            (id, symbol, client, registrant, issue, amount_usd,
             filed_at, period, source, raw_hash, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(raw_hash) DO NOTHING
    `, e.ID, e.Symbol, e.Client, e.Registrant, e.Issue, e.AmountUSD,
		e.FiledAt, e.Period, e.Source, e.RawHash, e.CreatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// BySymbol returns recent lobbying events for a symbol.
func (r *LobbyingRepo) BySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.LobbyingEvent, error) {
	var es []domain.LobbyingEvent
	if err := r.db.SelectContext(ctx, &es, `
        SELECT * FROM lobbying_events WHERE symbol=? AND filed_at >= ? ORDER BY filed_at DESC
    `, symbol, since); err != nil {
		return nil, err
	}
	return es, nil
}

// ListRecent returns the newest lobbying events.
func (r *LobbyingRepo) ListRecent(ctx context.Context, limit int) ([]domain.LobbyingEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var es []domain.LobbyingEvent
	if err := r.db.SelectContext(ctx, &es, `
        SELECT * FROM lobbying_events ORDER BY filed_at DESC LIMIT ?
    `, limit); err != nil {
		return nil, err
	}
	return es, nil
}

// GovContractRepo persists federal contract awards.
type GovContractRepo struct{ db *sqlx.DB }

// Insert upserts a contract by raw_hash.
func (r *GovContractRepo) Insert(ctx context.Context, c *domain.GovContract) (bool, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.Source == "" {
		c.Source = "quiver"
	}
	res, err := r.db.ExecContext(ctx, `
        INSERT INTO gov_contracts
            (id, symbol, agency, description, amount_usd, awarded_at,
             source, raw_hash, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(raw_hash) DO NOTHING
    `, c.ID, c.Symbol, c.Agency, c.Description, c.AmountUSD, c.AwardedAt,
		c.Source, c.RawHash, c.CreatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// BySymbol returns recent contracts for a symbol.
func (r *GovContractRepo) BySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.GovContract, error) {
	var cs []domain.GovContract
	if err := r.db.SelectContext(ctx, &cs, `
        SELECT * FROM gov_contracts WHERE symbol=? AND awarded_at >= ? ORDER BY awarded_at DESC
    `, symbol, since); err != nil {
		return nil, err
	}
	return cs, nil
}

// ListRecent returns the newest contracts.
func (r *GovContractRepo) ListRecent(ctx context.Context, limit int) ([]domain.GovContract, error) {
	if limit <= 0 {
		limit = 100
	}
	var cs []domain.GovContract
	if err := r.db.SelectContext(ctx, &cs, `
        SELECT * FROM gov_contracts ORDER BY awarded_at DESC LIMIT ?
    `, limit); err != nil {
		return nil, err
	}
	return cs, nil
}

// ShortVolumeRepo persists daily off-exchange short volume.
type ShortVolumeRepo struct{ db *sqlx.DB }

// Upsert replaces an existing (symbol, day) row or inserts it.
func (r *ShortVolumeRepo) Upsert(ctx context.Context, s *domain.ShortVolume) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.Source == "" {
		s.Source = "quiver"
	}
	// Clamp the ratio defensively; some days report short > total
	// due to reporting-lag artefacts, and a denormalised ratio
	// would break threshold comparisons downstream.
	if s.ShortRatio < 0 {
		s.ShortRatio = 0
	}
	if s.ShortRatio > 1 {
		s.ShortRatio = 1
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO short_volume
            (symbol, day, short_volume, total_volume, short_exempt_volume,
             short_ratio, source, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(symbol, day) DO UPDATE SET
            short_volume        = excluded.short_volume,
            total_volume        = excluded.total_volume,
            short_exempt_volume = excluded.short_exempt_volume,
            short_ratio         = excluded.short_ratio,
            source              = excluded.source
    `, s.Symbol, s.Day, s.ShortVolume, s.TotalVolume, s.ShortExemptVolume,
		s.ShortRatio, s.Source, s.CreatedAt)
	return err
}

// BySymbol returns daily short volume rows for a symbol since t.
func (r *ShortVolumeRepo) BySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.ShortVolume, error) {
	var ss []domain.ShortVolume
	if err := r.db.SelectContext(ctx, &ss, `
        SELECT * FROM short_volume WHERE symbol=? AND day >= ? ORDER BY day DESC
    `, symbol, since); err != nil {
		return nil, err
	}
	return ss, nil
}

// LatestBySymbol returns the most recent short-volume row per symbol
// (for the engine's TechnicalContext enrichment step).
func (r *ShortVolumeRepo) LatestBySymbol(ctx context.Context, symbol string) (*domain.ShortVolume, error) {
	var s domain.ShortVolume
	err := r.db.GetContext(ctx, &s, `
        SELECT * FROM short_volume WHERE symbol=? ORDER BY day DESC LIMIT 1
    `, symbol)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
