package strategy

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/nulzo/trader/internal/domain"
)

func TestInsiderFollow_CEOClusterEmitsHighConfidenceBuy(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	trades := []domain.InsiderTrade{
		{Symbol: "AAPL", InsiderName: "Tim Cook", InsiderTitle: "CEO", Side: domain.SideBuy, ValueUSD: 1_500_000, TransactedAt: now.Add(-2 * 24 * time.Hour), FiledAt: now.Add(-1 * 24 * time.Hour)},
		{Symbol: "AAPL", InsiderName: "Luca Maestri", InsiderTitle: "CFO", Side: domain.SideBuy, ValueUSD: 500_000, TransactedAt: now.Add(-3 * 24 * time.Hour), FiledAt: now.Add(-2 * 24 * time.Hour)},
		{Symbol: "AAPL", InsiderName: "Jeff Williams", InsiderTitle: "COO", Side: domain.SideBuy, ValueUSD: 250_000, TransactedAt: now.Add(-5 * 24 * time.Hour), FiledAt: now.Add(-4 * 24 * time.Hour)},
	}
	s := &InsiderFollow{
		Recent: func(_ context.Context, _ time.Time) ([]domain.InsiderTrade, error) { return trades, nil },
		HalfLife: 10 * 24 * time.Hour,
		Now:     func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("want 1 signal, got %d: %+v", len(sigs), sigs)
	}
	got := sigs[0]
	if got.Kind != domain.SignalKindInsider || got.Symbol != "AAPL" || got.Side != domain.SideBuy {
		t.Fatalf("wrong shape: %+v", got)
	}
	if got.Score <= 0 {
		t.Errorf("expected positive buy score, got %.2f", got.Score)
	}
	if got.Confidence < 0.7 {
		t.Errorf("CEO+CFO+COO cluster should carry high confidence, got %.2f", got.Confidence)
	}
	if got.RefID == "" || got.ExpiresAt.Before(now) {
		t.Errorf("expected valid RefID + future ExpiresAt, got %+v", got)
	}
}

func TestInsiderFollow_SingleInsiderSellIsSuppressed(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	trades := []domain.InsiderTrade{
		{Symbol: "TSLA", InsiderName: "Some Director", InsiderTitle: "Director", Side: domain.SideSell, ValueUSD: 5_000_000, TransactedAt: now.Add(-1 * 24 * time.Hour), FiledAt: now.Add(-12 * time.Hour)},
	}
	s := &InsiderFollow{
		Recent: func(_ context.Context, _ time.Time) ([]domain.InsiderTrade, error) { return trades, nil },
		Now:    func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("single insider sell should not emit a signal, got %+v", sigs)
	}
}

func TestInsiderFollow_TinyBuysFiltered(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	trades := []domain.InsiderTrade{
		{Symbol: "MSFT", InsiderName: "Board Member", InsiderTitle: "Director", Side: domain.SideBuy, ValueUSD: 5_000, TransactedAt: now.Add(-24 * time.Hour), FiledAt: now},
	}
	s := &InsiderFollow{
		Recent:      func(_ context.Context, _ time.Time) ([]domain.InsiderTrade, error) { return trades, nil },
		MinValueUSD: 25_000,
		Now:         func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("tiny buy should be filtered, got %+v", sigs)
	}
}

func TestSocialBuzz_BullishChorusEmitsBuy(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	posts := []domain.SocialPost{
		{Symbol: "GME", Platform: "wsb", Mentions: 5000, Sentiment: 0.7, BucketAt: now.Add(-2 * time.Hour)},
		{Symbol: "GME", Platform: "wsb", Mentions: 3000, Sentiment: 0.6, BucketAt: now.Add(-1 * time.Hour)},
	}
	s := &SocialBuzz{
		Recent: func(_ context.Context, _ time.Time) ([]domain.SocialPost, error) { return posts, nil },
		Now:    func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 1 || sigs[0].Side != domain.SideBuy || sigs[0].Symbol != "GME" {
		t.Fatalf("want GME buy, got %+v", sigs)
	}
	if sigs[0].Confidence > 0.55 {
		t.Errorf("confidence cap should hold, got %.2f", sigs[0].Confidence)
	}
	if sigs[0].Score <= 0 {
		t.Errorf("bullish chorus should produce positive score, got %.2f", sigs[0].Score)
	}
}

func TestSocialBuzz_NeutralSentimentSkipped(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	posts := []domain.SocialPost{
		{Symbol: "NVDA", Platform: "wsb", Mentions: 5000, Sentiment: 0.05, BucketAt: now.Add(-1 * time.Hour)},
	}
	s := &SocialBuzz{
		Recent: func(_ context.Context, _ time.Time) ([]domain.SocialPost, error) { return posts, nil },
		Now:    func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("neutral sentiment should not emit a signal, got %+v", sigs)
	}
}

func TestSocialBuzz_BelowMentionFloorSkipped(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	posts := []domain.SocialPost{
		{Symbol: "XYZ", Platform: "wsb", Mentions: 30, Sentiment: 0.9, BucketAt: now.Add(-1 * time.Hour)},
	}
	s := &SocialBuzz{
		Recent: func(_ context.Context, _ time.Time) ([]domain.SocialPost, error) { return posts, nil },
		Now:    func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("below mention floor should be filtered, got %+v", sigs)
	}
}

func TestSocialBuzz_TwitterOnlyRowsDoNotCount(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	posts := []domain.SocialPost{
		{Symbol: "AAPL", Platform: "twitter", Mentions: 0, Followers: 50_000_000, Sentiment: 0.02, BucketAt: now.Add(-1 * time.Hour)},
	}
	s := &SocialBuzz{
		Recent: func(_ context.Context, _ time.Time) ([]domain.SocialPost, error) { return posts, nil },
		Now:    func() time.Time { return now },
	}
	sigs, err := s.Generate(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("follower-only rows should be skipped, got %+v", sigs)
	}
}

func TestInsiderFollow_AgeDecayReducesOlderTrades(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	recent := []domain.InsiderTrade{
		{Symbol: "AAPL", InsiderName: "A CEO", InsiderTitle: "CEO", Side: domain.SideBuy, ValueUSD: 200_000, TransactedAt: now.Add(-1 * 24 * time.Hour), FiledAt: now},
	}
	old := []domain.InsiderTrade{
		{Symbol: "AAPL", InsiderName: "A CEO", InsiderTitle: "CEO", Side: domain.SideBuy, ValueUSD: 200_000, TransactedAt: now.Add(-20 * 24 * time.Hour), FiledAt: now},
	}
	mk := func(trades []domain.InsiderTrade) float64 {
		s := &InsiderFollow{
			Recent: func(_ context.Context, _ time.Time) ([]domain.InsiderTrade, error) { return trades, nil },
			HalfLife: 10 * 24 * time.Hour,
			Now:      func() time.Time { return now },
		}
		sigs, err := s.Generate(context.Background())
		if err != nil || len(sigs) != 1 {
			t.Fatalf("bad setup: err=%v sigs=%+v", err, sigs)
		}
		return math.Abs(sigs[0].Score)
	}
	recentScore := mk(recent)
	oldScore := mk(old)
	if !(recentScore > oldScore) {
		t.Fatalf("recent(%.2f) should outscore old(%.2f) with half-life decay", recentScore, oldScore)
	}
}
