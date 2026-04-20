package strategy_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/providers/market"
	"github.com/nulzo/trader/internal/strategy"
)

type stubTechnicals map[string]*market.Technical

func (s stubTechnicals) Snapshot(_ context.Context, symbol string) (*market.Technical, error) {
	if v, ok := s[symbol]; ok {
		return v, nil
	}
	return nil, assertMissing{sym: symbol}
}

type assertMissing struct{ sym string }

func (a assertMissing) Error() string { return "no technical for " + a.sym }

func d(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// uptrendTech is a happy, trending stock: price above both SMAs,
// near the top of the 52wk range, RSI in the mid-60s.
func uptrendTech() *market.Technical {
	return &market.Technical{
		Symbol:   "UP",
		Price:    d(110),
		SMA20:    d(105),
		SMA50:    d(100),
		Hi52:     d(115),
		Lo52:     d(60),
		RSI14:    60,
		HasSMA20: true, HasSMA50: true, HasRSI14: true, Has52wk: true,
	}
}

// breakdownTech: price < SMA20 < SMA50 and within 10% of 52w low.
func breakdownTech() *market.Technical {
	return &market.Technical{
		Symbol:   "DOWN",
		Price:    d(51),
		SMA20:    d(55),
		SMA50:    d(70),
		Hi52:     d(150),
		Lo52:     d(50),
		RSI14:    28,
		HasSMA20: true, HasSMA50: true, HasRSI14: true, Has52wk: true,
	}
}

// overextendedTech: price < SMA50, but RSI is very high (late-stage
// squeeze fading).
func overextendedTech() *market.Technical {
	return &market.Technical{
		Symbol:   "HOT",
		Price:    d(95),
		SMA20:    d(96),
		SMA50:    d(100),
		Hi52:     d(110),
		Lo52:     d(70),
		RSI14:    82,
		HasSMA20: true, HasSMA50: true, HasRSI14: true, Has52wk: true,
	}
}

// oversoldTech: price > SMA20 (trying to reclaim), RSI < 30, near lows.
func oversoldTech() *market.Technical {
	return &market.Technical{
		Symbol:   "DIP",
		Price:    d(52),
		SMA20:    d(51),
		SMA50:    d(80),
		Hi52:     d(150),
		Lo52:     d(48),
		RSI14:    22,
		HasSMA20: true, HasSMA50: true, HasRSI14: true, Has52wk: true,
	}
}

// flatTech should fire nothing: price ≈ SMA20 ≈ SMA50, middle of range, RSI 50.
func flatTech() *market.Technical {
	return &market.Technical{
		Symbol:   "MEH",
		Price:    d(100),
		SMA20:    d(100),
		SMA50:    d(100),
		Hi52:     d(120),
		Lo52:     d(80),
		RSI14:    50,
		HasSMA20: true, HasSMA50: true, HasRSI14: true, Has52wk: true,
	}
}

func TestMomentum_EmitsOneSignalPerSymbol(t *testing.T) {
	stub := stubTechnicals{
		"UP":   uptrendTech(),
		"DOWN": breakdownTech(),
		"HOT":  overextendedTech(),
		"DIP":  oversoldTech(),
		"MEH":  flatTech(),
	}
	m := &strategy.Momentum{
		Technicals: stub,
		Universe: func(_ context.Context) ([]string, error) {
			return []string{"UP", "DOWN", "HOT", "DIP", "MEH"}, nil
		},
		Now: func() time.Time { return time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) },
	}
	sigs, err := m.Generate(context.Background())
	require.NoError(t, err)

	bySym := map[string]domain.Signal{}
	for _, s := range sigs {
		bySym[s.Symbol] = s
	}
	require.Contains(t, bySym, "UP")
	require.Contains(t, bySym, "DOWN")
	require.Contains(t, bySym, "HOT")
	require.Contains(t, bySym, "DIP")
	require.NotContains(t, bySym, "MEH", "flat charts should not emit signals")

	assert.Equal(t, domain.SideBuy, bySym["UP"].Side)
	assert.Equal(t, domain.SideSell, bySym["DOWN"].Side)
	assert.Equal(t, domain.SideSell, bySym["HOT"].Side)
	assert.Equal(t, domain.SideBuy, bySym["DIP"].Side)

	for _, s := range sigs {
		assert.Equal(t, domain.SignalKindMomentum, s.Kind)
		assert.NotEmpty(t, s.Reason)
		assert.NotEmpty(t, s.RefID, "refid required for idempotent upsert")
	}
}

func TestMomentum_IdempotentRefIDs(t *testing.T) {
	stub := stubTechnicals{"UP": uptrendTech()}
	m := &strategy.Momentum{
		Technicals: stub,
		Universe:   func(_ context.Context) ([]string, error) { return []string{"UP"}, nil },
		Now:        func() time.Time { return time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) },
	}
	a, err := m.Generate(context.Background())
	require.NoError(t, err)
	b, err := m.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.Equal(t, a[0].RefID, b[0].RefID, "same day/side/kind must produce the same refid")
}

func TestMomentum_MinConfidenceFilters(t *testing.T) {
	stub := stubTechnicals{"UP": uptrendTech()}
	m := &strategy.Momentum{
		Technicals:    stub,
		Universe:      func(_ context.Context) ([]string, error) { return []string{"UP"}, nil },
		MinConfidence: 0.99, // absurdly high: nothing should pass
	}
	sigs, err := m.Generate(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestMomentum_SkipsMissingSymbols(t *testing.T) {
	stub := stubTechnicals{} // empty — every Snapshot call errors
	m := &strategy.Momentum{
		Technicals: stub,
		Universe:   func(_ context.Context) ([]string, error) { return []string{"GHOST"}, nil },
	}
	sigs, err := m.Generate(context.Background())
	require.NoError(t, err, "missing technicals must never fail the whole batch")
	assert.Empty(t, sigs)
}

func TestMomentum_DedupesUniverse(t *testing.T) {
	stub := stubTechnicals{"UP": uptrendTech()}
	m := &strategy.Momentum{
		Technicals: stub,
		Universe:   func(_ context.Context) ([]string, error) { return []string{"UP", "up", "UP"}, nil },
	}
	sigs, err := m.Generate(context.Background())
	require.NoError(t, err)
	assert.Len(t, sigs, 1)
}

func TestMomentum_RejectsInvalidTicker(t *testing.T) {
	stub := stubTechnicals{}
	m := &strategy.Momentum{
		Technicals: stub,
		Universe:   func(_ context.Context) ([]string, error) { return []string{"-notaticker-", ""}, nil },
	}
	sigs, err := m.Generate(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestMomentumUniverseFromPositions(t *testing.T) {
	in := []domain.Position{
		{Symbol: "AAPL", Quantity: d(5)},
		{Symbol: "MSFT", Quantity: d(0)},
		{Symbol: "NVDA", Quantity: d(1)},
	}
	out := strategy.MomentumUniverseFromPositions(in)
	assert.ElementsMatch(t, []string{"AAPL", "NVDA"}, out)
}
