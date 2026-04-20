package strategy_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/strategy"
)

func TestPoliticianFollow_BuysScorePositive(t *testing.T) {
	trades := []domain.PoliticianTrade{
		{PoliticianName: "A", Chamber: "house", Symbol: "NVDA", Side: domain.SideBuy, AmountMinUSD: 100_000, AmountMaxUSD: 250_000, TradedAt: time.Now()},
		{PoliticianName: "B", Chamber: "senate", Symbol: "NVDA", Side: domain.SideBuy, AmountMinUSD: 50_000, AmountMaxUSD: 100_000, TradedAt: time.Now()},
	}
	s := &strategy.PoliticianFollow{
		Recent: func(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) {
			return trades, nil
		},
		LookbackDur: 30 * 24 * time.Hour,
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "NVDA", sigs[0].Symbol)
	assert.Equal(t, domain.SideBuy, sigs[0].Side)
	assert.Greater(t, sigs[0].Score, 0.0)
	assert.Greater(t, sigs[0].Confidence, 0.0)
}

// Every signal must carry a stable RefID so SignalRepo.Upsert can
// dedupe subsequent ingest ticks. Without this the signals table grew
// unbounded in production (4,670 rows after one day from ~15 ingest
// ticks, all with empty ref_id).
func TestPoliticianFollow_EmitsStableRefID(t *testing.T) {
	fixed := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	trades := []domain.PoliticianTrade{
		{PoliticianName: "A", Symbol: "AAPL", Side: domain.SideBuy, AmountMinUSD: 1001, AmountMaxUSD: 15_000, TradedAt: fixed.Add(-time.Hour)},
	}
	s := &strategy.PoliticianFollow{
		Recent:      func(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) { return trades, nil },
		LookbackDur: 7 * 24 * time.Hour,
		Now:         func() time.Time { return fixed },
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "politician:AAPL:buy:20260420", sigs[0].RefID)

	// Same trades, same day ⇒ identical RefID (so Upsert collapses).
	sigs2, err := s.Generate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, sigs[0].RefID, sigs2[0].RefID)
}

// Year-0001 trades leak through when Quiver changes its date format.
// They must not produce signals — the lookback filter can't reason
// about them and they used to pollute the watchlist.
func TestPoliticianFollow_DropsZeroDateTrades(t *testing.T) {
	trades := []domain.PoliticianTrade{
		{PoliticianName: "A", Symbol: "AAPL", Side: domain.SideBuy, AmountMinUSD: 1001, AmountMaxUSD: 15_000, TradedAt: time.Time{}},
	}
	s := &strategy.PoliticianFollow{
		Recent:      func(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) { return trades, nil },
		LookbackDur: 7 * 24 * time.Hour,
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

// CUSIPs and foreign-exchange tickers ("571903BM4", "LM09.SG") used
// to flow through to the broker and 400. Strategies must reject them
// at signal-generation time.
func TestPoliticianFollow_RejectsNonTradeableSymbols(t *testing.T) {
	trades := []domain.PoliticianTrade{
		{PoliticianName: "A", Symbol: "571903BM4", Side: domain.SideBuy, AmountMinUSD: 100_000, AmountMaxUSD: 250_000, TradedAt: time.Now()},
		{PoliticianName: "B", Symbol: "LM09.SG", Side: domain.SideBuy, AmountMinUSD: 100_000, AmountMaxUSD: 250_000, TradedAt: time.Now()},
		{PoliticianName: "C", Symbol: "AAPL", Side: domain.SideBuy, AmountMinUSD: 100_000, AmountMaxUSD: 250_000, TradedAt: time.Now()},
	}
	s := &strategy.PoliticianFollow{
		Recent: func(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) { return trades, nil },
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "AAPL", sigs[0].Symbol)
}

// The reason string used to hardcode "$0-$%d" even when the DB had a
// real min amount, which made the dashboard misleading. Now it must
// show the actual min-max aggregation.
func TestPoliticianFollow_ReasonShowsRealMinMax(t *testing.T) {
	trades := []domain.PoliticianTrade{
		{PoliticianName: "A", Symbol: "AAPL", Side: domain.SideBuy, AmountMinUSD: 1001, AmountMaxUSD: 15_000, TradedAt: time.Now()},
		{PoliticianName: "B", Symbol: "AAPL", Side: domain.SideBuy, AmountMinUSD: 15_001, AmountMaxUSD: 50_000, TradedAt: time.Now()},
	}
	s := &strategy.PoliticianFollow{
		Recent: func(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) { return trades, nil },
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	// $1,001 + $15,001 = $16,002 minimum; $15,000 + $50,000 = $65,000 maximum
	assert.Contains(t, sigs[0].Reason, "$16,002")
	assert.Contains(t, sigs[0].Reason, "$65,000")
	assert.False(t, strings.Contains(sigs[0].Reason, "$0-"), "legacy '$0-' prefix must be gone: %q", sigs[0].Reason)
}

func TestNewsSentiment_CollapsesBySymbol(t *testing.T) {
	items := []domain.NewsItem{
		{Title: "A", Symbols: "AAPL", Sentiment: 0.8, Relevance: 0.9},
		{Title: "B", Symbols: "AAPL", Sentiment: 0.4, Relevance: 0.6},
		{Title: "C", Symbols: "TSLA", Sentiment: -0.5, Relevance: 0.7},
		{Title: "D", Symbols: "AAPL", Sentiment: 0.0, Relevance: 0.1}, // below MinRelevance
	}
	s := &strategy.NewsSentiment{
		Recent:       func(ctx context.Context) ([]domain.NewsItem, error) { return items, nil },
		MinRelevance: 0.3,
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 2)
	found := map[string]domain.Signal{}
	for _, s := range sigs {
		found[s.Symbol] = s
	}
	assert.Equal(t, domain.SideBuy, found["AAPL"].Side)
	assert.Equal(t, domain.SideSell, found["TSLA"].Side)
}

func TestNewsSentiment_EmitsStableRefID(t *testing.T) {
	fixed := time.Date(2026, 4, 20, 14, 30, 0, 0, time.UTC)
	items := []domain.NewsItem{
		{Title: "earnings beat", Symbols: "AAPL", Sentiment: 0.8, Relevance: 0.9},
	}
	s := &strategy.NewsSentiment{
		Recent:       func(ctx context.Context) ([]domain.NewsItem, error) { return items, nil },
		MinRelevance: 0.3,
		Now:          func() time.Time { return fixed },
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	// Hourly bucket: news moves faster than politician disclosures.
	assert.Equal(t, "news:AAPL:buy:2026042014", sigs[0].RefID)
}

func TestNewsSentiment_RejectsNonTradeableSymbols(t *testing.T) {
	items := []domain.NewsItem{
		{Title: "A", Symbols: "LM09.SG,AAPL", Sentiment: 0.8, Relevance: 0.9},
	}
	s := &strategy.NewsSentiment{
		Recent:       func(ctx context.Context) ([]domain.NewsItem, error) { return items, nil },
		MinRelevance: 0.3,
	}
	sigs, err := s.Generate(context.Background())
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	assert.Equal(t, "AAPL", sigs[0].Symbol)
}

func TestMerge_SortsByAbsScore(t *testing.T) {
	sigs := []domain.Signal{
		{Symbol: "A", Side: domain.SideBuy, Score: 0.1, Confidence: 0.5},
		{Symbol: "B", Side: domain.SideBuy, Score: 0.8, Confidence: 0.9},
		{Symbol: "C", Side: domain.SideSell, Score: -0.5, Confidence: 0.8},
	}
	m := strategy.Merge(sigs)
	require.Len(t, m, 3)
	assert.Equal(t, "B", m[0].Symbol)
	assert.Equal(t, "C", m[1].Symbol)
	assert.Equal(t, "A", m[2].Symbol)
}

// The original Merge took the raw weighted average across all signals
// on all sides, which made a minority sell signal invisible against a
// majority of weak buys. That caused the "never sells" failure mode
// observed in production. Merge must now surface the dominant side
// separately so callers can act on it.
func TestMerge_SurfacesDominantSellEvenWithMinorityBuys(t *testing.T) {
	// 1 strong sell (-0.8, 0.9) vs 3 very weak buys (+0.1, 0.3).
	// Weighted by confidence the sell dominates.
	sigs := []domain.Signal{
		{Symbol: "X", Side: domain.SideSell, Score: -0.8, Confidence: 0.9},
		{Symbol: "X", Side: domain.SideBuy, Score: 0.1, Confidence: 0.3},
		{Symbol: "X", Side: domain.SideBuy, Score: 0.1, Confidence: 0.3},
		{Symbol: "X", Side: domain.SideBuy, Score: 0.1, Confidence: 0.3},
	}
	m := strategy.Merge(sigs)
	require.Len(t, m, 1)
	assert.Equal(t, domain.SideSell, m[0].DominantSide)
	assert.Less(t, m[0].Score, 0.0)
}

func TestMerge_DominantBuy(t *testing.T) {
	sigs := []domain.Signal{
		{Symbol: "Y", Side: domain.SideBuy, Score: 0.9, Confidence: 0.9},
		{Symbol: "Y", Side: domain.SideSell, Score: -0.2, Confidence: 0.3},
	}
	m := strategy.Merge(sigs)
	require.Len(t, m, 1)
	assert.Equal(t, domain.SideBuy, m[0].DominantSide)
	assert.Greater(t, m[0].Score, 0.0)
}

func TestValidTicker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in string
		ok bool
	}{
		{"AAPL", true},
		{"F", true},
		{"BRK.B", true},
		{"BF.A", true},
		{"MSFT", true},
		{"", false},
		{"N/A", false},
		{"571903BM4", false}, // CUSIP
		{"LM09.SG", false},   // foreign
		{"0QZI.IL", false},
		{"3V64.TI", false},
		{"TOOLONG", false},
		{"aapl", true},  // normalised
		{" AAPL ", true}, // trimmed
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.ok, strategy.ValidTicker(c.in))
		})
	}
}
