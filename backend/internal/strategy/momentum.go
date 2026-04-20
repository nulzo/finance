package strategy

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/providers/market"
)

// TechnicalSnapshotter is the slice of market.TechnicalProvider that
// the momentum strategy depends on. Declared here so tests can supply
// a tiny in-memory stub without importing the provider package.
type TechnicalSnapshotter interface {
	Snapshot(ctx context.Context, symbol string) (*market.Technical, error)
}

// Momentum emits signals based purely on technical structure — moving
// averages, RSI, and 52-week position. It complements the
// PoliticianFollow / NewsSentiment strategies which capture
// "information" flow; momentum captures "price" flow. A confluence of
// both is a much stronger thesis than either alone.
//
// Signal semantics:
//   - Buy: price > SMA20 > SMA50 AND RSI < 70 AND within 15% of 52w high
//     → uptrend with room to run.
//   - Buy (oversold bounce): price > SMA20 AND RSI < 30 AND within 10%
//     of 52w low → mean reversion candidate.
//   - Sell: price < SMA50 AND RSI > 70 → extended and rolling over.
//   - Sell (breakdown): price < SMA20 < SMA50 AND within 10% of 52w low
//     → broken trend, likely further downside.
//
// The strategy never fabricates signals for symbols the provider
// cannot supply technicals for; it simply skips them. RefIDs include
// the classifier name so buy/sell on the same symbol coexist without
// one over-writing the other in the Upsert.
type Momentum struct {
	// Technicals resolves {sma, rsi, hi52, lo52} for a symbol.
	Technicals TechnicalSnapshotter
	// Universe returns the symbols to evaluate this tick. The engine
	// typically passes held symbols + the union of symbols present in
	// today's PoliticianFollow / NewsSentiment signals + a static
	// watchlist so we can discover new names.
	Universe func(ctx context.Context) ([]string, error)
	// MinConfidence filters out signals below this threshold to keep
	// the decide loop focused; zero disables the filter.
	MinConfidence float64
	// Now is injected for deterministic tests; defaults to time.Now().
	Now func() time.Time
}

// Name implements Strategy.
func (m *Momentum) Name() string { return "momentum" }

// classification describes one of the fixed buy/sell patterns above.
type classification struct {
	side    domain.Side
	kind    string // uptrend | oversold | overextended | breakdown
	score   float64
	conf    float64
	reasons []string
}

// Generate walks the universe, fetches a Technical snapshot per symbol,
// and emits momentum signals. Errors on a single symbol are swallowed —
// we can't let one missing symbol kill the batch.
func (m *Momentum) Generate(ctx context.Context) ([]domain.Signal, error) {
	if m == nil || m.Technicals == nil || m.Universe == nil {
		return nil, fmt.Errorf("%w: momentum missing deps", domain.ErrValidation)
	}
	syms, err := m.Universe(ctx)
	if err != nil {
		return nil, err
	}
	nowFn := m.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	now := nowFn()
	day := now.UTC().Format("20060102")

	// Dedupe symbols (the universe may combine multiple sources) and
	// drop anything that wouldn't parse as a valid ticker. Sorting
	// keeps signal order deterministic for tests / logs.
	seen := make(map[string]struct{}, len(syms))
	clean := make([]string, 0, len(syms))
	for _, s := range syms {
		s = strings.ToUpper(strings.TrimSpace(s))
		if !ValidTicker(s) {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		clean = append(clean, s)
	}
	sort.Strings(clean)

	out := make([]domain.Signal, 0, len(clean))
	for _, sym := range clean {
		tech, err := m.Technicals.Snapshot(ctx, sym)
		if err != nil || tech == nil {
			continue
		}
		c, ok := classifyMomentum(tech)
		if !ok {
			continue
		}
		if c.conf < m.MinConfidence {
			continue
		}
		score := c.score
		if c.side == domain.SideSell {
			score = -math.Abs(score)
		} else {
			score = math.Abs(score)
		}
		reason := fmt.Sprintf("momentum %s: %s", c.kind, strings.Join(c.reasons, "; "))
		out = append(out, domain.Signal{
			Kind:       domain.SignalKindMomentum,
			Symbol:     sym,
			Side:       c.side,
			Score:      round2(score),
			Confidence: round2(c.conf),
			Reason:     reason,
			RefID:      fmt.Sprintf("momentum:%s:%s:%s:%s", sym, c.side, c.kind, day),
			ExpiresAt:  now.Add(2 * 24 * time.Hour),
		})
	}
	return out, nil
}

// classifyMomentum inspects a Technical snapshot and returns the
// strongest matching classification, or (_, false) if nothing fires.
// The order of the checks matters: the most decisive patterns
// (breakdown, overextended) run before the gentler ones so a confused
// chart doesn't ping-pong between them.
func classifyMomentum(t *market.Technical) (classification, bool) {
	if t == nil {
		return classification{}, false
	}
	// Every pattern below requires SMA20 at minimum.
	if !t.HasSMA20 || t.Price.IsZero() {
		return classification{}, false
	}
	price, _ := t.Price.Float64()
	sma20, _ := t.SMA20.Float64()
	sma50, _ := t.SMA50.Float64()

	hi, _ := t.Hi52.Float64()
	lo, _ := t.Lo52.Float64()

	// Position within 52wk range (0 = at low, 1 = at high). Only
	// meaningful when the window is populated.
	pos := 0.5
	if t.Has52wk && hi > lo {
		pos = (price - lo) / (hi - lo)
		if pos < 0 {
			pos = 0
		} else if pos > 1 {
			pos = 1
		}
	}

	// Breakdown: price < SMA20 < SMA50 AND within 10% of 52w low.
	if t.HasSMA50 && price < sma20 && sma20 < sma50 && t.Has52wk && pos < 0.10 {
		conf := clamp(0.55 + 0.25*(0.10-pos)/0.10)
		score := clamp(0.6 + 0.3*(0.10-pos)/0.10)
		if t.HasRSI14 {
			// Even weaker RSI doesn't change direction; oversold sells
			// are still directional.
			conf = clamp(conf + 0.10*(70-t.RSI14)/70)
		}
		return classification{
			side: domain.SideSell,
			kind: "breakdown",
			score: score,
			conf:  conf,
			reasons: []string{
				fmt.Sprintf("price %.2f < SMA20 %.2f < SMA50 %.2f", price, sma20, sma50),
				fmt.Sprintf("within %.1f%% of 52w low %.2f", pos*100, lo),
			},
		}, true
	}

	// Overextended: price < SMA50 AND RSI > 70. A classic rollover.
	// We require RSI14 because without it this degenerates into a
	// plain trend-follow sell that duplicates the breakdown branch.
	if t.HasSMA50 && t.HasRSI14 && price < sma50 && t.RSI14 > 70 {
		score := clamp(0.5 + (t.RSI14-70)/60) // 70 → 0.5, 100 → 1.0
		conf := clamp(0.55 + (t.RSI14-70)/100)
		return classification{
			side:  domain.SideSell,
			kind:  "overextended",
			score: score,
			conf:  conf,
			reasons: []string{
				fmt.Sprintf("RSI14=%.1f > 70 and price %.2f < SMA50 %.2f", t.RSI14, price, sma50),
			},
		}, true
	}

	// Oversold bounce: price > SMA20 AND RSI < 30 AND within 10% of low.
	if t.HasRSI14 && price > sma20 && t.RSI14 < 30 && t.Has52wk && pos < 0.15 {
		score := clamp(0.5 + (30-t.RSI14)/60)
		conf := clamp(0.5 + (30-t.RSI14)/100)
		return classification{
			side:  domain.SideBuy,
			kind:  "oversold",
			score: score,
			conf:  conf,
			reasons: []string{
				fmt.Sprintf("RSI14=%.1f < 30 reclaiming SMA20 %.2f from 52w low %.2f", t.RSI14, sma20, lo),
			},
		}, true
	}

	// Uptrend: price > SMA20 > SMA50 AND RSI < 70 AND within 15% of high.
	if t.HasSMA50 && price > sma20 && sma20 > sma50 {
		rsiOK := !t.HasRSI14 || t.RSI14 < 70
		highOK := !t.Has52wk || pos > 0.85
		if rsiOK && highOK {
			score := 0.55
			conf := 0.55
			if t.Has52wk {
				score = clamp(0.55 + 0.35*(pos-0.85)/0.15)
				conf = clamp(0.55 + 0.25*(pos-0.85)/0.15)
			}
			if t.HasRSI14 {
				// Strongest near 55-65; penalise anything approaching
				// overbought or dropping toward 50.
				dist := math.Abs(t.RSI14 - 60)
				conf = clamp(conf - dist/200)
			}
			return classification{
				side:  domain.SideBuy,
				kind:  "uptrend",
				score: score,
				conf:  conf,
				reasons: []string{
					fmt.Sprintf("price %.2f > SMA20 %.2f > SMA50 %.2f", price, sma20, sma50),
					fmt.Sprintf("%.1f%% of 52w range (hi %.2f)", pos*100, hi),
				},
			}, true
		}
	}
	return classification{}, false
}

// clamp bounds v to [0, 1].
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// MomentumUniverseFromPositions is a small helper the engine can use
// to build part of the universe: every open position + every symbol
// with an active signal from another strategy. We keep it decoupled
// from the strategy itself so the engine owns the policy.
func MomentumUniverseFromPositions(positions []domain.Position) []string {
	out := make([]string, 0, len(positions))
	for _, p := range positions {
		if p.Quantity.GreaterThan(decimal.Zero) {
			out = append(out, strings.ToUpper(strings.TrimSpace(p.Symbol)))
		}
	}
	return out
}
