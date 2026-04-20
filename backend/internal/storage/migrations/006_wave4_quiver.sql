-- Wave 4 alternative data tables.
--
-- Quiver Quantitative exposes far more than congressional trades:
-- insider (Form 4) filings, WallStreetBets / Twitter social data,
-- corporate lobbying, federal contracts, and off-exchange short
-- volume. Each lands in its own table so the schema stays readable
-- and the individual feeds can evolve independently.
--
-- Every dataset follows the same conventions:
--   * raw_hash for idempotent INSERT ... ON CONFLICT dedup
--   * source column (default 'quiver') so future providers can
--     co-exist with backfills / replacements without a schema change
--   * symbol-indexed for the hot-path "recent events for this ticker"
--     read pattern used by strategies and the LLM decision context

-- Form 4 insider transactions (CEOs, CFOs, directors buying/selling
-- their own company's stock on the open market). The signal here is
-- substantially higher-conviction than congressional disclosures
-- because filers sit inside the company and trade their own book
-- under personal legal exposure.
CREATE TABLE IF NOT EXISTS insider_trades (
    id             TEXT PRIMARY KEY,
    symbol         TEXT NOT NULL,
    insider_name   TEXT NOT NULL,
    insider_title  TEXT NOT NULL DEFAULT '',
    side           TEXT NOT NULL, -- 'buy' | 'sell'
    shares         INTEGER NOT NULL DEFAULT 0,
    price_cents    INTEGER NOT NULL DEFAULT 0,
    value_usd      INTEGER NOT NULL DEFAULT 0,
    transacted_at  TIMESTAMP NOT NULL,
    filed_at       TIMESTAMP NOT NULL,
    source         TEXT NOT NULL DEFAULT 'quiver',
    raw_hash       TEXT NOT NULL UNIQUE,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_insider_trades_symbol_ts
    ON insider_trades(symbol, transacted_at DESC);
CREATE INDEX IF NOT EXISTS idx_insider_trades_filed
    ON insider_trades(filed_at DESC);

-- WallStreetBets / Twitter / social posts aggregated by Quiver. We
-- keep an hour-granularity rollup instead of individual posts — the
-- raw firehose is too noisy and Quiver already collapses it.
-- `mentions` is the volume over the bucket, `sentiment` is [-1, 1].
CREATE TABLE IF NOT EXISTS social_posts (
    id           TEXT PRIMARY KEY,
    symbol       TEXT NOT NULL,
    platform     TEXT NOT NULL, -- 'wsb' | 'twitter' | ...
    mentions     INTEGER NOT NULL DEFAULT 0,
    sentiment    REAL NOT NULL DEFAULT 0,
    followers    INTEGER NOT NULL DEFAULT 0, -- for twitter only
    bucket_at    TIMESTAMP NOT NULL,
    source       TEXT NOT NULL DEFAULT 'quiver',
    raw_hash     TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_social_posts_symbol_ts
    ON social_posts(symbol, bucket_at DESC);
CREATE INDEX IF NOT EXISTS idx_social_posts_platform_ts
    ON social_posts(platform, bucket_at DESC);

-- Corporate lobbying spend filed with the US Senate LDA. Used purely
-- as LLM decision context: "this company has been lobbying hard
-- against X regulation" is signal, but too slow to stand as its own
-- scored signal.
CREATE TABLE IF NOT EXISTS lobbying_events (
    id          TEXT PRIMARY KEY,
    symbol      TEXT NOT NULL,
    client      TEXT NOT NULL DEFAULT '',
    registrant  TEXT NOT NULL DEFAULT '',
    issue       TEXT NOT NULL DEFAULT '',
    amount_usd  INTEGER NOT NULL DEFAULT 0,
    filed_at    TIMESTAMP NOT NULL,
    period      TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'quiver',
    raw_hash    TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_lobbying_events_symbol_ts
    ON lobbying_events(symbol, filed_at DESC);

-- Federal contract awards from USAspending / SAM.gov via Quiver.
-- A company winning a multi-billion-dollar DoD contract is a
-- material revenue event the engine should weigh.
CREATE TABLE IF NOT EXISTS gov_contracts (
    id           TEXT PRIMARY KEY,
    symbol       TEXT NOT NULL,
    agency       TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    amount_usd   INTEGER NOT NULL DEFAULT 0,
    awarded_at   TIMESTAMP NOT NULL,
    source       TEXT NOT NULL DEFAULT 'quiver',
    raw_hash     TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_gov_contracts_symbol_ts
    ON gov_contracts(symbol, awarded_at DESC);

-- Off-exchange (FINRA ATS) short volume. Feeds the momentum
-- strategy's TechnicalContext to detect short-squeeze setups and
-- heavy institutional distribution.
CREATE TABLE IF NOT EXISTS short_volume (
    symbol              TEXT NOT NULL,
    day                 TIMESTAMP NOT NULL,
    short_volume        INTEGER NOT NULL DEFAULT 0,
    total_volume        INTEGER NOT NULL DEFAULT 0,
    short_exempt_volume INTEGER NOT NULL DEFAULT 0,
    short_ratio         REAL NOT NULL DEFAULT 0, -- short / total, clamped [0, 1]
    source              TEXT NOT NULL DEFAULT 'quiver',
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(symbol, day)
);
CREATE INDEX IF NOT EXISTS idx_short_volume_day
    ON short_volume(day DESC);
