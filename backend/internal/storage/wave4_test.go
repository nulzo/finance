package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
)

func TestInsiders_InsertDedupAndQuery(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	now := time.Now().UTC()

	t1 := domain.InsiderTrade{
		Symbol: "AAPL", InsiderName: "Tim Cook", InsiderTitle: "CEO", Side: domain.SideBuy,
		Shares: 1000, PriceCents: 18500, ValueUSD: 185_000,
		TransactedAt: now.Add(-48 * time.Hour), FiledAt: now.Add(-24 * time.Hour),
		RawHash: "hash-aapl-1",
	}
	ok, err := s.Insiders.Insert(ctx, &t1)
	require.NoError(t, err)
	require.True(t, ok, "first insert should return true")

	// Dedup: same raw_hash must no-op.
	ok2, err := s.Insiders.Insert(ctx, &domain.InsiderTrade{
		Symbol: "AAPL", InsiderName: "Tim Cook", Side: domain.SideBuy, RawHash: "hash-aapl-1",
		TransactedAt: now, FiledAt: now,
	})
	require.NoError(t, err)
	require.False(t, ok2, "duplicate raw_hash should be dropped")

	// Different symbol + hash inserts fresh.
	t2 := domain.InsiderTrade{
		Symbol: "MSFT", InsiderName: "Satya", Side: domain.SideSell,
		ValueUSD: 50_000, TransactedAt: now.Add(-12 * time.Hour), FiledAt: now.Add(-6 * time.Hour),
		RawHash: "hash-msft-1",
	}
	_, err = s.Insiders.Insert(ctx, &t2)
	require.NoError(t, err)

	all, err := s.Insiders.Since(ctx, now.Add(-72*time.Hour))
	require.NoError(t, err)
	require.Len(t, all, 2)

	onlyAAPL, err := s.Insiders.BySymbol(ctx, "AAPL", now.Add(-72*time.Hour))
	require.NoError(t, err)
	require.Len(t, onlyAAPL, 1)
	require.Equal(t, "AAPL", onlyAAPL[0].Symbol)
}

func TestSocial_InsertAndBySymbol(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	now := time.Now().UTC()

	posts := []domain.SocialPost{
		{Symbol: "GME", Platform: "wsb", Mentions: 2000, Sentiment: 0.7, BucketAt: now.Add(-2 * time.Hour), RawHash: "h1"},
		{Symbol: "GME", Platform: "wsb", Mentions: 1000, Sentiment: 0.4, BucketAt: now.Add(-1 * time.Hour), RawHash: "h2"},
		{Symbol: "AAPL", Platform: "twitter", Followers: 5000000, BucketAt: now.Add(-30 * time.Minute), RawHash: "h3"},
	}
	for i := range posts {
		_, err := s.Social.Insert(ctx, &posts[i])
		require.NoError(t, err)
	}
	gme, err := s.Social.BySymbol(ctx, "GME", now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Len(t, gme, 2)

	all, err := s.Social.Since(ctx, now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Len(t, all, 3)
}

func TestShortVolume_UpsertClampsRatio(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	day := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)

	// Insert with an absurdly large ratio — must clamp to 1.
	row := &domain.ShortVolume{
		Symbol: "AMC", Day: day,
		ShortVolume: 500, TotalVolume: 100, // nonsense pair, reporting-lag artefact
		ShortRatio: 5.0,
	}
	require.NoError(t, s.Shorts.Upsert(ctx, row))

	got, err := s.Shorts.LatestBySymbol(ctx, "AMC")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 1.0, got.ShortRatio, "ratio should be clamped to 1.0")

	// Re-upsert with sane values — should update in place (PK (symbol,day)).
	row2 := &domain.ShortVolume{
		Symbol: "AMC", Day: day,
		ShortVolume: 60, TotalVolume: 100, ShortRatio: 0.6,
	}
	require.NoError(t, s.Shorts.Upsert(ctx, row2))

	got2, err := s.Shorts.LatestBySymbol(ctx, "AMC")
	require.NoError(t, err)
	require.InDelta(t, 0.6, got2.ShortRatio, 1e-9)
	require.Equal(t, int64(60), got2.ShortVolume)
}

func TestLobbyingAndContracts_BasicRoundTrip(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	now := time.Now().UTC()

	l := &domain.LobbyingEvent{
		Symbol: "AAPL", Registrant: "Cravath", Issue: "App Store regulation",
		AmountUSD: 250_000, FiledAt: now.Add(-30 * 24 * time.Hour), Period: "Q1 2026",
		RawHash: "lob-1",
	}
	_, err := s.Lobbying.Insert(ctx, l)
	require.NoError(t, err)
	got, err := s.Lobbying.BySymbol(ctx, "AAPL", now.Add(-180*24*time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1)

	cc := &domain.GovContract{
		Symbol: "LMT", Agency: "DoD", Description: "F-35 sustainment",
		AmountUSD: 500_000_000, AwardedAt: now.Add(-10 * 24 * time.Hour),
		RawHash: "gc-1",
	}
	_, err = s.Contracts.Insert(ctx, cc)
	require.NoError(t, err)
	gotCC, err := s.Contracts.ListRecent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, gotCC, 1)
	require.Equal(t, "LMT", gotCC[0].Symbol)
}
