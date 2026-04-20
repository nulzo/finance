package market_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/providers/market"
)

// synthBars produces a deterministic bar series for indicator tests.
// Callers control the sequence of closes; O = C (for simplicity) and
// H = C + 1, L = C - 1 so 52-wk hi/lo math exercises the High/Low
// fields rather than Close.
func synthBars(closes []float64) []market.Bar {
	t := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]market.Bar, len(closes))
	for i, c := range closes {
		d := decimal.NewFromFloat(c)
		out[i] = market.Bar{
			Time:   t.AddDate(0, 0, i),
			Open:   d,
			High:   d.Add(decimal.NewFromInt(1)),
			Low:    d.Sub(decimal.NewFromInt(1)),
			Close:  d,
			Volume: 1000,
		}
	}
	return out
}

func TestComputeTechnical_SMA(t *testing.T) {
	// Closes: 1..20 → SMA20 = avg(1..20) = 10.5.
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = float64(i + 1)
	}
	tech := market.ComputeTechnical("X", synthBars(closes))
	require.True(t, tech.HasSMA20)
	assert.Equal(t, "10.5", tech.SMA20.String())
	assert.False(t, tech.HasSMA50, "20 bars is not enough for SMA50")
}

func TestComputeTechnical_SMA50(t *testing.T) {
	closes := make([]float64, 50)
	for i := range closes {
		closes[i] = 100 // constant price → SMA50 == 100.
	}
	tech := market.ComputeTechnical("X", synthBars(closes))
	require.True(t, tech.HasSMA50)
	assert.Equal(t, "100", tech.SMA50.String())
}

func TestComputeTechnical_RSI_AllGainsSaturates(t *testing.T) {
	// Monotonically rising closes should push RSI14 to 100 (no losses).
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = float64(i + 1)
	}
	tech := market.ComputeTechnical("X", synthBars(closes))
	require.True(t, tech.HasRSI14)
	assert.Equal(t, 100.0, tech.RSI14)
}

func TestComputeTechnical_RSI_BalancedMid(t *testing.T) {
	// Alternating +1/-1 closes → equal gains/losses → RSI ≈ 50.
	closes := make([]float64, 40)
	for i := range closes {
		if i%2 == 0 {
			closes[i] = 100
		} else {
			closes[i] = 99
		}
	}
	tech := market.ComputeTechnical("X", synthBars(closes))
	require.True(t, tech.HasRSI14)
	// Allow a couple of points of wiggle; Wilder's smoothing doesn't
	// produce exactly 50 for an odd-length tail, but should be close.
	assert.InDelta(t, 50.0, tech.RSI14, 5.0)
}

func TestComputeTechnical_52wk(t *testing.T) {
	// 60 bars: 50 boring + one spike high + one dip low + tail.
	closes := make([]float64, 60)
	for i := range closes {
		closes[i] = 100
	}
	closes[20] = 150 // 52w high
	closes[30] = 50  // 52w low
	tech := market.ComputeTechnical("X", synthBars(closes))
	require.True(t, tech.Has52wk)
	assert.Equal(t, "151", tech.Hi52.String()) // High = close + 1
	assert.Equal(t, "49", tech.Lo52.String())  // Low  = close - 1
}

func TestComputeTechnical_Chg(t *testing.T) {
	// 31-bar series: 100 → 110 last, with day T-1 = 105, T-5 = 95, T-30 = 50.
	closes := make([]float64, 31)
	closes[0] = 50 // T-30
	for i := 1; i < 25; i++ {
		closes[i] = 100
	}
	closes[25] = 95 // T-5
	closes[26] = 100
	closes[27] = 100
	closes[28] = 100
	closes[29] = 105 // T-1
	closes[30] = 110 // T
	tech := market.ComputeTechnical("X", synthBars(closes))
	assert.InDelta(t, (110.0-105.0)/105.0, tech.Chg1d, 1e-6)
	assert.InDelta(t, (110.0-95.0)/95.0, tech.Chg5d, 1e-6)
	assert.InDelta(t, (110.0-50.0)/50.0, tech.Chg30d, 1e-6)
}

func TestComputeTechnical_ShortHistoryFlagsOff(t *testing.T) {
	tech := market.ComputeTechnical("X", synthBars([]float64{100, 101, 102}))
	assert.False(t, tech.HasSMA20)
	assert.False(t, tech.HasSMA50)
	assert.False(t, tech.HasRSI14)
	assert.False(t, tech.Has52wk)
	assert.Equal(t, 3, tech.Bars)
}

// Fake BarProvider for CachedBarProvider / TechnicalProvider tests.
type stubBars struct {
	bars []market.Bar
	err  error
	hits int
}

func (s *stubBars) Bars(_ context.Context, _ string, _ int) ([]market.Bar, error) {
	s.hits++
	if s.err != nil {
		return nil, s.err
	}
	return s.bars, nil
}

func TestCachedBarProvider_CachesAndFallsBack(t *testing.T) {
	primary := &stubBars{err: errors.New("primary dead")}
	fallback := &stubBars{bars: synthBars([]float64{1, 2, 3})}
	cp := market.NewCachedBarChain(time.Second, primary, fallback)

	for i := 0; i < 3; i++ {
		bars, err := cp.Bars(context.Background(), "AAPL", 10)
		require.NoError(t, err)
		require.Len(t, bars, 3)
	}
	// Primary tried once per miss; fallback served once, then cache.
	assert.Equal(t, 1, primary.hits)
	assert.Equal(t, 1, fallback.hits)
}

func TestTechnicalProvider_SnapshotCaches(t *testing.T) {
	closes := make([]float64, 60)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	sb := &stubBars{bars: synthBars(closes)}
	tp := market.NewTechnicalProvider(sb, 260, time.Minute)

	s1, err := tp.Snapshot(context.Background(), "AAPL")
	require.NoError(t, err)
	s2, err := tp.Snapshot(context.Background(), "AAPL")
	require.NoError(t, err)
	assert.True(t, s1.HasSMA20)
	assert.True(t, s2.HasSMA20)
	assert.Equal(t, 1, sb.hits, "snapshot should be cached")
}
