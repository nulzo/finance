package broker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
)

func newMock(cash float64) (*broker.MockBroker, *broker.StaticPrices) {
	prices := broker.NewStaticPrices(map[string]decimal.Decimal{"AAPL": decimal.NewFromFloat(150)})
	b := broker.NewMockBroker(prices, decimal.NewFromFloat(cash), 0)
	return b, prices
}

func TestMock_BuyUpdatesCashAndPosition(t *testing.T) {
	b, _ := newMock(10_000)
	o := &domain.Order{Symbol: "AAPL", Side: domain.SideBuy, Type: domain.OrderTypeMarket, Quantity: decimal.NewFromInt(10)}
	bo, err := b.SubmitOrder(context.Background(), o)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusFilled, bo.Status)
	assert.Equal(t, "150", bo.FilledAvg.String())

	acct, _ := b.Account(context.Background())
	assert.Equal(t, "8500", acct.CashUSD.String())

	pos, _ := b.Positions(context.Background())
	require.Len(t, pos, 1)
	assert.Equal(t, "10", pos[0].Quantity.String())
	assert.Equal(t, "150", pos[0].AvgPrice.String())
}

func TestMock_BuyRejectsOnInsufficientFunds(t *testing.T) {
	b, _ := newMock(100)
	o := &domain.Order{Symbol: "AAPL", Side: domain.SideBuy, Type: domain.OrderTypeMarket, Quantity: decimal.NewFromInt(10)}
	_, err := b.SubmitOrder(context.Background(), o)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInsufficientFund))
}

func TestMock_SellWithoutPositionRejected(t *testing.T) {
	b, _ := newMock(10_000)
	o := &domain.Order{Symbol: "AAPL", Side: domain.SideSell, Type: domain.OrderTypeMarket, Quantity: decimal.NewFromInt(1)}
	_, err := b.SubmitOrder(context.Background(), o)
	require.Error(t, err)
}

func TestMock_SellReducesPosition(t *testing.T) {
	b, _ := newMock(10_000)
	_, err := b.SubmitOrder(context.Background(), &domain.Order{Symbol: "AAPL", Side: domain.SideBuy, Type: domain.OrderTypeMarket, Quantity: decimal.NewFromInt(5)})
	require.NoError(t, err)
	_, err = b.SubmitOrder(context.Background(), &domain.Order{Symbol: "AAPL", Side: domain.SideSell, Type: domain.OrderTypeMarket, Quantity: decimal.NewFromInt(3)})
	require.NoError(t, err)
	pos, _ := b.Positions(context.Background())
	require.Len(t, pos, 1)
	assert.Equal(t, "2", pos[0].Quantity.String())
}

// Hydrate must replace cash + positions from an external source so
// the mock broker can pick up where a previous process left off.
// Without this, on restart the DB still shows held positions but the
// broker thinks they're zero, and every sell is rejected.
func TestMock_HydrateRestoresCashAndPositions(t *testing.T) {
	b, _ := newMock(0)
	b.Hydrate(decimal.NewFromFloat(4_321.10), map[string]broker.BrokerPosition{
		"AAPL": {Quantity: decimal.NewFromInt(10), AvgPrice: decimal.NewFromFloat(120)},
		"MSFT": {Quantity: decimal.NewFromInt(5), AvgPrice: decimal.NewFromFloat(400)},
	})
	acct, err := b.Account(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "4321.1", acct.CashUSD.String())

	pos, err := b.Positions(context.Background())
	require.NoError(t, err)
	require.Len(t, pos, 2)

	// After hydration the broker must be willing to sell what the DB
	// says we have — this was the live bug.
	_, err = b.SubmitOrder(context.Background(), &domain.Order{
		Symbol: "AAPL", Side: domain.SideSell, Type: domain.OrderTypeMarket,
		Quantity: decimal.NewFromInt(3),
	})
	require.NoError(t, err, "hydrated position must be sellable")
}

// Hydrate is idempotent — calling it again replaces rather than
// accumulates, so a shared mock broker in tests doesn't leak state.
func TestMock_HydrateReplacesState(t *testing.T) {
	b, _ := newMock(1_000)
	b.Hydrate(decimal.NewFromInt(5_000), map[string]broker.BrokerPosition{
		"AAPL": {Quantity: decimal.NewFromInt(10), AvgPrice: decimal.NewFromFloat(100)},
	})
	b.Hydrate(decimal.NewFromInt(2_000), map[string]broker.BrokerPosition{
		"MSFT": {Quantity: decimal.NewFromInt(5), AvgPrice: decimal.NewFromFloat(400)},
	})
	pos, _ := b.Positions(context.Background())
	require.Len(t, pos, 1)
	assert.Equal(t, "MSFT", pos[0].Symbol)

	acct, _ := b.Account(context.Background())
	assert.Equal(t, "2000", acct.CashUSD.String())
}

// The error message on a sell attempt with no broker position must
// mention hydration so future operators can diagnose the mismatch.
func TestMock_SellWithoutPositionErrorMentionsHydration(t *testing.T) {
	b, _ := newMock(10_000)
	_, err := b.SubmitOrder(context.Background(), &domain.Order{
		Symbol: "AAPL", Side: domain.SideSell, Type: domain.OrderTypeMarket,
		Quantity: decimal.NewFromInt(1),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydrated")
}

func TestMock_LimitBuyNotCrossed(t *testing.T) {
	b, prices := newMock(10_000)
	prices.Set("AAPL", decimal.NewFromFloat(160))
	limit := decimal.NewFromFloat(150)
	_, err := b.SubmitOrder(context.Background(), &domain.Order{
		Symbol: "AAPL", Side: domain.SideBuy, Type: domain.OrderTypeLimit, LimitPrice: &limit, Quantity: decimal.NewFromInt(1),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrBrokerRejected))
}
