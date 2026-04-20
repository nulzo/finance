-- Wave 3 ops tables.
--
-- `rejections` is a structured log of everything that was NOT traded,
-- along with why. We already log these via audit_log, but audit rows
-- are free-form strings — fine for forensics, bad for reporting. The
-- frontend "Why we didn't trade X" page needs to filter by portfolio,
-- symbol, source, and time window, so we persist a typed row per
-- rejection and index it for those queries.
--
-- `source` is one of: 'risk' (risk engine rejected pre-submit),
-- 'broker' (broker rejected the order post-submit), 'engine' (engine
-- short-circuited before evaluation — cooldown, daily cap, near-cap).
-- `decision_id` is nullable because engine-level short-circuits never
-- produce a decision row.
CREATE TABLE IF NOT EXISTS rejections (
    id           TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    symbol       TEXT NOT NULL,
    decision_id  TEXT,
    side         TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    reason       TEXT NOT NULL,
    target_usd   TEXT NOT NULL DEFAULT '0',
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_rejections_portfolio_ts
    ON rejections(portfolio_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rejections_symbol_ts
    ON rejections(symbol, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rejections_source
    ON rejections(source);

-- The Wave 3 partial-fill timeout needs a cheap way to ask "orders
-- that have been in `submitted` for longer than N minutes". The
-- existing idx_orders_portfolio_status_ts covers it on
-- created_at DESC; but the cancel loop wants oldest-first, so add a
-- complementary ascending index for submitted_at.
CREATE INDEX IF NOT EXISTS idx_orders_status_submitted_asc
    ON orders(status, submitted_at ASC);
