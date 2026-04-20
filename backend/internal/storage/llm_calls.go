package storage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// LLMCallRepo persists llm_calls rows.
//
// Writes are best-effort: the caller (the LLM client's recorder) should
// never block the hot path on a failed insert. Callers are expected to
// log and move on when Insert returns an error.
type LLMCallRepo struct{ db *sqlx.DB }

// Insert persists a single LLM attempt. Missing fields (ID, CreatedAt)
// are initialised if empty.
func (r *LLMCallRepo) Insert(ctx context.Context, c *domain.LLMCall) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.Outcome == "" {
		c.Outcome = "ok"
	}
	_, err := r.db.NamedExecContext(ctx, `
        INSERT INTO llm_calls (
            id, created_at, operation, attempt_index,
            model_requested, model_used, outcome,
            prompt_tokens, completion_tokens, total_tokens,
            prompt_cost_usd, completion_cost_usd, total_cost_usd,
            latency_ms, request_bytes, response_bytes,
            request_messages, response_text, error_message,
            trace_id, span_id, temperature, max_tokens, json_mode
        ) VALUES (
            :id, :created_at, :operation, :attempt_index,
            :model_requested, :model_used, :outcome,
            :prompt_tokens, :completion_tokens, :total_tokens,
            :prompt_cost_usd, :completion_cost_usd, :total_cost_usd,
            :latency_ms, :request_bytes, :response_bytes,
            :request_messages, :response_text, :error_message,
            :trace_id, :span_id, :temperature, :max_tokens, :json_mode
        )
    `, c)
	return err
}

// ListFilter selects recent calls.
type ListFilter struct {
	Limit     int
	Offset    int
	Operation string // exact match
	Model     string // exact match on model_used
	Outcome   string // exact match
	Since     *time.Time
	Until     *time.Time
}

// List returns rows matching filter, newest first.
func (r *LLMCallRepo) List(ctx context.Context, f ListFilter) ([]domain.LLMCall, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	where, args := "WHERE 1=1", []any{}
	if f.Operation != "" {
		where += " AND operation=?"
		args = append(args, f.Operation)
	}
	if f.Model != "" {
		where += " AND model_used=?"
		args = append(args, f.Model)
	}
	if f.Outcome != "" {
		where += " AND outcome=?"
		args = append(args, f.Outcome)
	}
	if f.Since != nil {
		where += " AND created_at >= ?"
		args = append(args, *f.Since)
	}
	if f.Until != nil {
		where += " AND created_at < ?"
		args = append(args, *f.Until)
	}
	q := `SELECT * FROM llm_calls ` + where + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)
	var out []domain.LLMCall
	if err := r.db.SelectContext(ctx, &out, q, args...); err != nil {
		return nil, err
	}
	return out, nil
}

// UsageTotals is the rolled-up cost/token counts across a time range.
type UsageTotals struct {
	Calls            int             `db:"calls" json:"calls"`
	PromptTokens     int64           `db:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens int64           `db:"completion_tokens" json:"completion_tokens"`
	TotalTokens      int64           `db:"total_tokens" json:"total_tokens"`
	TotalCostUSD     decimal.Decimal `db:"total_cost_usd" json:"total_cost_usd"`
	AvgLatencyMS     float64         `db:"avg_latency_ms" json:"avg_latency_ms"`
	ErrorCalls       int             `db:"error_calls" json:"error_calls"`
}

// UsageRow is a bucketed aggregation.
type UsageRow struct {
	Bucket           string          `db:"bucket" json:"bucket"`
	Calls            int             `db:"calls" json:"calls"`
	PromptTokens     int64           `db:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens int64           `db:"completion_tokens" json:"completion_tokens"`
	TotalTokens      int64           `db:"total_tokens" json:"total_tokens"`
	TotalCostUSD     decimal.Decimal `db:"total_cost_usd" json:"total_cost_usd"`
	AvgLatencyMS     float64         `db:"avg_latency_ms" json:"avg_latency_ms"`
	ErrorCalls       int             `db:"error_calls" json:"error_calls"`
}

// Totals returns aggregate usage since t (inclusive). If t is zero,
// the full history is summed.
func (r *LLMCallRepo) Totals(ctx context.Context, since time.Time) (UsageTotals, error) {
	var t UsageTotals
	// total_cost_usd is TEXT — coerce via CAST(... AS REAL) then sum, then
	// render back through shopspring for a nice JSON.
	q := `SELECT
            COUNT(*) AS calls,
            COALESCE(SUM(prompt_tokens),0) AS prompt_tokens,
            COALESCE(SUM(completion_tokens),0) AS completion_tokens,
            COALESCE(SUM(total_tokens),0) AS total_tokens,
            COALESCE(SUM(CAST(total_cost_usd AS REAL)),0) AS total_cost_real,
            COALESCE(AVG(latency_ms),0) AS avg_latency_ms,
            COALESCE(SUM(CASE WHEN outcome != 'ok' THEN 1 ELSE 0 END),0) AS error_calls
          FROM llm_calls WHERE created_at >= ?`
	var raw struct {
		Calls          int     `db:"calls"`
		PromptTokens   int64   `db:"prompt_tokens"`
		CompletionTok  int64   `db:"completion_tokens"`
		TotalTokens    int64   `db:"total_tokens"`
		TotalCostReal  float64 `db:"total_cost_real"`
		AvgLatencyMS   float64 `db:"avg_latency_ms"`
		ErrorCalls     int     `db:"error_calls"`
	}
	if err := r.db.GetContext(ctx, &raw, q, since); err != nil {
		return t, err
	}
	t.Calls = raw.Calls
	t.PromptTokens = raw.PromptTokens
	t.CompletionTokens = raw.CompletionTok
	t.TotalTokens = raw.TotalTokens
	t.TotalCostUSD = decimal.NewFromFloat(raw.TotalCostReal).Round(8)
	t.AvgLatencyMS = raw.AvgLatencyMS
	t.ErrorCalls = raw.ErrorCalls
	return t, nil
}

// groupByExpr maps a user-facing dimension to a SQL expression used
// both in SELECT and GROUP BY clauses. Unknown values default to "day".
func groupByExpr(dim string) string {
	switch dim {
	case "model":
		return "model_used"
	case "operation":
		return "operation"
	case "outcome":
		return "outcome"
	case "hour":
		return "strftime('%Y-%m-%dT%H:00:00Z', created_at)"
	default:
		return "strftime('%Y-%m-%d', created_at)"
	}
}

// UsageBy returns buckets aggregated by dimension since the given
// timestamp. dimension is one of: "day" (default), "hour", "model",
// "operation", "outcome".
func (r *LLMCallRepo) UsageBy(ctx context.Context, dimension string, since time.Time) ([]UsageRow, error) {
	expr := groupByExpr(dimension)
	q := `SELECT
            ` + expr + ` AS bucket,
            COUNT(*) AS calls,
            COALESCE(SUM(prompt_tokens),0) AS prompt_tokens,
            COALESCE(SUM(completion_tokens),0) AS completion_tokens,
            COALESCE(SUM(total_tokens),0) AS total_tokens,
            COALESCE(SUM(CAST(total_cost_usd AS REAL)),0) AS total_cost_real,
            COALESCE(AVG(latency_ms),0) AS avg_latency_ms,
            COALESCE(SUM(CASE WHEN outcome != 'ok' THEN 1 ELSE 0 END),0) AS error_calls
          FROM llm_calls WHERE created_at >= ?
          GROUP BY bucket
          ORDER BY bucket DESC
          LIMIT 500`
	type rawRow struct {
		Bucket         string  `db:"bucket"`
		Calls          int     `db:"calls"`
		PromptTokens   int64   `db:"prompt_tokens"`
		CompletionTok  int64   `db:"completion_tokens"`
		TotalTokens    int64   `db:"total_tokens"`
		TotalCostReal  float64 `db:"total_cost_real"`
		AvgLatencyMS   float64 `db:"avg_latency_ms"`
		ErrorCalls     int     `db:"error_calls"`
	}
	var rs []rawRow
	if err := r.db.SelectContext(ctx, &rs, q, since); err != nil {
		return nil, err
	}
	out := make([]UsageRow, 0, len(rs))
	for _, r := range rs {
		out = append(out, UsageRow{
			Bucket:           r.Bucket,
			Calls:            r.Calls,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTok,
			TotalTokens:      r.TotalTokens,
			TotalCostUSD:     decimal.NewFromFloat(r.TotalCostReal).Round(8),
			AvgLatencyMS:     r.AvgLatencyMS,
			ErrorCalls:       r.ErrorCalls,
		})
	}
	return out, nil
}
