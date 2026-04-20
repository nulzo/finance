package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// AlpacaConfig carries the credentials and endpoints needed to talk to
// Alpaca trading + market-data APIs. Use paper or live base URL.
type AlpacaConfig struct {
	APIKey    string
	APISecret string
	BaseURL   string // e.g. https://paper-api.alpaca.markets
	DataURL   string // e.g. https://data.alpaca.markets
}

// AlpacaBroker is a thin HTTP client for Alpaca's REST API.
// It covers the subset required by the trading engine: account lookup,
// order submission, order state polling, positions and last-trade quotes.
type AlpacaBroker struct {
	cfg  AlpacaConfig
	http *http.Client
}

// NewAlpacaBroker builds a client. The returned instance is safe for
// concurrent use.
func NewAlpacaBroker(cfg AlpacaConfig) *AlpacaBroker {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://paper-api.alpaca.markets"
	}
	if cfg.DataURL == "" {
		cfg.DataURL = "https://data.alpaca.markets"
	}
	return &AlpacaBroker{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

// Name reports the broker name.
func (a *AlpacaBroker) Name() string { return "alpaca" }

func (a *AlpacaBroker) do(ctx context.Context, method, url string, body any, out any) error {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, buf)
	if err != nil {
		return err
	}
	req.Header.Set("APCA-API-KEY-ID", a.cfg.APIKey)
	req.Header.Set("APCA-API-SECRET-KEY", a.cfg.APISecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrProviderFailure, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: alpaca %d: %s", domain.ErrBrokerRejected, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Account returns the Alpaca account details normalised into our shape.
func (a *AlpacaBroker) Account(ctx context.Context) (*Account, error) {
	var raw struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		Cash        string `json:"cash"`
		Equity      string `json:"equity"`
		BuyingPower string `json:"buying_power"`
		Currency    string `json:"currency"`
	}
	if err := a.do(ctx, http.MethodGet, a.cfg.BaseURL+"/v2/account", nil, &raw); err != nil {
		return nil, err
	}
	cash, _ := decimal.NewFromString(raw.Cash)
	eq, _ := decimal.NewFromString(raw.Equity)
	bp, _ := decimal.NewFromString(raw.BuyingPower)
	return &Account{
		ID: raw.ID, Status: raw.Status, CashUSD: cash, EquityUSD: eq,
		BuyingPower: bp, Currency: raw.Currency,
	}, nil
}

type alpacaOrder struct {
	ID          string  `json:"id"`
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`
	Qty         string  `json:"qty"`
	FilledQty   string  `json:"filled_qty"`
	FilledAvg   *string `json:"filled_avg_price"`
	Status      string  `json:"status"`
	SubmittedAt *time.Time `json:"submitted_at"`
	FilledAt    *time.Time `json:"filled_at"`
}

func (o alpacaOrder) normalise() *BrokerOrder {
	qty, _ := decimal.NewFromString(o.Qty)
	fqty, _ := decimal.NewFromString(o.FilledQty)
	favg := decimal.Zero
	if o.FilledAvg != nil {
		favg, _ = decimal.NewFromString(*o.FilledAvg)
	}
	status := mapAlpacaStatus(o.Status)
	sub := time.Now().UTC()
	if o.SubmittedAt != nil {
		sub = *o.SubmittedAt
	}
	return &BrokerOrder{
		BrokerID:    o.ID,
		Symbol:      o.Symbol,
		Side:        domain.Side(strings.ToLower(o.Side)),
		Quantity:    qty,
		FilledQty:   fqty,
		FilledAvg:   favg,
		Status:      status,
		SubmittedAt: sub,
		FilledAt:    o.FilledAt,
	}
}

func mapAlpacaStatus(s string) domain.OrderStatus {
	switch strings.ToLower(s) {
	case "new", "accepted", "pending_new", "accepted_for_bidding":
		return domain.OrderStatusSubmitted
	case "filled":
		return domain.OrderStatusFilled
	case "partially_filled":
		return domain.OrderStatusPartial
	case "canceled", "pending_cancel":
		return domain.OrderStatusCancelled
	case "rejected":
		return domain.OrderStatusRejected
	case "expired", "done_for_day":
		return domain.OrderStatusExpired
	default:
		return domain.OrderStatusSubmitted
	}
}

// SubmitOrder places an order with Alpaca.
func (a *AlpacaBroker) SubmitOrder(ctx context.Context, o *domain.Order) (*BrokerOrder, error) {
	payload := map[string]any{
		"symbol":         o.Symbol,
		"qty":            o.Quantity.String(),
		"side":           string(o.Side),
		"type":           string(o.Type),
		"time_in_force":  string(o.TimeInForce),
	}
	if o.Type == domain.OrderTypeLimit && o.LimitPrice != nil {
		payload["limit_price"] = o.LimitPrice.String()
	}
	var raw alpacaOrder
	if err := a.do(ctx, http.MethodPost, a.cfg.BaseURL+"/v2/orders", payload, &raw); err != nil {
		return nil, err
	}
	return raw.normalise(), nil
}

// GetOrder fetches an order by Alpaca id.
func (a *AlpacaBroker) GetOrder(ctx context.Context, brokerID string) (*BrokerOrder, error) {
	if brokerID == "" {
		return nil, errors.New("broker id required")
	}
	var raw alpacaOrder
	if err := a.do(ctx, http.MethodGet, a.cfg.BaseURL+"/v2/orders/"+brokerID, nil, &raw); err != nil {
		return nil, err
	}
	return raw.normalise(), nil
}

// CancelOrder cancels an order by Alpaca id.
func (a *AlpacaBroker) CancelOrder(ctx context.Context, brokerID string) error {
	return a.do(ctx, http.MethodDelete, a.cfg.BaseURL+"/v2/orders/"+brokerID, nil, nil)
}

// Positions returns all open positions.
func (a *AlpacaBroker) Positions(ctx context.Context) ([]BrokerPosition, error) {
	var raw []struct {
		Symbol    string `json:"symbol"`
		Qty       string `json:"qty"`
		AvgEntry  string `json:"avg_entry_price"`
		MarketVal string `json:"market_value"`
	}
	if err := a.do(ctx, http.MethodGet, a.cfg.BaseURL+"/v2/positions", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]BrokerPosition, 0, len(raw))
	for _, p := range raw {
		qty, _ := decimal.NewFromString(p.Qty)
		avg, _ := decimal.NewFromString(p.AvgEntry)
		mv, _ := decimal.NewFromString(p.MarketVal)
		out = append(out, BrokerPosition{Symbol: p.Symbol, Quantity: qty, AvgPrice: avg, MarketVal: mv})
	}
	return out, nil
}

// Quote returns the latest trade price for a symbol.
func (a *AlpacaBroker) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	var raw struct {
		Trade struct {
			Price float64   `json:"p"`
			Time  time.Time `json:"t"`
		} `json:"trade"`
	}
	url := fmt.Sprintf("%s/v2/stocks/%s/trades/latest", a.cfg.DataURL, strings.ToUpper(symbol))
	if err := a.do(ctx, http.MethodGet, url, nil, &raw); err != nil {
		return nil, err
	}
	if raw.Trade.Price <= 0 {
		return nil, fmt.Errorf("%w: no price", domain.ErrProviderFailure)
	}
	return &domain.Quote{
		Symbol:    strings.ToUpper(symbol),
		Price:     decimal.NewFromFloat(raw.Trade.Price),
		Timestamp: raw.Trade.Time,
	}, nil
}
