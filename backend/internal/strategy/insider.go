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

// InsiderFollow emits signals from SEC Form 4 insider transactions.
// Insider open-market buys are a disproportionately strong positive
// signal — the filer has material non-public information and is
// personally on the hook for 10b-5 / Section 16 violations. Open-
// market insider sells carry very little signal (stock grants,
// 10b5-1 plans, diversification); we only fire on sells when the
// cluster is overwhelming.
//
// Every generated signal carries a stable RefID of the form
//
//	insider:SYMBOL:SIDE:YYYYMMDD
//
// so the Upsert is idempotent across ingest ticks.
type InsiderFollow struct {
	// Recent resolves insider trades filed on/after `since`. The
	// engine wires this to InsiderRepo.Since.
	Recent func(ctx context.Context, since time.Time) ([]domain.InsiderTrade, error)
	// LookbackDur bounds how far back a trade can be to still count.
	// Defaults to 30 days.
	LookbackDur time.Duration
	// HalfLife is the age-decay half-life applied per trade. Zero
	// disables decay. Recommended 10 days.
	HalfLife time.Duration
	// MinValueUSD drops tiny / noise filings (think: a single
	// director buying $3k of shares on their personal grant plan).
	// Defaults to $25k.
	MinValueUSD int64
	// SellClusterMin is the minimum number of distinct insider
	// sells required before we emit a sell signal. Insider sells
	// are noisy individually; a 3+ insider cluster is unusual.
	// Zero defaults to 3.
	SellClusterMin int
	// Now is injected for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Name implements Strategy.
func (p *InsiderFollow) Name() string { return "insider_follow" }

// Generate aggregates insider trades into per-(symbol, side) signals.
func (p *InsiderFollow) Generate(ctx context.Context) ([]domain.Signal, error) {
	if p.LookbackDur == 0 {
		p.LookbackDur = 30 * 24 * time.Hour
	}
	if p.MinValueUSD == 0 {
		p.MinValueUSD = 25_000
	}
	if p.SellClusterMin == 0 {
		p.SellClusterMin = 3
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
		count   int
		weight  float64
		value   int64
		names   map[string]struct{}
		titles  map[string]struct{}
	}
	buckets := map[key]*bucket{}
	for _, t := range trades {
		if t.ValueUSD < p.MinValueUSD {
			continue
		}
		if t.TransactedAt.IsZero() || t.TransactedAt.Year() < 1990 {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if !ValidTicker(sym) {
			continue
		}
		w := 1.0
		if p.HalfLife > 0 {
			age := now.Sub(t.TransactedAt)
			if age < 0 {
				age = 0
			}
			decay := math.Pow(2, -float64(age)/float64(p.HalfLife))
			if decay < 0.01 {
				decay = 0.01
			}
			w *= decay
		}
		// C-suite titles carry more signal weight than rank-and-
		// file director purchases. We boost CEO/CFO/COO filings
		// by 50% to reflect their information asymmetry.
		if hasExecTitle(t.InsiderTitle) {
			w *= 1.5
		}
		k := key{sym, t.Side}
		b := buckets[k]
		if b == nil {
			b = &bucket{names: map[string]struct{}{}, titles: map[string]struct{}{}}
			buckets[k] = b
		}
		b.count++
		b.weight += w
		b.value += t.ValueUSD
		b.names[t.InsiderName] = struct{}{}
		if t.InsiderTitle != "" {
			b.titles[t.InsiderTitle] = struct{}{}
		}
	}
	out := make([]domain.Signal, 0, len(buckets))
	day := now.UTC().Format("20060102")
	for k, b := range buckets {
		// Insider sells are mostly noise unless we see a cluster.
		if k.side == domain.SideSell && len(b.names) < p.SellClusterMin {
			continue
		}
		mag := math.Tanh(b.weight / 2)
		score := mag
		if k.side == domain.SideSell {
			score = -mag
		}
		// Confidence blends cluster size (many insiders agreeing)
		// and dollar value (personal skin in the game). Buys get
		// a small structural floor so isolated-but-real CEO buys
		// still flow through.
		conf := 0.3 + 0.15*float64(len(b.names)) + math.Tanh(float64(b.value)/2_000_000)/3
		if k.side == domain.SideBuy {
			conf += 0.1
		}
		if conf > 1 {
			conf = 1
		}
		names := make([]string, 0, len(b.names))
		for n := range b.names {
			names = append(names, n)
		}
		sort.Strings(names)
		out = append(out, domain.Signal{
			Kind:       domain.SignalKindInsider,
			Symbol:     k.symbol,
			Side:       k.side,
			Score:      round2(score),
			Confidence: round2(conf),
			Reason: fmt.Sprintf("%d insider %s(s): %s; total $%s",
				b.count, k.side, strings.Join(names, ", "), formatUSD(b.value)),
			RefID:     fmt.Sprintf("insider:%s:%s:%s", k.symbol, k.side, day),
			ExpiresAt: now.Add(14 * 24 * time.Hour),
		})
	}
	return out, nil
}

// hasExecTitle reports whether the title belongs to a named executive
// officer (CEO/CFO/COO/President/Chairman). Pattern is ASCII-
// insensitive; titles are always small enough that a substring match
// is fine.
func hasExecTitle(title string) bool {
	if title == "" {
		return false
	}
	t := strings.ToLower(title)
	hits := []string{"ceo", "cfo", "coo", "president", "chairman", "chief executive", "chief financial", "chief operating"}
	for _, h := range hits {
		if strings.Contains(t, h) {
			return true
		}
	}
	return false
}
