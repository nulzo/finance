package llm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/llm"
)

// fakeRecorder collects CallRecords synchronously so tests can assert
// on them without racing.
type fakeRecorder struct {
	mu    sync.Mutex
	calls []llm.CallRecord
}

func (r *fakeRecorder) RecordCall(_ context.Context, rec llm.CallRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, rec)
}

func (r *fakeRecorder) Calls() []llm.CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]llm.CallRecord, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestRecorder_RecordsSuccessWithCost(t *testing.T) {
	// Usage is echoed so cost can be computed.
	body := `{"model":"primary","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()

	// $1/1M input, $2/1M output.
	tbl := llm.NewPriceTable(map[string]llm.ModelPrice{
		"primary": {InputPer1M: decimal.NewFromInt(1), OutputPer1M: decimal.NewFromInt(2)},
	})
	rec := &fakeRecorder{}
	c := llm.NewClient("", s.URL, "primary", nil,
		llm.WithPricing(tbl), llm.WithRecorder(rec))

	ctx := llm.WithOperation(context.Background(), "test.op")
	_, _, err := c.Complete(ctx, []llm.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	calls := rec.Calls()
	require.Len(t, calls, 1)
	got := calls[0]
	assert.Equal(t, "test.op", got.Operation)
	assert.Equal(t, 0, got.AttemptIndex)
	assert.Equal(t, "primary", got.ModelRequested)
	assert.Equal(t, "primary", got.ModelUsed)
	assert.Equal(t, "ok", got.Outcome)
	assert.Equal(t, 1000, got.PromptTokens)
	assert.Equal(t, 500, got.CompletionTokens)
	assert.Equal(t, 1500, got.TotalTokens)
	// prompt cost: 1000 tok * $1/1M = $0.001
	// completion cost: 500 tok * $2/1M = $0.001
	// total: $0.002
	assert.Equal(t, "0.001", got.PromptCostUSD)
	assert.Equal(t, "0.001", got.CompletionCostUSD)
	assert.Equal(t, "0.002", got.TotalCostUSD)
	assert.NotEmpty(t, got.RequestMessages, "should capture messages")
	assert.Equal(t, "ok", got.ResponseText)
	assert.Equal(t, false, got.JSONMode)
}

func TestRecorder_RecordsEveryAttempt(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "boom", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fb","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer s.Close()

	rec := &fakeRecorder{}
	c := llm.NewClient("", s.URL, "primary", []string{"fb"},
		llm.WithRecorder(rec))

	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	recs := rec.Calls()
	require.Len(t, recs, 2, "both primary failure and fallback success should persist")
	assert.Equal(t, 0, recs[0].AttemptIndex)
	assert.Equal(t, "http_500", recs[0].Outcome)
	assert.Equal(t, "primary", recs[0].ModelRequested)
	assert.Equal(t, 1, recs[1].AttemptIndex)
	assert.Equal(t, "ok", recs[1].Outcome)
	assert.Equal(t, "fb", recs[1].ModelRequested)
}

func TestRecorder_RecordsTransportError(t *testing.T) {
	// Point at a port nothing listens on to force a transport error.
	rec := &fakeRecorder{}
	c := llm.NewClient("", "http://127.0.0.1:1", "m", nil,
		llm.WithRecorder(rec))
	_, _, _ = c.Complete(context.Background(), []llm.Message{{Role: "user", Content: "x"}})
	recs := rec.Calls()
	require.Len(t, recs, 1)
	assert.Equal(t, "transport_error", recs[0].Outcome)
	assert.NotEmpty(t, recs[0].ErrorMessage)
}

func TestOperationContextIsolated(t *testing.T) {
	ctx := llm.WithOperation(context.Background(), "a")
	assert.Equal(t, "a", llm.OperationFrom(ctx))
	// Empty op is a no-op so callers can unconditionally wrap.
	ctx2 := llm.WithOperation(ctx, "")
	assert.Equal(t, "a", llm.OperationFrom(ctx2))
	assert.Equal(t, "", llm.OperationFrom(context.Background()))
}
