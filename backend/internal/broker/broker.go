// Package broker provides an abstraction over trading venues so the rest
// of the application can place orders and query positions without caring
// whether execution is mocked, paper-traded, or live.
package broker

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// Account summarises a broker account.
type Account struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	CashUSD     decimal.Decimal `json:"cash_usd"`
	EquityUSD   decimal.Decimal `json:"equity_usd"`
	BuyingPower decimal.Decimal `json:"buying_power"`
	Currency    string          `json:"currency"`
}

// BrokerOrder is the broker's view of an order lifecycle state.
type BrokerOrder struct {
	BrokerID    string          `json:"broker_id"`
	Symbol      string          `json:"symbol"`
	Side        domain.Side     `json:"side"`
	Quantity    decimal.Decimal `json:"quantity"`
	FilledQty   decimal.Decimal `json:"filled_qty"`
	FilledAvg   decimal.Decimal `json:"filled_avg_price"`
	Status      domain.OrderStatus `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	SubmittedAt time.Time       `json:"submitted_at"`
	FilledAt    *time.Time      `json:"filled_at,omitempty"`
}

// BrokerPosition reported by the broker.
type BrokerPosition struct {
	Symbol    string          `json:"symbol"`
	Quantity  decimal.Decimal `json:"quantity"`
	AvgPrice  decimal.Decimal `json:"avg_price"`
	MarketVal decimal.Decimal `json:"market_value"`
}

// Broker is the execution interface. Implementations must be goroutine-safe.
type Broker interface {
	Name() string
	Account(ctx context.Context) (*Account, error)
	SubmitOrder(ctx context.Context, order *domain.Order) (*BrokerOrder, error)
	GetOrder(ctx context.Context, brokerID string) (*BrokerOrder, error)
	CancelOrder(ctx context.Context, brokerID string) error
	Positions(ctx context.Context) ([]BrokerPosition, error)
	Quote(ctx context.Context, symbol string) (*domain.Quote, error)
}
