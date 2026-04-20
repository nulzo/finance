package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/storage"
)

func TestLLMCalls_InsertAndList(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rows := []domain.LLMCall{
		{
			CreatedAt:         now.Add(-2 * time.Hour),
			Operation:         "news.analyse",
			ModelRequested:    "openai/gpt-4o-mini",
			ModelUsed:         "openai/gpt-4o-mini",
			Outcome:           "ok",
			PromptTokens:      100,
			CompletionTokens:  50,
			TotalTokens:       150,
			PromptCostUSD:     decimal.NewFromFloat(0.000015),
			CompletionCostUSD: decimal.NewFromFloat(0.00003),
			TotalCostUSD:      decimal.NewFromFloat(0.000045),
			LatencyMS:         500,
			Temperature:       0.2,
			MaxTokens:         600,
			JSONMode:          true,
		},
		{
			CreatedAt:         now.Add(-1 * time.Hour),
			Operation:         "engine.decide",
			ModelRequested:    "openai/gpt-4o-mini",
			ModelUsed:         "openai/gpt-4o",
			Outcome:           "ok",
			PromptTokens:      2000,
			CompletionTokens:  400,
			TotalTokens:       2400,
			PromptCostUSD:     decimal.NewFromFloat(0.005),
			CompletionCostUSD: decimal.NewFromFloat(0.004),
			TotalCostUSD:      decimal.NewFromFloat(0.009),
			LatencyMS:         1200,
		},
		{
			CreatedAt:      now,
			Operation:      "news.analyse",
			ModelRequested: "openai/gpt-4o-mini",
			ModelUsed:      "openai/gpt-4o-mini",
			Outcome:        "http_503",
			ErrorMessage:   "service unavailable",
			LatencyMS:      80,
		},
	}
	for i := range rows {
		require.NoError(t, s.LLMCalls.Insert(ctx, &rows[i]))
		assert.NotEmpty(t, rows[i].ID, "ID should be assigned on insert")
	}

	// Newest first.
	got, err := s.LLMCalls.List(ctx, storage.ListFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "http_503", got[0].Outcome)

	// Filter by operation.
	got, err = s.LLMCalls.List(ctx, storage.ListFilter{Operation: "engine.decide"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "engine.decide", got[0].Operation)

	// Filter by outcome.
	got, err = s.LLMCalls.List(ctx, storage.ListFilter{Outcome: "ok"})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestLLMCalls_Totals(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insert := func(op string, cost decimal.Decimal, prompt, complete int, outcome string, latency int64) {
		require.NoError(t, s.LLMCalls.Insert(ctx, &domain.LLMCall{
			CreatedAt:        now,
			Operation:        op,
			Outcome:          outcome,
			PromptTokens:     prompt,
			CompletionTokens: complete,
			TotalTokens:      prompt + complete,
			TotalCostUSD:     cost,
			LatencyMS:        latency,
		}))
	}
	insert("news.analyse", decimal.NewFromFloat(0.01), 100, 20, "ok", 100)
	insert("news.analyse", decimal.NewFromFloat(0.02), 200, 30, "ok", 200)
	insert("engine.decide", decimal.NewFromFloat(0.05), 500, 100, "http_500", 300)

	tot, err := s.LLMCalls.Totals(ctx, now.Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, tot.Calls)
	assert.Equal(t, int64(800), tot.PromptTokens)
	assert.Equal(t, int64(150), tot.CompletionTokens)
	assert.Equal(t, int64(950), tot.TotalTokens)
	assert.InDelta(t, 0.08, tot.TotalCostUSD.InexactFloat64(), 1e-9)
	assert.InDelta(t, 200.0, tot.AvgLatencyMS, 1e-9)
	assert.Equal(t, 1, tot.ErrorCalls)
}

func TestLLMCalls_UsageByModel(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insert := func(model string, cost decimal.Decimal) {
		require.NoError(t, s.LLMCalls.Insert(ctx, &domain.LLMCall{
			CreatedAt:    now,
			ModelUsed:    model,
			Outcome:      "ok",
			TotalCostUSD: cost,
		}))
	}
	insert("openai/gpt-4o", decimal.NewFromFloat(1.0))
	insert("openai/gpt-4o", decimal.NewFromFloat(0.5))
	insert("google/gemini-2.0-flash", decimal.NewFromFloat(0.1))

	rows, err := s.LLMCalls.UsageBy(ctx, "model", now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// convert to map for stable assertions
	byModel := map[string]float64{}
	callsByModel := map[string]int{}
	for _, r := range rows {
		byModel[r.Bucket] = r.TotalCostUSD.InexactFloat64()
		callsByModel[r.Bucket] = r.Calls
	}
	assert.InDelta(t, 1.5, byModel["openai/gpt-4o"], 1e-9)
	assert.InDelta(t, 0.1, byModel["google/gemini-2.0-flash"], 1e-9)
	assert.Equal(t, 2, callsByModel["openai/gpt-4o"])
}
