package strategy_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/strategy"
)

// mkBuy / mkSell build Merged fixtures with the minimum fields the
// selector looks at: Symbol, Score, Confidence, DominantSide, and
// a Rationale list whose Kind drives per-kind bucketing.
func mkMerged(sym string, side domain.Side, score, conf float64, kinds ...domain.SignalKind) strategy.Merged {
	m := strategy.Merged{
		Symbol:       sym,
		Score:        score,
		Confidence:   conf,
		DominantSide: side,
	}
	for _, k := range kinds {
		m.Rationale = append(m.Rationale, domain.Signal{
			Kind: k, Symbol: sym, Side: side, Score: score, Confidence: conf,
		})
	}
	return m
}

func TestApplyConcentrationPenalty_BuysDemotedSellsUntouched(t *testing.T) {
	// A heavily-filled BUY should lose score/confidence; a SELL on
	// the same fill fraction should be untouched; an unheld symbol
	// should be untouched regardless of side.
	merged := []strategy.Merged{
		mkMerged("FULL", domain.SideBuy, 0.9, 0.9, domain.SignalKindPolitician),
		mkMerged("HALF", domain.SideBuy, 0.8, 0.9, domain.SignalKindPolitician),
		mkMerged("SELL", domain.SideSell, -0.8, 0.9, domain.SignalKindPolitician),
		mkMerged("NEW", domain.SideBuy, 0.5, 0.6, domain.SignalKindNews),
	}
	out := strategy.ApplyConcentrationPenalty(merged, map[string]float64{
		"FULL": 1.0, // 1 - 1.0*1.1 = -0.1 → clamped to 0
		"HALF": 0.5, // 1 - 0.5*1.1 = 0.45
		"SELL": 0.9, // sell: untouched
	}, 0)

	// Find each row by symbol (order may change after re-sort).
	bySym := map[string]strategy.Merged{}
	for _, m := range out {
		bySym[m.Symbol] = m
	}
	assert.InDelta(t, 0.0, bySym["FULL"].Score, 0.001, "fully filled buy zeroed")
	assert.InDelta(t, 0.0, bySym["FULL"].Confidence, 0.001)
	assert.InDelta(t, 0.36, bySym["HALF"].Score, 0.01, "half-filled buy ~0.45 * 0.8")
	assert.InDelta(t, -0.8, bySym["SELL"].Score, 0.001, "sell unaffected")
	assert.InDelta(t, 0.5, bySym["NEW"].Score, 0.001, "unheld buy unaffected")

	// The NEW buy should now outrank FULL by abs score.
	newIdx, fullIdx := -1, -1
	for i, m := range out {
		switch m.Symbol {
		case "NEW":
			newIdx = i
		case "FULL":
			fullIdx = i
		}
	}
	assert.Less(t, newIdx, fullIdx, "unheld buy must outrank near-capped buy after penalty")
}

func TestApplyConcentrationPenalty_RespectsFloor(t *testing.T) {
	merged := []strategy.Merged{mkMerged("FULL", domain.SideBuy, 1.0, 1.0, domain.SignalKindPolitician)}
	out := strategy.ApplyConcentrationPenalty(merged, map[string]float64{"FULL": 2.0}, 0.2)
	// 1 - 2*1.1 = -1.2 → clamped to floor 0.2.
	assert.InDelta(t, 0.2, out[0].Score, 0.001)
	assert.InDelta(t, 0.2, out[0].Confidence, 0.001)
}

func TestSelectCandidates_PerKindQuotasPreventMonopoly(t *testing.T) {
	// 6 politician buys, 1 news buy, 1 momentum buy. With top-N=3
	// overall the politicians would take every slot; per-kind cap
	// of 2 leaves room for the news + momentum symbols.
	merged := []strategy.Merged{
		mkMerged("P1", domain.SideBuy, 0.95, 1.0, domain.SignalKindPolitician),
		mkMerged("P2", domain.SideBuy, 0.90, 1.0, domain.SignalKindPolitician),
		mkMerged("P3", domain.SideBuy, 0.85, 1.0, domain.SignalKindPolitician),
		mkMerged("P4", domain.SideBuy, 0.80, 1.0, domain.SignalKindPolitician),
		mkMerged("P5", domain.SideBuy, 0.75, 1.0, domain.SignalKindPolitician),
		mkMerged("P6", domain.SideBuy, 0.70, 1.0, domain.SignalKindPolitician),
		mkMerged("N1", domain.SideBuy, 0.60, 0.9, domain.SignalKindNews),
		mkMerged("M1", domain.SideBuy, 0.55, 0.9, domain.SignalKindMomentum),
	}
	out := strategy.SelectCandidates(strategy.SelectParams{
		Merged:          merged,
		PerKindCap:      2,
		DiscoverySlots:  0,
		MaxCandidates:   10,
		ConfidenceFloor: 0.3,
	})
	symbols := map[string]bool{}
	for _, c := range out {
		symbols[c.Symbol] = true
	}
	assert.True(t, symbols["P1"] && symbols["P2"], "top-2 politician must appear")
	assert.False(t, symbols["P3"] || symbols["P4"] || symbols["P5"] || symbols["P6"],
		"per-kind cap (2) must exclude politician symbols 3..6 from the per-kind pass")
	assert.True(t, symbols["N1"], "news survives because politician can't monopolise")
	assert.True(t, symbols["M1"], "momentum survives because politician can't monopolise")
}

func TestSelectCandidates_DiscoverySlotsReserveUnheld(t *testing.T) {
	// Per-kind cap 0, discovery slots 2. Expect: held symbol (from
	// the held set) + 2 highest-scoring unheld buys as discovery.
	merged := []strategy.Merged{
		mkMerged("HELD", domain.SideBuy, 0.4, 0.5, domain.SignalKindPolitician),
		mkMerged("NEW1", domain.SideBuy, 0.9, 0.9, domain.SignalKindPolitician),
		mkMerged("NEW2", domain.SideBuy, 0.8, 0.9, domain.SignalKindPolitician),
		mkMerged("NEW3", domain.SideBuy, 0.7, 0.9, domain.SignalKindPolitician),
	}
	out := strategy.SelectCandidates(strategy.SelectParams{
		Merged:          merged,
		HeldSymbols:     map[string]bool{"HELD": true},
		PerKindCap:      0,
		DiscoverySlots:  2,
		MaxCandidates:   10,
		ConfidenceFloor: 0.3,
	})
	syms := map[string]bool{}
	for _, c := range out {
		syms[c.Symbol] = true
	}
	assert.True(t, syms["HELD"], "held always included")
	assert.True(t, syms["NEW1"], "top unheld gets discovery slot")
	assert.True(t, syms["NEW2"], "second-best unheld gets discovery slot")
	assert.False(t, syms["NEW3"], "discovery slots exhausted after 2")
}

func TestSelectCandidates_DropsSellOnUnowned(t *testing.T) {
	// SELL on an unheld symbol is a guaranteed HOLD — burning an
	// LLM call on it is pure waste. Selector must drop them.
	merged := []strategy.Merged{
		mkMerged("SELLNOTHELD", domain.SideSell, -0.9, 0.9, domain.SignalKindPolitician),
		mkMerged("HELD", domain.SideSell, -0.9, 0.9, domain.SignalKindPolitician),
		mkMerged("BUY", domain.SideBuy, 0.5, 0.5, domain.SignalKindPolitician),
	}
	out := strategy.SelectCandidates(strategy.SelectParams{
		Merged:          merged,
		HeldSymbols:     map[string]bool{"HELD": true},
		PerKindCap:      5,
		DiscoverySlots:  5,
		MaxCandidates:   10,
		ConfidenceFloor: 0.3,
	})
	syms := map[string]bool{}
	for _, c := range out {
		syms[c.Symbol] = true
	}
	assert.False(t, syms["SELLNOTHELD"], "sell on unheld symbol must be filtered")
	assert.True(t, syms["HELD"], "sell on held symbol must survive")
	assert.True(t, syms["BUY"], "buy on unheld survives normally")
}

func TestSelectCandidates_IncludesHeldWithoutSignals(t *testing.T) {
	// A symbol we hold with no active merged signal this tick must
	// still be returned as a candidate so the exit path runs.
	out := strategy.SelectCandidates(strategy.SelectParams{
		Merged:         nil,
		HeldSymbols:    map[string]bool{"HELD": true},
		PerKindCap:     5,
		DiscoverySlots: 5,
		MaxCandidates:  10,
	})
	require.Len(t, out, 1)
	assert.Equal(t, "HELD", out[0].Symbol)
	assert.InDelta(t, 0.0, out[0].Score, 0.001, "synthesised held candidate has zero score")
}

func TestSelectCandidates_ConfidenceFloorExcludesWeakNonHeld(t *testing.T) {
	merged := []strategy.Merged{
		mkMerged("WEAK", domain.SideBuy, 0.5, 0.1, domain.SignalKindPolitician),
		mkMerged("STRONG", domain.SideBuy, 0.5, 0.9, domain.SignalKindPolitician),
	}
	out := strategy.SelectCandidates(strategy.SelectParams{
		Merged:          merged,
		PerKindCap:      5,
		DiscoverySlots:  5,
		MaxCandidates:   10,
		ConfidenceFloor: 0.35,
	})
	syms := map[string]bool{}
	for _, c := range out {
		syms[c.Symbol] = true
	}
	assert.False(t, syms["WEAK"], "non-held below floor must be filtered")
	assert.True(t, syms["STRONG"], "non-held above floor survives")
}

func TestSelectCandidates_MaxCandidatesIsHardCap(t *testing.T) {
	merged := []strategy.Merged{
		mkMerged("A", domain.SideBuy, 0.9, 0.9, domain.SignalKindPolitician),
		mkMerged("B", domain.SideBuy, 0.8, 0.9, domain.SignalKindNews),
		mkMerged("C", domain.SideBuy, 0.7, 0.9, domain.SignalKindMomentum),
	}
	out := strategy.SelectCandidates(strategy.SelectParams{
		Merged:          merged,
		PerKindCap:      5,
		DiscoverySlots:  5,
		MaxCandidates:   2,
		ConfidenceFloor: 0.3,
	})
	assert.Len(t, out, 2, "MaxCandidates is a hard cap across all passes")
}

func TestSelectCandidates_Deterministic(t *testing.T) {
	// Same input twice must produce identical output order;
	// downstream LLM cost tracking and test assertions both depend
	// on stable ordering.
	base := []strategy.Merged{
		mkMerged("B", domain.SideBuy, 0.8, 0.9, domain.SignalKindNews),
		mkMerged("A", domain.SideBuy, 0.9, 0.9, domain.SignalKindPolitician),
		mkMerged("C", domain.SideBuy, 0.7, 0.9, domain.SignalKindMomentum),
	}
	params := strategy.SelectParams{
		Merged: base, PerKindCap: 5, DiscoverySlots: 5, MaxCandidates: 10,
	}
	a := strategy.SelectCandidates(params)
	b := strategy.SelectCandidates(params)
	require.Equal(t, len(a), len(b))
	for i := range a {
		assert.Equal(t, a[i].Symbol, b[i].Symbol)
	}
}

func TestPoliticianFollow_AgeDecay(t *testing.T) {
	fixed := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	// Same politician, same amount, two BUYs on different symbols:
	// FRESH today, STALE 14 days ago. With a 7-day half-life the
	// stale trade contributes 0.25, the fresh 1.0 → FRESH score
	// must be strictly higher than STALE.
	trades := []domain.PoliticianTrade{
		{PoliticianName: "A", Chamber: "house", Symbol: "FRESH", Side: domain.SideBuy,
			AmountMinUSD: 10_000, AmountMaxUSD: 50_000, TradedAt: fixed},
		{PoliticianName: "A", Chamber: "house", Symbol: "STALE", Side: domain.SideBuy,
			AmountMinUSD: 10_000, AmountMaxUSD: 50_000, TradedAt: fixed.Add(-14 * 24 * time.Hour)},
	}
	s := &strategy.PoliticianFollow{
		Recent:      func(_ context.Context, _ time.Time) ([]domain.PoliticianTrade, error) { return trades, nil },
		LookbackDur: 30 * 24 * time.Hour,
		HalfLife:    7 * 24 * time.Hour,
		Now:         func() time.Time { return fixed },
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 2)
	var fresh, stale float64
	for _, sg := range sigs {
		switch sg.Symbol {
		case "FRESH":
			fresh = sg.Score
		case "STALE":
			stale = sg.Score
		}
	}
	assert.Greater(t, fresh, stale, "fresh politician trade must outrank stale one under decay")
	// And with decay disabled (HalfLife=0) both must score identically.
	s.HalfLife = 0
	sigs2, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs2, 2)
	var f2, s2 float64
	for _, sg := range sigs2 {
		switch sg.Symbol {
		case "FRESH":
			f2 = sg.Score
		case "STALE":
			s2 = sg.Score
		}
	}
	assert.InDelta(t, f2, s2, 1e-9, "without decay FRESH and STALE must score equally")
	// Sanity: decay really did lower STALE by much more than FRESH,
	// not just a rounding-precision wobble.
	assert.Greater(t, math.Abs(fresh-stale), 0.05,
		"the score gap with decay should be meaningful, got %.4f vs %.4f", fresh, stale)
}
