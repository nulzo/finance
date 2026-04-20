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

// PoliticianRepo persists tracked politicians.
type PoliticianRepo struct{ db *sqlx.DB }

// Upsert inserts or updates a politician by name.
func (r *PoliticianRepo) Upsert(ctx context.Context, p *domain.Politician) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.TrackWeight == 0 {
		p.TrackWeight = 1.0
	}
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO politicians (id, name, chamber, party, state, track_weight, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(name) DO UPDATE SET
            chamber=excluded.chamber,
            party=excluded.party,
            state=excluded.state,
            track_weight=excluded.track_weight,
            updated_at=excluded.updated_at
    `, p.ID, p.Name, p.Chamber, p.Party, p.State, p.TrackWeight, p.CreatedAt, p.UpdatedAt)
	return err
}

// List returns all politicians.
func (r *PoliticianRepo) List(ctx context.Context) ([]domain.Politician, error) {
	var ps []domain.Politician
	if err := r.db.SelectContext(ctx, &ps, `SELECT * FROM politicians ORDER BY name`); err != nil {
		return nil, err
	}
	return ps, nil
}

// GetByName returns the politician with a case-insensitive name match.
func (r *PoliticianRepo) GetByName(ctx context.Context, name string) (*domain.Politician, error) {
	var p domain.Politician
	err := r.db.GetContext(ctx, &p, `SELECT * FROM politicians WHERE lower(name)=lower(?)`, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PoliticianTradeRepo persists disclosed politician trades.
type PoliticianTradeRepo struct{ db *sqlx.DB }

// Insert a trade, ignoring duplicates by raw_hash.
// Returns true if a new row was inserted.
func (r *PoliticianTradeRepo) Insert(ctx context.Context, t *domain.PoliticianTrade) (bool, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	res, err := r.db.ExecContext(ctx, `
        INSERT INTO politician_trades
            (id, politician_id, politician_name, chamber, symbol, asset_name,
             side, amount_min_usd, amount_max_usd, traded_at, disclosed_at,
             source, raw_hash, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(raw_hash) DO NOTHING
    `, t.ID, t.PoliticianID, t.PoliticianName, t.Chamber, t.Symbol, t.AssetName,
		t.Side, t.AmountMinUSD, t.AmountMaxUSD, t.TradedAt, t.DisclosedAt,
		t.Source, t.RawHash, t.CreatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListRecent returns the newest politician trades.
func (r *PoliticianTradeRepo) ListRecent(ctx context.Context, limit int) ([]domain.PoliticianTrade, error) {
	if limit <= 0 {
		limit = 100
	}
	var ts []domain.PoliticianTrade
	if err := r.db.SelectContext(ctx, &ts, `
        SELECT * FROM politician_trades ORDER BY disclosed_at DESC, traded_at DESC LIMIT ?
    `, limit); err != nil {
		return nil, err
	}
	return ts, nil
}

// BySymbol returns recent trades for a symbol.
func (r *PoliticianTradeRepo) BySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.PoliticianTrade, error) {
	var ts []domain.PoliticianTrade
	if err := r.db.SelectContext(ctx, &ts, `
        SELECT * FROM politician_trades WHERE symbol=? AND traded_at >= ? ORDER BY traded_at DESC
    `, symbol, since); err != nil {
		return nil, err
	}
	return ts, nil
}

// Since returns trades disclosed after t.
func (r *PoliticianTradeRepo) Since(ctx context.Context, t time.Time) ([]domain.PoliticianTrade, error) {
	var ts []domain.PoliticianTrade
	if err := r.db.SelectContext(ctx, &ts, `
        SELECT * FROM politician_trades WHERE disclosed_at >= ? ORDER BY disclosed_at DESC
    `, t); err != nil {
		return nil, err
	}
	return ts, nil
}

