// Package strategy contains pluggable signal generators. Each strategy
// takes the aggregated inputs it cares about and emits domain.Signal
// values. The engine then composes the signals into a Decision.
package strategy

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nulzo/trader/internal/domain"
)

// Strategy produces signals.
type Strategy interface {
	Name() string
	Generate(ctx context.Context) ([]domain.Signal, error)
}

// PoliticianFollow emits signals based on recently disclosed
// congressional trades. Trades from high-weight politicians and larger
// dollar ranges produce higher confidence.
//
// Every generated signal carries a stable RefID of the form
//   politician:SYMBOL:SIDE:YYYYMMDD
// so the SignalRepo upsert is idempotent across ingest ticks — we
// refresh the existing row instead of stacking duplicates.
type PoliticianFollow struct {
	Recent      func(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error)
	Weight      func(politician string) float64 // 0..2; default 1.0
	LookbackDur time.Duration
	MinAmount   int64 // ignore trades smaller than this
	// HalfLife controls age-decay on the contribution each trade
	// makes to the aggregated score. A trade's weight is multiplied
	// by `2^(-age/HalfLife)` so a HalfLife of 7 days means a 7-day
	// old trade counts for 0.5, and a 14-day-old trade counts for
	// 0.25. Zero (or negative) disables decay — kept for
	// back-compat with older tests that pre-date Wave 2.5.
	HalfLife time.Duration
	// Now is injected for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Name implements Strategy.
func (p *PoliticianFollow) Name() string { return "politician_follow" }

// Generate aggregates recent disclosures into one signal per (symbol, side).
func (p *PoliticianFollow) Generate(ctx context.Context) ([]domain.Signal, error) {
	if p.LookbackDur == 0 {
		p.LookbackDur = 21 * 24 * time.Hour
	}
	nowFn := p.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	now := nowFn()
	since := now.Add(-p.LookbackDur)
	trades, err := p.Recent(ctx, since)
	if err != nil {
		return nil, err
	}
	type key struct {
		symbol string
		side   domain.Side
	}
	type bucket struct {
		count     int
		weight    float64
		amountMin int64
		amountMax int64
		names     map[string]struct{}
	}
	buckets := map[key]*bucket{}
	for _, t := range trades {
		if t.TradedAt.IsZero() || t.TradedAt.Year() < 1990 {
			// Politician-trade rows with unparseable dates leak through
			// when upstream feeds change their date format. They produce
			// confusing "traded 2000 years ago" signals; drop them.
			continue
		}
		if p.MinAmount > 0 && t.AmountMinUSD < p.MinAmount {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if !ValidTicker(sym) {
			continue
		}
		w := 1.0
		if p.Weight != nil {
			w = p.Weight(t.PoliticianName)
		}
		// Age-decay: older disclosures contribute less. Clamped at
		// ≥ 0.01 so even 60-day-old trades aren't literally zero
		// (they still count as "someone bought this last month").
		if p.HalfLife > 0 {
			age := now.Sub(t.TradedAt)
			if age < 0 {
				age = 0
			}
			decay := math.Pow(2, -float64(age)/float64(p.HalfLife))
			if decay < 0.01 {
				decay = 0.01
			}
			w *= decay
		}
		k := key{sym, t.Side}
		b := buckets[k]
		if b == nil {
			b = &bucket{names: map[string]struct{}{}}
			buckets[k] = b
		}
		b.count++
		b.weight += w
		b.amountMin += t.AmountMinUSD
		b.amountMax += t.AmountMaxUSD
		b.names[t.PoliticianName] = struct{}{}
	}
	out := make([]domain.Signal, 0, len(buckets))
	day := now.UTC().Format("20060102")
	for k, b := range buckets {
		mag := math.Tanh(b.weight / 3)
		score := mag
		if k.side == domain.SideSell {
			score = -mag
		}
		conf := math.Min(1.0, 0.3+0.15*float64(b.count)+math.Tanh(float64(b.amountMax)/1_000_000)/3)
		names := make([]string, 0, len(b.names))
		for n := range b.names {
			names = append(names, n)
		}
		sort.Strings(names)
		out = append(out, domain.Signal{
			Kind:       domain.SignalKindPolitician,
			Symbol:     k.symbol,
			Side:       k.side,
			Score:      round2(score),
			Confidence: round2(conf),
			Reason: fmt.Sprintf("%d politician %s(s): %s; total $%s-$%s",
				b.count, k.side, strings.Join(names, ", "),
				formatUSD(b.amountMin), formatUSD(b.amountMax)),
			RefID:     fmt.Sprintf("politician:%s:%s:%s", k.symbol, k.side, day),
			ExpiresAt: now.Add(7 * 24 * time.Hour),
		})
	}
	return out, nil
}

// NewsSentiment emits signals derived from news items. An LLM-scored
// sentiment combined with relevance becomes the signal strength.
//
// Every generated signal carries a stable RefID of the form
//   news:SYMBOL:SIDE:YYYYMMDDHH
// so the ingest loop refreshes the hourly rollup instead of creating
// new rows each tick. News moves faster than politician disclosures,
// so the bucket is hourly rather than daily.
type NewsSentiment struct {
	Recent func(ctx context.Context) ([]domain.NewsItem, error)
	// MinRelevance filters noise.
	MinRelevance float64
	// Now is injected for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Name implements Strategy.
func (n *NewsSentiment) Name() string { return "news_sentiment" }

// Generate collapses news items per symbol into net-sentiment signals.
func (n *NewsSentiment) Generate(ctx context.Context) ([]domain.Signal, error) {
	items, err := n.Recent(ctx)
	if err != nil {
		return nil, err
	}
	nowFn := n.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	now := nowFn()
	type agg struct {
		score     float64
		weight    float64
		count     int
		headlines []string
	}
	bySymbol := map[string]*agg{}
	for _, it := range items {
		if it.Relevance < n.MinRelevance {
			continue
		}
		for _, s := range it.SymbolList() {
			if !ValidTicker(s) {
				continue
			}
			cur := bySymbol[s]
			if cur == nil {
				cur = &agg{}
				bySymbol[s] = cur
			}
			cur.score += it.Sentiment * it.Relevance
			cur.weight += it.Relevance
			cur.count++
			if len(cur.headlines) < 3 {
				cur.headlines = append(cur.headlines, it.Title)
			}
		}
	}
	out := make([]domain.Signal, 0, len(bySymbol))
	bucket := now.UTC().Format("2006010215")
	for sym, b := range bySymbol {
		if b.weight == 0 {
			continue
		}
		avg := b.score / b.weight
		side := domain.SideBuy
		if avg < 0 {
			side = domain.SideSell
		}
		out = append(out, domain.Signal{
			Kind:       domain.SignalKindNews,
			Symbol:     sym,
			Side:       side,
			Score:      round2(avg),
			Confidence: round2(math.Min(1.0, b.weight/3)),
			Reason:     fmt.Sprintf("%d article(s): %s", b.count, strings.Join(b.headlines, " | ")),
			RefID:      fmt.Sprintf("news:%s:%s:%s", sym, side, bucket),
			ExpiresAt:  now.Add(24 * time.Hour),
		})
	}
	return out, nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// formatUSD renders a dollar integer with thousands separators.
func formatUSD(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d", v)
	// Insert thousands separators right-to-left.
	n := len(s)
	if n <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	out := make([]byte, 0, n+n/3)
	pre := n % 3
	if pre > 0 {
		out = append(out, s[:pre]...)
		if n > pre {
			out = append(out, ',')
		}
	}
	for i := pre; i < n; i += 3 {
		out = append(out, s[i:i+3]...)
		if i+3 < n {
			out = append(out, ',')
		}
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// Merged is the output of collapsing many Signals into one ranked view.
type Merged struct {
	Symbol     string
	Score      float64
	Confidence float64
	// DominantSide is whichever side (buy/sell) has the larger weighted
	// contribution. Callers that want to act on the merged signal
	// should prefer this over interpreting Score's sign directly, since
	// Score is a raw average across sides and can sit near zero when
	// bullish and bearish signals cancel.
	DominantSide domain.Side
	Rationale    []domain.Signal
}

// Merge returns a merged signal per symbol, sorted by absolute score desc.
//
// Each side's signals are aggregated separately so a strong sell
// minority isn't washed out by a majority of weak buys. DominantSide
// is whichever side carries the larger absolute weighted score. Score
// itself is the signed net (buy_weighted - sell_weighted) normalised
// by total weight — so it stays in [-1, 1] and its sign agrees with
// DominantSide.
func Merge(signals []domain.Signal) []Merged {
	type acc struct {
		buyScore, buyWeight, buyMaxConf   float64
		sellScore, sellWeight, sellMaxConf float64
		rationale                         []domain.Signal
	}
	bySym := map[string]*acc{}
	for _, s := range signals {
		a := bySym[s.Symbol]
		if a == nil {
			a = &acc{}
			bySym[s.Symbol] = a
		}
		w := math.Max(0.05, s.Confidence)
		absScore := math.Abs(s.Score)
		switch s.Side {
		case domain.SideSell:
			a.sellScore += absScore * w
			a.sellWeight += w
			if s.Confidence > a.sellMaxConf {
				a.sellMaxConf = s.Confidence
			}
		default: // buy
			a.buyScore += absScore * w
			a.buyWeight += w
			if s.Confidence > a.buyMaxConf {
				a.buyMaxConf = s.Confidence
			}
		}
		a.rationale = append(a.rationale, s)
	}
	out := make([]Merged, 0, len(bySym))
	for sym, a := range bySym {
		totalWeight := a.buyWeight + a.sellWeight
		var score float64
		if totalWeight > 0 {
			score = (a.buyScore - a.sellScore) / totalWeight
		}
		side := domain.SideBuy
		conf := a.buyMaxConf
		if a.sellScore > a.buyScore {
			side = domain.SideSell
			conf = a.sellMaxConf
		}
		out = append(out, Merged{
			Symbol:       sym,
			Score:        round2(score),
			Confidence:   round2(conf),
			DominantSide: side,
			Rationale:    a.rationale,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return math.Abs(out[i].Score) > math.Abs(out[j].Score)
	})
	return out
}

// ApplyConcentrationPenalty demotes BUY-side merged entries whose
// symbols are already heavily filled in the portfolio. The caller
// supplies `fill` as a map of `symbol → [0, 1]` where 1 means the
// position is at MaxPositionUSD. We scale both Score and Confidence
// by `max(floor, 1 - fill*1.1)` so anything at 91 %+ fill is pushed
// to zero and anything at 50 % is roughly halved. SELL-side entries
// are never penalised — a near-capped position is precisely where we
// want the easiest path to an exit signal.
//
// The function mutates the input slice in place for convenience (the
// slice is already owned by the caller from Merge) and returns the
// same slice re-sorted by the adjusted score so callers can pick
// top-N without a second sort. Unknown symbols (not in `fill`) are
// unchanged.
func ApplyConcentrationPenalty(merged []Merged, fill map[string]float64, floor float64) []Merged {
	if len(merged) == 0 || len(fill) == 0 {
		return merged
	}
	if floor < 0 {
		floor = 0
	}
	if floor > 1 {
		floor = 1
	}
	for i := range merged {
		if merged[i].DominantSide != domain.SideBuy {
			continue
		}
		frac, ok := fill[merged[i].Symbol]
		if !ok || frac <= 0 {
			continue
		}
		if frac > 1 {
			frac = 1
		}
		mult := 1 - frac*1.1
		if mult < floor {
			mult = floor
		}
		merged[i].Score = round2(merged[i].Score * mult)
		merged[i].Confidence = round2(merged[i].Confidence * mult)
	}
	sort.Slice(merged, func(i, j int) bool {
		return math.Abs(merged[i].Score) > math.Abs(merged[j].Score)
	})
	return merged
}

// SelectParams configures candidate selection for a decide tick.
// Zero fields fall back to sensible defaults so callers can leave
// anything they don't care about unset.
type SelectParams struct {
	// Merged is the full merged-signal list (caller should already
	// have applied any concentration penalty).
	Merged []Merged
	// HeldSymbols is the set of symbols with a non-zero open
	// position. Every held symbol is guaranteed inclusion so exit
	// logic fires even on weak / missing signals.
	HeldSymbols map[string]bool
	// PerKindCap limits how many symbols from each signal kind can
	// make it into the candidate pool. 0 disables per-kind quotas.
	// Recommended 5 — prevents a single strategy's leaderboard from
	// swallowing all candidate slots.
	PerKindCap int
	// DiscoverySlots reserves room for unheld symbols so new names
	// actually get a chance to enter the book. 0 disables.
	DiscoverySlots int
	// MaxCandidates is the hard cap on the returned slice; 0 = no
	// cap (returns everything that cleared the floor).
	MaxCandidates int
	// ConfidenceFloor drops non-held candidates below this floor.
	// Held symbols ignore the floor (we always want the exit path).
	ConfidenceFloor float64
}

// SelectCandidates builds the ordered candidate list the engine
// should evaluate this tick.
//
// Selection priority (higher = added first, deduped against later
// passes):
//  1. Every held symbol — so every open position gets its exit
//     evaluation even with no fresh signal. Held symbols that have
//     no merged entry this tick are synthesised as zero-score
//     placeholders so exit logic still runs.
//  2. Per-kind top-N — for each signal kind represented in the
//     rationale, take the top-PerKindCap merged symbols that carry
//     that kind with the dominant side. Prevents one strategy's
//     leaderboard from crowding out another's.
//  3. Discovery slots — DiscoverySlots top-by-score unheld symbols
//     that didn't already make it in via the per-kind pass.
//
// MaxCandidates is a hard cap across all passes. There is
// intentionally no "top-overall fill" catch-all: that would let the
// dominant signal kind re-claim the slots the per-kind cap just
// blocked. If per-kind + discovery + held don't fill MaxCandidates
// that's fine — the engine won't waste LLM calls on lukewarm signals.
//
// Sell-side merged entries on unowned symbols are filtered out up
// front: they would always HOLD (no position to sell) and burning an
// LLM call on them is a guaranteed waste.
func SelectCandidates(p SelectParams) []Merged {
	held := p.HeldSymbols
	if held == nil {
		held = map[string]bool{}
	}
	// 1. Drop sell-on-unowned up front so they don't consume any quota.
	filtered := make([]Merged, 0, len(p.Merged))
	for _, m := range p.Merged {
		if m.DominantSide == domain.SideSell && !held[m.Symbol] {
			continue
		}
		filtered = append(filtered, m)
	}
	// Index for fast lookup by symbol.
	bySym := make(map[string]int, len(filtered))
	for i := range filtered {
		bySym[filtered[i].Symbol] = i
	}

	seen := make(map[string]bool, len(filtered)+len(held))
	out := make([]Merged, 0, len(filtered)+len(held))
	add := func(m Merged) bool {
		if seen[m.Symbol] {
			return false
		}
		if p.MaxCandidates > 0 && len(out) >= p.MaxCandidates {
			return false
		}
		seen[m.Symbol] = true
		out = append(out, m)
		return true
	}
	// 2. Held symbols always in. If a symbol is held but has no
	// signal this tick, synthesise a zero-score placeholder so the
	// engine still walks its exit path. Iteration order over a Go
	// map is randomised; sort held keys so the output is stable for
	// tests / logs.
	heldKeys := make([]string, 0, len(held))
	for sym := range held {
		heldKeys = append(heldKeys, sym)
	}
	sort.Strings(heldKeys)
	for _, sym := range heldKeys {
		if idx, ok := bySym[sym]; ok {
			add(filtered[idx])
			continue
		}
		add(Merged{Symbol: sym})
	}
	// 3. Per-kind quotas. Group filtered by kind (via rationale)
	// and take top-PerKindCap by abs score per kind.
	if p.PerKindCap > 0 {
		byKind := make(map[domain.SignalKind][]int)
		for i, m := range filtered {
			kinds := map[domain.SignalKind]bool{}
			for _, r := range m.Rationale {
				if r.Side != m.DominantSide {
					continue
				}
				kinds[r.Kind] = true
			}
			for k := range kinds {
				byKind[k] = append(byKind[k], i)
			}
		}
		// Iterate in kind-name order so selection is deterministic.
		kinds := make([]domain.SignalKind, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
		for _, k := range kinds {
			idxs := byKind[k]
			sort.Slice(idxs, func(i, j int) bool {
				return math.Abs(filtered[idxs[i]].Score) > math.Abs(filtered[idxs[j]].Score)
			})
			picked := 0
			for _, idx := range idxs {
				m := filtered[idx]
				if !held[m.Symbol] && m.Confidence < p.ConfidenceFloor {
					continue
				}
				if math.Abs(m.Score) == 0 {
					continue
				}
				if add(m) {
					picked++
				}
				if picked >= p.PerKindCap {
					break
				}
			}
		}
	}
	// 4. Discovery — top-by-score unheld symbols that weren't picked
	// by per-kind quotas. Makes sure fresh names earn evaluation
	// slots even when every kind's pool is dominated by held names.
	if p.DiscoverySlots > 0 {
		picked := 0
		for _, m := range filtered {
			if picked >= p.DiscoverySlots {
				break
			}
			if held[m.Symbol] {
				continue
			}
			if m.Confidence < p.ConfidenceFloor {
				continue
			}
			if math.Abs(m.Score) == 0 {
				continue
			}
			if add(m) {
				picked++
			}
		}
	}
	return out
}
