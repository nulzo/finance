// Package risk enforces pre-trade limits. Any trade attempt must pass
// Approve before being submitted to the broker.
package risk

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// Limits defines risk parameters applied to every trade.
type Limits struct {
	MaxOrderUSD        decimal.Decimal // cap per order notional
	MaxPositionUSD     decimal.Decimal // cap per-symbol exposure (cost basis)
	MaxDailyLossUSD    decimal.Decimal // stop trading if realized P&L drops below -X
	MaxDailyOrders     int             // cap order count per UTC day
	MaxSymbolExposure  decimal.Decimal // cap fraction of portfolio equity into one symbol (0..1)
	// MaxConcurrentPositions, if > 0, blocks buys that would open a
	// new symbol while the book already has this many open
	// positions. Re-buys on already-held symbols and all sells are
	// unaffected — the cap only limits the width of the portfolio.
	MaxConcurrentPositions int
	Blacklist          []string        // symbols that may never be traded
	RequireApproval    bool            // if true, trades must be manually approved
}

// Request is everything the engine needs to evaluate a prospective trade.
type Request struct {
	Symbol            string
	Side              domain.Side
	TargetUSD         decimal.Decimal
	Price             decimal.Decimal
	PortfolioCash     decimal.Decimal
	PortfolioEquity   decimal.Decimal
	PositionQty       decimal.Decimal
	PositionAvgCost   decimal.Decimal
	OrdersToday       int
	RealizedPnLToday  decimal.Decimal
	// OpenPositions is the total number of symbols with a non-zero
	// position in the portfolio at evaluation time. Used by the
	// MaxConcurrentPositions check to decide whether a buy on a new
	// (unheld) symbol would widen the book past the cap.
	OpenPositions int
}

// Result captures the engine's decision plus a downscaled quantity.
type Result struct {
	Approved bool
	Reason   string
	Quantity decimal.Decimal // approved quantity (may be less than requested)
	Notional decimal.Decimal
}

// Engine evaluates risk.
//
// Limits are protected by a RWMutex so the HTTP control API
// (PATCH /v1/risk/limits) can mutate them live without racing the
// decide loop. Callers should use `GetLimits` / `UpdateLimits` / the
// `Blacklist*` helpers rather than reaching into the struct field
// directly — direct access is preserved for backward compatibility
// with older tests but is not safe under concurrent mutation.
type Engine struct {
	Limits Limits
	Now    func() time.Time

	mu sync.RWMutex
}

// NewEngine constructs a risk engine. The Now function defaults to time.Now.
func NewEngine(l Limits) *Engine {
	return &Engine{Limits: l, Now: time.Now}
}

// GetLimits returns a safe, point-in-time copy of the current limits.
// Mutating the returned struct does NOT mutate the engine.
func (e *Engine) GetLimits() Limits {
	e.mu.RLock()
	defer e.mu.RUnlock()
	l := e.Limits
	if len(e.Limits.Blacklist) > 0 {
		l.Blacklist = append([]string(nil), e.Limits.Blacklist...)
	}
	return l
}

// UpdateLimits replaces the full limits struct atomically. Intended
// for the control API — callers who only want to edit a single field
// should call GetLimits first, mutate the copy, and pass it back.
func (e *Engine) UpdateLimits(l Limits) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Limits = l
}

// AddBlacklist idempotently adds a symbol to the blacklist.
func (e *Engine) AddBlacklist(sym string) {
	sym = strings.ToUpper(strings.TrimSpace(sym))
	if sym == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range e.Limits.Blacklist {
		if strings.EqualFold(b, sym) {
			return
		}
	}
	e.Limits.Blacklist = append(e.Limits.Blacklist, sym)
}

// RemoveBlacklist removes a symbol from the blacklist, if present.
func (e *Engine) RemoveBlacklist(sym string) {
	sym = strings.ToUpper(strings.TrimSpace(sym))
	if sym == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.Limits.Blacklist[:0]
	for _, b := range e.Limits.Blacklist {
		if !strings.EqualFold(b, sym) {
			out = append(out, b)
		}
	}
	e.Limits.Blacklist = out
}

// Approve inspects the request and decides whether to allow the trade.
// The approved quantity is downscaled to fit any applicable limit.
func (e *Engine) Approve(_ context.Context, r Request) Result {
	// Snapshot limits under the read lock so a concurrent
	// UpdateLimits can't mutate the slice/decimals mid-evaluation.
	limits := e.GetLimits()

	sym := strings.ToUpper(r.Symbol)
	for _, b := range limits.Blacklist {
		if strings.EqualFold(b, sym) {
			return Result{Approved: false, Reason: "symbol blacklisted"}
		}
	}
	if r.Price.IsZero() || r.Price.IsNegative() {
		return Result{Approved: false, Reason: "invalid price"}
	}
	if !r.Side.Valid() {
		return Result{Approved: false, Reason: "invalid side"}
	}
	if r.TargetUSD.IsNegative() || r.TargetUSD.IsZero() {
		return Result{Approved: false, Reason: "non-positive target"}
	}
	if limits.MaxDailyOrders > 0 && r.OrdersToday >= limits.MaxDailyOrders {
		return Result{Approved: false, Reason: "daily order cap reached"}
	}
	if !limits.MaxDailyLossUSD.IsZero() && r.RealizedPnLToday.LessThan(limits.MaxDailyLossUSD.Neg()) {
		return Result{Approved: false, Reason: "daily loss limit hit"}
	}

	notional := r.TargetUSD
	// Cap to per-order limit.
	if !limits.MaxOrderUSD.IsZero() && notional.GreaterThan(limits.MaxOrderUSD) {
		notional = limits.MaxOrderUSD
	}

	switch r.Side {
	case domain.SideBuy:
		// Concurrent-position cap: only applies when opening a new
		// symbol. Re-buys on existing positions are allowed to
		// grow regardless. Sells are never cap-limited — we always
		// allow closing a position.
		if limits.MaxConcurrentPositions > 0 &&
			r.PositionQty.IsZero() &&
			r.OpenPositions >= limits.MaxConcurrentPositions {
			return Result{Approved: false, Reason: "max concurrent positions reached"}
		}
		// Cash must cover notional.
		if notional.GreaterThan(r.PortfolioCash) {
			notional = r.PortfolioCash
		}
		// Per-symbol exposure cap.
		if !limits.MaxPositionUSD.IsZero() {
			currentCost := r.PositionQty.Mul(r.PositionAvgCost)
			headroom := limits.MaxPositionUSD.Sub(currentCost)
			if headroom.LessThanOrEqual(decimal.Zero) {
				return Result{Approved: false, Reason: "position at max"}
			}
			if notional.GreaterThan(headroom) {
				notional = headroom
			}
		}
		// Fractional exposure cap.
		if limits.MaxSymbolExposure.GreaterThan(decimal.Zero) && r.PortfolioEquity.GreaterThan(decimal.Zero) {
			maxNotional := r.PortfolioEquity.Mul(limits.MaxSymbolExposure)
			existing := r.PositionQty.Mul(r.Price)
			remain := maxNotional.Sub(existing)
			if remain.LessThan(notional) {
				if remain.LessThanOrEqual(decimal.Zero) {
					return Result{Approved: false, Reason: "symbol exposure cap"}
				}
				notional = remain
			}
		}
	case domain.SideSell:
		// Cannot sell more than position.
		positionNotional := r.PositionQty.Mul(r.Price)
		if positionNotional.LessThanOrEqual(decimal.Zero) {
			return Result{Approved: false, Reason: "no position to sell"}
		}
		if notional.GreaterThan(positionNotional) {
			notional = positionNotional
		}
	}

	if notional.IsZero() || notional.IsNegative() {
		return Result{Approved: false, Reason: "no headroom"}
	}

	qty := notional.Div(r.Price)
	// Round down to 4 decimal places (fractional shares) and discard tiny tails.
	qty = qty.Round(4)
	if qty.LessThanOrEqual(decimal.Zero) {
		return Result{Approved: false, Reason: "quantity rounds to zero"}
	}
	// Minimum notional guard: $1
	if qty.Mul(r.Price).LessThan(decimal.NewFromInt(1)) {
		return Result{Approved: false, Reason: "below minimum notional"}
	}
	if limits.RequireApproval {
		return Result{Approved: false, Reason: "manual approval required", Quantity: qty, Notional: qty.Mul(r.Price)}
	}
	return Result{Approved: true, Reason: fmt.Sprintf("ok: sized to $%s", qty.Mul(r.Price).StringFixed(2)), Quantity: qty, Notional: qty.Mul(r.Price)}
}
