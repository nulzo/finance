package market

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// Technical is a compact snapshot of the indicators the strategy layer
// and LLM prompt actually use. Any field can be zero if we had
// insufficient bars to compute it — callers must guard on the flags
// rather than treat zeros as meaningful.
type Technical struct {
	Symbol string `json:"symbol"`
	// AsOf is the close time of the most recent bar used.
	AsOf time.Time `json:"as_of"`
	// Price is the close of the most recent bar — a reliable reference
	// when callers don't have a live quote handy.
	Price decimal.Decimal `json:"price"`

	SMA20 decimal.Decimal `json:"sma20"`
	SMA50 decimal.Decimal `json:"sma50"`
	RSI14 float64         `json:"rsi14"`
	// Hi52, Lo52 are the highest High / lowest Low over the last ~252
	// trading days (1 calendar year). Less if the symbol has a shorter
	// history.
	Hi52 decimal.Decimal `json:"hi52"`
	Lo52 decimal.Decimal `json:"lo52"`
	// Chg1d, Chg5d, Chg30d are fractional returns ("0.012" == +1.2%).
	Chg1d  float64 `json:"chg_1d"`
	Chg5d  float64 `json:"chg_5d"`
	Chg30d float64 `json:"chg_30d"`

	Bars int `json:"bars"`

	// Flags indicating whether a computation was possible.
	HasSMA20 bool `json:"has_sma20"`
	HasSMA50 bool `json:"has_sma50"`
	HasRSI14 bool `json:"has_rsi14"`
	Has52wk  bool `json:"has_52wk"`
}

// TechnicalProvider is a thin facade over a BarProvider that exposes
// ComputeTechnical so the engine / strategy code doesn't have to juggle
// bars directly.
type TechnicalProvider struct {
	Bars BarProvider
	// Lookback controls how many bars we ask the underlying provider
	// for. 260 covers a full trading year, enough for SMA50 and
	// 52-week high/low math.
	Lookback int

	mu    sync.RWMutex
	cache map[string]cachedTechnical
	TTL   time.Duration
}

type cachedTechnical struct {
	snap Technical
	exp  time.Time
}

// NewTechnicalProvider builds a provider with sensible defaults. The
// snapshot cache is separate from (and shorter than) the bar cache so
// downstream callers can compute off the same bars without repeating
// the indicator math.
func NewTechnicalProvider(bars BarProvider, lookback int, ttl time.Duration) *TechnicalProvider {
	if lookback <= 0 {
		lookback = 260
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &TechnicalProvider{Bars: bars, Lookback: lookback, TTL: ttl, cache: map[string]cachedTechnical{}}
}

// Snapshot returns a Technical for a symbol, using cached indicators
// when fresh. Returns domain.ErrProviderFailure when no bars are
// available so callers can decide whether to skip the symbol or
// continue without technicals.
func (t *TechnicalProvider) Snapshot(ctx context.Context, symbol string) (*Technical, error) {
	if t == nil || t.Bars == nil {
		return nil, fmt.Errorf("%w: technical provider not configured", domain.ErrValidation)
	}
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("%w: empty symbol", domain.ErrValidation)
	}
	now := time.Now()
	t.mu.RLock()
	hit, ok := t.cache[sym]
	t.mu.RUnlock()
	if ok && now.Before(hit.exp) {
		out := hit.snap
		return &out, nil
	}
	bars, err := t.Bars.Bars(ctx, sym, t.Lookback)
	if err != nil {
		return nil, err
	}
	snap := ComputeTechnical(sym, bars)
	t.mu.Lock()
	t.cache[sym] = cachedTechnical{snap: snap, exp: now.Add(t.TTL)}
	t.mu.Unlock()
	return &snap, nil
}

// ComputeTechnical runs every indicator against a bar series. The bars
// must be in ascending chronological order; callers that already
// normalised ordering (StooqBars.Bars does) pass them straight
// through. A short series still returns a snapshot with the flags
// indicating which indicators were skipped.
func ComputeTechnical(symbol string, bars []Bar) Technical {
	snap := Technical{Symbol: strings.ToUpper(symbol), Bars: len(bars)}
	if len(bars) == 0 {
		return snap
	}
	last := bars[len(bars)-1]
	snap.AsOf = last.Time
	snap.Price = last.Close

	if v, ok := smaTailClose(bars, 20); ok {
		snap.SMA20 = v
		snap.HasSMA20 = true
	}
	if v, ok := smaTailClose(bars, 50); ok {
		snap.SMA50 = v
		snap.HasSMA50 = true
	}
	if v, ok := rsiTailClose(bars, 14); ok {
		snap.RSI14 = v
		snap.HasRSI14 = true
	}
	if hi, lo, ok := hiLoWindow(bars, 252); ok {
		snap.Hi52 = hi
		snap.Lo52 = lo
		snap.Has52wk = true
	}
	snap.Chg1d = pctChange(bars, 1)
	snap.Chg5d = pctChange(bars, 5)
	snap.Chg30d = pctChange(bars, 30)
	return snap
}

// smaTailClose returns the simple moving average of the last N closes.
// It returns (0, false) when the series is shorter than N, so callers
// can distinguish "zero because price really is zero" (shouldn't
// happen) from "not enough history yet".
func smaTailClose(bars []Bar, n int) (decimal.Decimal, bool) {
	if n <= 0 || len(bars) < n {
		return decimal.Zero, false
	}
	sum := decimal.Zero
	for _, b := range bars[len(bars)-n:] {
		sum = sum.Add(b.Close)
	}
	return sum.Div(decimal.NewFromInt(int64(n))), true
}

// rsiTailClose computes Wilder's 14-period RSI over closes ending at
// the last bar. We use Wilder smoothing (the classic definition) so
// the values match TradingView / yfinance; the simpler SMA-of-gains
// variant drifts from the standard and would trigger false signals at
// the boundaries.
func rsiTailClose(bars []Bar, period int) (float64, bool) {
	if period <= 0 || len(bars) < period+1 {
		return 0, false
	}
	// First pass: simple average over the first period to seed avgGain/avgLoss.
	var gainSum, lossSum float64
	for i := 1; i <= period; i++ {
		diff, _ := bars[i].Close.Sub(bars[i-1].Close).Float64()
		if diff > 0 {
			gainSum += diff
		} else {
			lossSum -= diff
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)
	// Subsequent bars: Wilder smoothing.
	for i := period + 1; i < len(bars); i++ {
		diff, _ := bars[i].Close.Sub(bars[i-1].Close).Float64()
		gain, loss := 0.0, 0.0
		if diff > 0 {
			gain = diff
		} else {
			loss = -diff
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50, true
		}
		return 100, true
	}
	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))
	if math.IsNaN(rsi) || math.IsInf(rsi, 0) {
		return 0, false
	}
	return rsi, true
}

// hiLoWindow returns the highest High and lowest Low over the last n
// bars (or the entire series if shorter than n, as long as we have at
// least a couple of months of history — otherwise "52wk" is
// meaningless).
func hiLoWindow(bars []Bar, n int) (decimal.Decimal, decimal.Decimal, bool) {
	if len(bars) < 40 {
		return decimal.Zero, decimal.Zero, false
	}
	start := 0
	if len(bars) > n {
		start = len(bars) - n
	}
	hi := bars[start].High
	lo := bars[start].Low
	for _, b := range bars[start+1:] {
		if b.High.GreaterThan(hi) {
			hi = b.High
		}
		if b.Low.LessThan(lo) {
			lo = b.Low
		}
	}
	return hi, lo, true
}

// pctChange returns the fractional return from n bars ago to the last
// bar ("0.012" == +1.2%). Returns 0 if there aren't n+1 bars.
func pctChange(bars []Bar, n int) float64 {
	if n <= 0 || len(bars) < n+1 {
		return 0
	}
	last := bars[len(bars)-1].Close
	prev := bars[len(bars)-1-n].Close
	if prev.IsZero() {
		return 0
	}
	diff, _ := last.Sub(prev).Div(prev).Float64()
	return diff
}
