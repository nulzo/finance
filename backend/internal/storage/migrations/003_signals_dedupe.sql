-- Signals used to be inserted by the ingest loop without any dedup key,
-- so every tick duplicated the same (kind, symbol, side) aggregation.
-- This migration:
--   1. Deletes legacy rows that lack a RefID (they carry no provenance
--      and would all collide under the new unique index).
--   2. Drops expired rows so the unique index doesn't trip over stale
--      keys that will never be upserted again.
--   3. Enforces a composite uniqueness key so strategies can use
--      INSERT ... ON CONFLICT to upsert instead of stacking duplicates.
--
-- Strategies must populate ref_id going forward (e.g.
--   politician:SYMBOL:SIDE:YYYYMMDD
--   news:SYMBOL:SIDE:YYYYMMDDHH
-- ) so the same aggregation refreshes in place instead of creating a
-- new row each ingest tick.

DELETE FROM signals WHERE ref_id = '';
DELETE FROM signals WHERE expires_at < datetime('now');

CREATE UNIQUE INDEX IF NOT EXISTS idx_signals_key
    ON signals(kind, symbol, side, ref_id);

CREATE INDEX IF NOT EXISTS idx_signals_expires ON signals(expires_at);

-- Politician trades from Quiver have been persisting with year-0001
-- timestamps whenever the upstream date format changed. Drop them: the
-- strategy lookback filter can't reason about them and they bloat the
-- signal generator.
DELETE FROM politician_trades WHERE traded_at < '1990-01-01';
