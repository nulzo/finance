-- Persist every LLM inference attempt (primary + fallbacks) so cost
-- and latency can be audited. Decimal money fields are stored as TEXT
-- to keep the arbitrary-precision guarantees of shopspring/decimal.
CREATE TABLE IF NOT EXISTS llm_calls (
    id TEXT PRIMARY KEY,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    operation TEXT NOT NULL DEFAULT '',
    attempt_index INTEGER NOT NULL DEFAULT 0,
    model_requested TEXT NOT NULL DEFAULT '',
    model_used TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT 'ok',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    prompt_cost_usd TEXT NOT NULL DEFAULT '0',
    completion_cost_usd TEXT NOT NULL DEFAULT '0',
    total_cost_usd TEXT NOT NULL DEFAULT '0',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    request_bytes INTEGER NOT NULL DEFAULT 0,
    response_bytes INTEGER NOT NULL DEFAULT 0,
    request_messages TEXT NOT NULL DEFAULT '',
    response_text TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    span_id TEXT NOT NULL DEFAULT '',
    temperature REAL NOT NULL DEFAULT 0,
    max_tokens INTEGER NOT NULL DEFAULT 0,
    json_mode INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_llm_calls_created ON llm_calls(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_calls_model ON llm_calls(model_used, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_calls_op ON llm_calls(operation, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_calls_outcome ON llm_calls(outcome, created_at DESC);
