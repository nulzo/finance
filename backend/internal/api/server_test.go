package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/api"
	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/engine"
	"github.com/nulzo/trader/internal/providers/market"
	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
)

type testEnv struct {
	srv       *api.Server
	store     *storage.Store
	mockBrok  *broker.MockBroker
	portfolio *domain.Portfolio
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	s, err := storage.Open(context.Background(), "file::memory:?_time_format=sqlite&cache=shared&_pragma=foreign_keys(on)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	p := &domain.Portfolio{Name: "main", Mode: "mock", CashCents: 1_000_000}
	require.NoError(t, s.Portfolios.Create(context.Background(), p))

	prices := broker.NewStaticPrices(map[string]decimal.Decimal{"AAPL": decimal.NewFromFloat(150)})
	mb := broker.NewMockBroker(prices, decimal.NewFromInt(10_000), 0)
	mp := market.NewCachedProvider(market.BrokerAdapter{B: mb}, nil, time.Second)

	re := risk.NewEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(1000)})
	eng := engine.New(engine.Deps{
		Store: s, Broker: mb, Market: mp, Risk: re, PortfolioID: p.ID, Log: zerolog.Nop(),
		IngestInterval: time.Minute, DecideInterval: time.Minute,
	})

	srv := api.New(api.Deps{
		Store: s, Broker: mb, Market: mp, Engine: eng, PortfolioID: p.ID, Log: zerolog.Nop(),
	})
	return &testEnv{srv: srv, store: s, mockBrok: mb, portfolio: p}
}

func (te *testEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	te.srv.Router.ServeHTTP(rec, req)
	return rec
}

func TestAPI_Health(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodGet, "/health", nil)
	assert.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}

func TestAPI_PortfolioList(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodGet, "/v1/portfolios", nil)
	assert.Equal(t, 200, rec.Code)
	var ps []domain.Portfolio
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ps))
	assert.Len(t, ps, 1)
}

func TestAPI_DepositWithdraw(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodPost, fmt.Sprintf("/v1/portfolios/%s/deposit", te.portfolio.ID), map[string]any{"amount_cents": 50_000})
	assert.Equal(t, 200, rec.Code)
	p, _ := te.store.Portfolios.Get(context.Background(), te.portfolio.ID)
	assert.Equal(t, int64(1_050_000), p.CashCents.Cents())

	rec = te.do(t, http.MethodPost, fmt.Sprintf("/v1/portfolios/%s/withdraw", te.portfolio.ID), map[string]any{"amount_cents": 100})
	assert.Equal(t, 200, rec.Code)
}

func TestAPI_ManualOrderEndToEnd(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodPost, fmt.Sprintf("/v1/portfolios/%s/orders", te.portfolio.ID), map[string]any{
		"symbol": "AAPL", "side": "buy", "notional_usd": 300,
	})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var o domain.Order
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &o))
	assert.Equal(t, "AAPL", o.Symbol)
	assert.Equal(t, domain.OrderStatusFilled, o.Status)

	pos, _ := te.store.Positions.List(context.Background(), te.portfolio.ID)
	require.Len(t, pos, 1)
	assert.True(t, pos[0].Quantity.GreaterThan(decimal.Zero))
}

func TestAPI_Quote(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodGet, "/v1/quotes/AAPL", nil)
	require.Equal(t, 200, rec.Code)
	var q domain.Quote
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &q))
	assert.Equal(t, "150", q.Price.String())
}

func TestAPI_EngineToggle(t *testing.T) {
	te := setup(t)
	rec := te.do(t, http.MethodPost, "/v1/engine/toggle", map[string]any{"enabled": false})
	require.Equal(t, 200, rec.Code)
	var out map[string]bool
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.False(t, out["enabled"])
}

func TestAPI_LLMCallsAndUsage(t *testing.T) {
	te := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustInsert := func(op, model, outcome string, cost decimal.Decimal, prompt, compl int) {
		require.NoError(t, te.store.LLMCalls.Insert(ctx, &domain.LLMCall{
			CreatedAt:        now,
			Operation:        op,
			ModelRequested:   model,
			ModelUsed:        model,
			Outcome:          outcome,
			PromptTokens:     prompt,
			CompletionTokens: compl,
			TotalTokens:      prompt + compl,
			TotalCostUSD:     cost,
			LatencyMS:        100,
		}))
	}
	mustInsert("news.analyse", "openai/gpt-4o-mini", "ok", decimal.NewFromFloat(0.001), 100, 50)
	mustInsert("news.analyse", "openai/gpt-4o-mini", "ok", decimal.NewFromFloat(0.002), 200, 60)
	mustInsert("engine.decide", "openai/gpt-4o", "http_500", decimal.NewFromFloat(0), 0, 0)

	// List
	rec := te.do(t, http.MethodGet, "/v1/llm/calls?limit=5", nil)
	require.Equal(t, 200, rec.Code)
	var calls []domain.LLMCall
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &calls))
	assert.Len(t, calls, 3)

	// List with filter
	rec = te.do(t, http.MethodGet, "/v1/llm/calls?operation=news.analyse", nil)
	require.Equal(t, 200, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &calls))
	assert.Len(t, calls, 2)
	for _, c := range calls {
		assert.Equal(t, "news.analyse", c.Operation)
	}

	// Usage roll-up grouped by model.
	rec = te.do(t, http.MethodGet, "/v1/llm/usage?group_by=model", nil)
	require.Equal(t, 200, rec.Code)
	var resp struct {
		Totals struct {
			Calls        int     `json:"calls"`
			TotalCostUSD string  `json:"total_cost_usd"`
			ErrorCalls   int     `json:"error_calls"`
			TotalTokens  int64   `json:"total_tokens"`
			AvgLatencyMS float64 `json:"avg_latency_ms"`
		} `json:"totals"`
		Buckets []struct {
			Bucket       string `json:"bucket"`
			Calls        int    `json:"calls"`
			TotalCostUSD string `json:"total_cost_usd"`
		} `json:"buckets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 3, resp.Totals.Calls)
	assert.Equal(t, 1, resp.Totals.ErrorCalls)
	assert.Len(t, resp.Buckets, 2)

	totalByModel := map[string]string{}
	for _, b := range resp.Buckets {
		totalByModel[b.Bucket] = b.TotalCostUSD
	}
	// 0.001 + 0.002 = 0.003
	assert.Equal(t, "0.003", totalByModel["openai/gpt-4o-mini"])
	assert.Equal(t, "0", totalByModel["openai/gpt-4o"])
}

func TestAPI_TokenAuth(t *testing.T) {
	te := setup(t)
	te.srv.Deps.APIToken = "secret"

	rec := te.do(t, http.MethodGet, "/v1/portfolios", nil)
	assert.Equal(t, 401, rec.Code)

	req := httptest.NewRequest(http.MethodGet, "/v1/portfolios", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	te.srv.Router.ServeHTTP(rec, req)
	assert.Equal(t, 200, rec.Code)
}
