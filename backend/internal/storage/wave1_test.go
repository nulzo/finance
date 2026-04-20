package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/storage"
)

func seedPortfolio(t *testing.T, s *storage.Store) *domain.Portfolio {
	t.Helper()
	p := &domain.Portfolio{Name: "main-" + uuid.NewString(), Mode: "mock", CashCents: 1_000_000}
	require.NoError(t, s.Portfolios.Create(context.Background(), p))
	return p
}

func TestRealizedEvents_SumSince(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	// Yesterday's loss must not count against today's window.
	yesterday := time.Now().UTC().Add(-30 * time.Hour)
	require.NoError(t, s.Realized.Insert(ctx, &storage.RealizedEvent{
		PortfolioID:   p.ID,
		Symbol:        "AAPL",
		Quantity:      decimal.NewFromInt(10),
		RealizedCents: domain.NewMoneyFromFloat(-50),
		CreatedAt:     yesterday,
	}))
	// Today: +$25, -$10 net +$15.
	require.NoError(t, s.Realized.Insert(ctx, &storage.RealizedEvent{
		PortfolioID:   p.ID,
		Symbol:        "AAPL",
		Quantity:      decimal.NewFromInt(5),
		RealizedCents: domain.NewMoneyFromFloat(25),
	}))
	require.NoError(t, s.Realized.Insert(ctx, &storage.RealizedEvent{
		PortfolioID:   p.ID,
		Symbol:        "TSLA",
		Quantity:      decimal.NewFromInt(2),
		RealizedCents: domain.NewMoneyFromFloat(-10),
	}))

	today := time.Now().UTC().Truncate(24 * time.Hour)
	got, err := s.Realized.SumSince(ctx, p.ID, today)
	require.NoError(t, err)
	assert.Equal(t, domain.NewMoneyFromFloat(15), got)

	all, err := s.Realized.SumSince(ctx, p.ID, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, domain.NewMoneyFromFloat(-35), all)
}

func TestCooldowns_UpsertKeepsLaterExpiry(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	now := time.Now().UTC()
	// Install a long cooldown (6h).
	long := &storage.Cooldown{PortfolioID: p.ID, Symbol: "NDAQ", Until: now.Add(6 * time.Hour), Reason: "daily cap"}
	require.NoError(t, s.Cooldowns.Upsert(ctx, long))

	// Attempting to overwrite with a shorter (30m) cooldown MUST NOT
	// shorten the active window — otherwise a generic rejection would
	// silently release a "stop until midnight" pause.
	short := &storage.Cooldown{PortfolioID: p.ID, Symbol: "NDAQ", Until: now.Add(30 * time.Minute), Reason: "other"}
	require.NoError(t, s.Cooldowns.Upsert(ctx, short))

	got, err := s.Cooldowns.Get(ctx, p.ID, "NDAQ")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.WithinDuration(t, long.Until, got.Until, time.Second)

	// Longer wins.
	longer := &storage.Cooldown{PortfolioID: p.ID, Symbol: "NDAQ", Until: now.Add(12 * time.Hour), Reason: "extended"}
	require.NoError(t, s.Cooldowns.Upsert(ctx, longer))
	got, _ = s.Cooldowns.Get(ctx, p.ID, "NDAQ")
	require.NotNil(t, got)
	assert.WithinDuration(t, longer.Until, got.Until, time.Second)
}

func TestCooldowns_ActiveForAndList(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	now := time.Now().UTC()
	require.NoError(t, s.Cooldowns.Upsert(ctx, &storage.Cooldown{
		PortfolioID: p.ID, Symbol: "AAPL", Until: now.Add(30 * time.Minute), Reason: "risk",
	}))
	require.NoError(t, s.Cooldowns.Upsert(ctx, &storage.Cooldown{
		PortfolioID: p.ID, Symbol: "MSFT", Until: now.Add(2 * time.Hour), Reason: "risk",
	}))
	// An expired row still sits in the table; ActiveFor must ignore it.
	require.NoError(t, s.Cooldowns.Upsert(ctx, &storage.Cooldown{
		PortfolioID: p.ID, Symbol: "TSLA", Until: now.Add(1 * time.Hour), Reason: "risk",
	}))
	// Force TSLA's row to be stale by overwriting its until_ts directly.
	_, err := s.DB.ExecContext(ctx, `UPDATE cooldowns SET until_ts = ? WHERE symbol = ?`, now.Add(-time.Minute), "TSLA")
	require.NoError(t, err)

	active, err := s.Cooldowns.ActiveFor(ctx, p.ID, "AAPL", now)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, "AAPL", active.Symbol)

	expired, err := s.Cooldowns.ActiveFor(ctx, p.ID, "TSLA", now)
	require.NoError(t, err)
	assert.Nil(t, expired)

	list, err := s.Cooldowns.ListActive(ctx, p.ID, now)
	require.NoError(t, err)
	var syms []string
	for _, c := range list {
		syms = append(syms, c.Symbol)
	}
	assert.ElementsMatch(t, []string{"AAPL", "MSFT"}, syms)
}

func TestCooldowns_Clear(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	now := time.Now().UTC()
	require.NoError(t, s.Cooldowns.Upsert(ctx, &storage.Cooldown{
		PortfolioID: p.ID, Symbol: "AAPL", Until: now.Add(time.Hour), Reason: "risk",
	}))
	require.NoError(t, s.Cooldowns.Clear(ctx, p.ID, "AAPL"))

	got, err := s.Cooldowns.Get(ctx, p.ID, "AAPL")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// CountSubmittedSince must exclude rejected rows — otherwise a noisy
// risk engine on one tick permanently shortens the day for the engine.
func TestOrders_CountSubmittedSinceExcludesRejected(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)

	for i, st := range []domain.OrderStatus{
		domain.OrderStatusSubmitted,
		domain.OrderStatusFilled,
		domain.OrderStatusRejected,
		domain.OrderStatusRejected,
		domain.OrderStatusCancelled,
	} {
		o := &domain.Order{
			ID:          uuid.NewString(),
			PortfolioID: p.ID,
			Symbol:      "AAPL",
			Side:        domain.SideBuy,
			Type:        domain.OrderTypeMarket,
			TimeInForce: domain.TIFDay,
			Quantity:    decimal.NewFromInt(1),
			Status:      domain.OrderStatusPending,
			Reason:      "seed",
			CreatedAt:   dayStart.Add(time.Duration(i) * time.Minute),
			UpdatedAt:   dayStart.Add(time.Duration(i) * time.Minute),
		}
		require.NoError(t, s.Orders.Create(ctx, o))
		o.Status = st
		require.NoError(t, s.Orders.UpdateStatus(ctx, o))
	}

	all, err := s.Orders.CountSince(ctx, p.ID, dayStart)
	require.NoError(t, err)
	assert.Equal(t, 5, all)

	submitted, err := s.Orders.CountSubmittedSince(ctx, p.ID, dayStart)
	require.NoError(t, err)
	assert.Equal(t, 2, submitted, "cancelled + rejected must not count")
}

func TestOrders_ListOpen(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	// submitted + has broker_id -> included.
	// filled -> excluded.
	// submitted + no broker_id -> excluded (nothing to poll for).
	cases := []struct {
		sym    string
		status domain.OrderStatus
		broker string
		want   bool
	}{
		{"A", domain.OrderStatusSubmitted, "b1", true},
		{"B", domain.OrderStatusFilled, "b2", false},
		{"C", domain.OrderStatusSubmitted, "", false},
		{"D", domain.OrderStatusPartial, "b3", true},
	}
	for i, c := range cases {
		o := &domain.Order{
			ID: uuid.NewString(), PortfolioID: p.ID, Symbol: c.sym,
			Side: domain.SideBuy, Type: domain.OrderTypeMarket, TimeInForce: domain.TIFDay,
			Quantity: decimal.NewFromInt(1), Status: domain.OrderStatusPending,
			CreatedAt: time.Now().UTC().Add(time.Duration(-i) * time.Minute),
		}
		require.NoError(t, s.Orders.Create(ctx, o))
		o.Status = c.status
		o.BrokerID = c.broker
		require.NoError(t, s.Orders.UpdateStatus(ctx, o))
	}

	open, err := s.Orders.ListOpen(ctx, p.ID, 100)
	require.NoError(t, err)
	got := map[string]bool{}
	for _, o := range open {
		got[o.Symbol] = true
	}
	for _, c := range cases {
		assert.Equal(t, c.want, got[c.sym], "symbol %s", c.sym)
	}
}

func TestPortfolios_ReservationHelpers(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	p := seedPortfolio(t, s)

	require.NoError(t, s.Portfolios.AddReservation(ctx, p.ID, domain.Money(10_000)))
	got, _ := s.Portfolios.Get(ctx, p.ID)
	assert.Equal(t, int64(10_000), got.ReservedCents.Cents())

	require.NoError(t, s.Portfolios.ReleaseReservation(ctx, p.ID, domain.Money(3_000)))
	got, _ = s.Portfolios.Get(ctx, p.ID)
	assert.Equal(t, int64(7_000), got.ReservedCents.Cents())

	// Over-release must clamp at zero — no negative reservations.
	require.NoError(t, s.Portfolios.ReleaseReservation(ctx, p.ID, domain.Money(999_999)))
	got, _ = s.Portfolios.Get(ctx, p.ID)
	assert.Equal(t, int64(0), got.ReservedCents.Cents(), "reservation must clamp at zero")
}
