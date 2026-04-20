package broker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// PriceSource provides reference prices to the mock broker.
// Returning (decimal.Zero, err) forces the broker to reject the order.
type PriceSource interface {
	Quote(ctx context.Context, symbol string) (*domain.Quote, error)
}

// StaticPrices is a simple PriceSource backed by an in-memory map.
type StaticPrices struct {
	mu     sync.RWMutex
	prices map[string]decimal.Decimal
}

// NewStaticPrices builds a StaticPrices from an initial map.
func NewStaticPrices(init map[string]decimal.Decimal) *StaticPrices {
	p := &StaticPrices{prices: map[string]decimal.Decimal{}}
	for k, v := range init {
		p.prices[k] = v
	}
	return p
}

// Set records a price for a symbol.
func (s *StaticPrices) Set(symbol string, price decimal.Decimal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prices[symbol] = price
}

// Quote returns the stored price or an error.
func (s *StaticPrices) Quote(_ context.Context, symbol string) (*domain.Quote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.prices[symbol]
	if !ok {
		return nil, fmt.Errorf("%w: no price for %s", domain.ErrProviderFailure, symbol)
	}
	return &domain.Quote{Symbol: symbol, Price: p, Timestamp: time.Now().UTC()}, nil
}

// MockBroker fills orders immediately using a PriceSource. It maintains
// internal positions/cash balance so it can be used as a standalone
// execution venue when no real broker is configured.
type MockBroker struct {
	mu       sync.Mutex
	prices   PriceSource
	orders   map[string]*BrokerOrder
	pos      map[string]*BrokerPosition
	cash     decimal.Decimal
	slippage float64 // fractional slippage
	rng      *rand.Rand
}

// NewMockBroker returns a MockBroker seeded with the given cash balance.
func NewMockBroker(prices PriceSource, cash decimal.Decimal, slippage float64) *MockBroker {
	return &MockBroker{
		prices:   prices,
		orders:   map[string]*BrokerOrder{},
		pos:      map[string]*BrokerPosition{},
		cash:     cash,
		slippage: slippage,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Hydrate replaces the in-memory cash + position state. Call this on
// process startup to sync the mock broker with whatever's already in
// the DB — without it, positions from prior runs will cause sells to
// be rejected with "insufficient position" even though the DB shows
// the shares are held.
//
// Positions in the input map are addressed by symbol; the values give
// quantity and average price (in dollars). Missing symbols are left
// alone, so callers can hydrate incrementally if they wish.
func (m *MockBroker) Hydrate(cash decimal.Decimal, positions map[string]BrokerPosition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cash = cash
	m.pos = map[string]*BrokerPosition{}
	for sym, p := range positions {
		if p.Quantity.IsZero() {
			continue
		}
		cp := p
		cp.Symbol = sym
		m.pos[sym] = &cp
	}
}

// Name returns a human-readable broker name.
func (m *MockBroker) Name() string { return "mock" }

// Account summarises the mock account.
func (m *MockBroker) Account(_ context.Context) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &Account{
		ID:          "mock-account",
		Status:      "active",
		CashUSD:     m.cash,
		EquityUSD:   m.cash.Add(m.equityLocked()),
		BuyingPower: m.cash,
		Currency:    "USD",
	}, nil
}

// equityLocked assumes caller holds the mutex; values positions at avg price.
func (m *MockBroker) equityLocked() decimal.Decimal {
	total := decimal.Zero
	for _, p := range m.pos {
		total = total.Add(p.Quantity.Mul(p.AvgPrice))
	}
	return total
}

// SubmitOrder fills immediately at current price +/- slippage.
func (m *MockBroker) SubmitOrder(ctx context.Context, o *domain.Order) (*BrokerOrder, error) {
	if !o.Side.Valid() {
		return nil, fmt.Errorf("%w: invalid side", domain.ErrValidation)
	}
	if o.Quantity.IsNegative() || o.Quantity.IsZero() {
		return nil, fmt.Errorf("%w: quantity must be positive", domain.ErrValidation)
	}
	q, err := m.prices.Quote(ctx, o.Symbol)
	if err != nil {
		return nil, err
	}
	price := q.Price
	if m.slippage > 0 {
		drift := decimal.NewFromFloat((m.rng.Float64()*2 - 1) * m.slippage)
		price = price.Mul(decimal.NewFromInt(1).Add(drift))
		if price.LessThanOrEqual(decimal.Zero) {
			price = q.Price
		}
	}
	// Enforce limit price if provided.
	if o.Type == domain.OrderTypeLimit && o.LimitPrice != nil {
		lp := *o.LimitPrice
		if o.Side == domain.SideBuy && price.GreaterThan(lp) {
			return m.rejected(o, "limit not crossed"), fmt.Errorf("%w: limit not crossed", domain.ErrBrokerRejected)
		}
		if o.Side == domain.SideSell && price.LessThan(lp) {
			return m.rejected(o, "limit not crossed"), fmt.Errorf("%w: limit not crossed", domain.ErrBrokerRejected)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	notional := o.Quantity.Mul(price)
	now := time.Now().UTC()

	switch o.Side {
	case domain.SideBuy:
		if m.cash.LessThan(notional) {
			return nil, fmt.Errorf("%w: cash %s < notional %s", domain.ErrInsufficientFund, m.cash, notional)
		}
		m.cash = m.cash.Sub(notional)
		p := m.pos[o.Symbol]
		if p == nil {
			p = &BrokerPosition{Symbol: o.Symbol}
			m.pos[o.Symbol] = p
		}
		existingVal := p.Quantity.Mul(p.AvgPrice)
		newQty := p.Quantity.Add(o.Quantity)
		if newQty.IsZero() {
			p.AvgPrice = decimal.Zero
		} else {
			p.AvgPrice = existingVal.Add(notional).Div(newQty)
		}
		p.Quantity = newQty
		p.MarketVal = newQty.Mul(price)
	case domain.SideSell:
		p := m.pos[o.Symbol]
		if p == nil {
			return nil, fmt.Errorf("%w: insufficient position: no %s held in broker (have the positions been hydrated from storage?)",
				domain.ErrBrokerRejected, o.Symbol)
		}
		if p.Quantity.LessThan(o.Quantity) {
			return nil, fmt.Errorf("%w: insufficient position: requested %s %s, broker holds %s",
				domain.ErrBrokerRejected, o.Quantity.String(), o.Symbol, p.Quantity.String())
		}
		m.cash = m.cash.Add(notional)
		p.Quantity = p.Quantity.Sub(o.Quantity)
		if p.Quantity.IsZero() {
			delete(m.pos, o.Symbol)
		} else {
			p.MarketVal = p.Quantity.Mul(price)
		}
	}
	bo := &BrokerOrder{
		BrokerID:    "mock-" + uuid.NewString(),
		Symbol:      o.Symbol,
		Side:        o.Side,
		Quantity:    o.Quantity,
		FilledQty:   o.Quantity,
		FilledAvg:   price,
		Status:      domain.OrderStatusFilled,
		SubmittedAt: now,
		FilledAt:    &now,
	}
	m.orders[bo.BrokerID] = bo
	return bo, nil
}

func (m *MockBroker) rejected(o *domain.Order, reason string) *BrokerOrder {
	return &BrokerOrder{
		BrokerID:    "mock-rej-" + uuid.NewString(),
		Symbol:      o.Symbol,
		Side:        o.Side,
		Quantity:    o.Quantity,
		Status:      domain.OrderStatusRejected,
		Reason:      reason,
		SubmittedAt: time.Now().UTC(),
	}
}

// GetOrder fetches by id.
func (m *MockBroker) GetOrder(_ context.Context, brokerID string) (*BrokerOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bo, ok := m.orders[brokerID]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *bo
	return &cp, nil
}

// CancelOrder is a no-op for the mock (orders fill synchronously).
func (m *MockBroker) CancelOrder(_ context.Context, _ string) error { return nil }

// Positions returns a snapshot of held positions.
func (m *MockBroker) Positions(_ context.Context) ([]BrokerPosition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]BrokerPosition, 0, len(m.pos))
	for _, p := range m.pos {
		out = append(out, *p)
	}
	return out, nil
}

// Quote delegates to the price source.
func (m *MockBroker) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	return m.prices.Quote(ctx, symbol)
}
