-- 007_equity_snapshots.sql
--
-- Wave 5 — portfolio time-series.
--
-- `equity_snapshots` records a full portfolio valuation at a single point
-- in time. Writing append-only snapshots (rather than trying to derive
-- historical equity from the order + quote logs) decouples chart rendering
-- from price-provider availability and keeps queries O(rows) instead of
-- O(orders × quotes). Each snapshot is self-contained so the UI can
-- render any subset of [cash | cost | mark | realised | unrealised |
-- equity] directly from one row.
--
-- Dimensional breakdown (all values in integer cents):
--   cash_cents         — buying-power cash at snapshot time.
--   positions_cost     — Σ(qty × avg_cost) across open positions.
--   positions_mtm      — Σ(qty × mark)     across open positions. `mark`
--                        is the best available live quote; if none is
--                        available we fall back to `avg_cost` so the
--                        unrealised delta reads as zero rather than
--                        looking like a sudden loss.
--   realized_cents     — cumulative realised P&L since inception. Kept
--                        on every snapshot so a chart can plot
--                        (realised + unrealised) without joining to
--                        `realized_events`.
--   unrealized_cents   — positions_mtm − positions_cost.
--   equity_cents       — cash_cents + positions_mtm.
--   open_positions     — number of non-zero-quantity rows contributing
--                        to positions_mtm (useful for a density chart).
--   priced_positions   — subset of `open_positions` that had a real
--                        live quote; gap = unquoted tail we marked at
--                        cost. Surfacing this lets the frontend annotate
--                        charts so "flat unrealised" doesn't get
--                        misread as "no movement".
CREATE TABLE IF NOT EXISTS equity_snapshots (
    id                TEXT    PRIMARY KEY,
    portfolio_id      TEXT    NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    taken_at          TIMESTAMP NOT NULL,
    cash_cents        INTEGER NOT NULL,
    positions_cost    INTEGER NOT NULL,
    positions_mtm     INTEGER NOT NULL,
    realized_cents    INTEGER NOT NULL,
    unrealized_cents  INTEGER NOT NULL,
    equity_cents      INTEGER NOT NULL,
    open_positions    INTEGER NOT NULL DEFAULT 0,
    priced_positions  INTEGER NOT NULL DEFAULT 0
);

-- The overwhelmingly common access pattern is "the most recent N
-- snapshots for a portfolio". A compound index on (portfolio_id,
-- taken_at DESC) serves both that query and the `ORDER BY taken_at DESC
-- LIMIT 1` "current equity" read.
CREATE INDEX IF NOT EXISTS idx_equity_snapshots_portfolio_ts
    ON equity_snapshots (portfolio_id, taken_at DESC);
