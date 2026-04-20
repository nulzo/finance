// Package storage provides persistence backed by SQLite via sqlx.
// It exposes a Store that groups repositories and handles migrations.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // pure-go SQLite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store aggregates database access and the repositories built on it.
type Store struct {
	DB         *sqlx.DB
	Portfolios *PortfolioRepo
	Orders     *OrderRepo
	Positions  *PositionRepo
	Politicians *PoliticianRepo
	PTrades    *PoliticianTradeRepo
	News       *NewsRepo
	Signals    *SignalRepo
	Decisions  *DecisionRepo
	Audit      *AuditRepo
	LLMCalls   *LLMCallRepo
	Realized   *RealizedEventRepo
	Cooldowns  *CooldownRepo
	Rejections *RejectionRepo
	Insiders   *InsiderRepo
	Social     *SocialRepo
	Lobbying   *LobbyingRepo
	Contracts  *GovContractRepo
	Shorts     *ShortVolumeRepo
	Equity     *EquitySnapshotRepo
}

// Open connects to the database URL and runs migrations.
func Open(ctx context.Context, url string) (*Store, error) {
	db, err := sqlx.ConnectContext(ctx, "sqlite", url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite prefers serialised writes
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	s.Portfolios = &PortfolioRepo{db: db}
	s.Orders = &OrderRepo{db: db}
	s.Positions = &PositionRepo{db: db}
	s.Politicians = &PoliticianRepo{db: db}
	s.PTrades = &PoliticianTradeRepo{db: db}
	s.News = &NewsRepo{db: db}
	s.Signals = &SignalRepo{db: db}
	s.Decisions = &DecisionRepo{db: db}
	s.Audit = &AuditRepo{db: db}
	s.LLMCalls = &LLMCallRepo{db: db}
	s.Realized = &RealizedEventRepo{db: db}
	s.Cooldowns = &CooldownRepo{db: db}
	s.Rejections = &RejectionRepo{db: db}
	s.Insiders = &InsiderRepo{db: db}
	s.Social = &SocialRepo{db: db}
	s.Lobbying = &LobbyingRepo{db: db}
	s.Contracts = &GovContractRepo{db: db}
	s.Shorts = &ShortVolumeRepo{db: db}
	s.Equity = &EquitySnapshotRepo{db: db}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.DB.Close() }

// Ping verifies the database connection is alive. Used by the /readyz
// HTTP probe so Kubernetes removes unhealthy pods from the Service.
func (s *Store) Ping(ctx context.Context) error { return s.DB.PingContext(ctx) }

func migrate(ctx context.Context, db *sqlx.DB) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		buf, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx, string(buf)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

// IsNotFound reports whether err represents a missing row.
func IsNotFound(err error) bool {
	return err != nil && errors.Is(err, sql.ErrNoRows)
}
