-- Wave 1 accounting tables.
--
-- realized_events is the source of truth for daily realized P&L. The
-- engine inserts one row per sell-side fill, and the risk engine sums
-- `realized_cents` since UTC midnight to enforce MAX_DAILY_LOSS_USD.
-- Positions.realized_cents already tracks cumulative realized per
-- symbol, but daily caps require a time-windowed view; rather than
-- snapshotting at midnight we keep an append-only event log so the
-- calculation is trivially correct across restarts.
CREATE TABLE IF NOT EXISTS realized_events (
    id             TEXT PRIMARY KEY,
    portfolio_id   TEXT NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    symbol         TEXT NOT NULL,
    quantity       TEXT NOT NULL,
    realized_cents INTEGER NOT NULL,
    order_id       TEXT,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_realized_events_portfolio_ts
    ON realized_events(portfolio_id, created_at DESC);

-- cooldowns prevent the engine from re-evaluating a symbol that was
-- just rejected (by the risk engine or the broker). Without this the
-- decide loop hammers the same 5 rejected symbols every tick, wasting
-- LLM tokens and polluting the decision log. ExitPolicy-driven sells
-- intentionally bypass cooldowns so losers can always be cut.
CREATE TABLE IF NOT EXISTS cooldowns (
    portfolio_id TEXT NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    symbol       TEXT NOT NULL,
    until_ts     TIMESTAMP NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(portfolio_id, symbol)
);
CREATE INDEX IF NOT EXISTS idx_cooldowns_until ON cooldowns(until_ts);

-- Speed up the hot-path count-orders-today queries now that the engine
-- filters by status as well as portfolio_id + created_at.
CREATE INDEX IF NOT EXISTS idx_orders_portfolio_status_ts
    ON orders(portfolio_id, status, created_at DESC);
