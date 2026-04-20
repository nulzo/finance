-- Initial schema for the trader application.
CREATE TABLE IF NOT EXISTS portfolios (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    mode TEXT NOT NULL,
    cash_cents INTEGER NOT NULL DEFAULT 0,
    reserved_cents INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS positions (
    id TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    quantity TEXT NOT NULL DEFAULT '0',
    avg_cost_cents INTEGER NOT NULL DEFAULT 0,
    realized_cents INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(portfolio_id, symbol)
);

CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    type TEXT NOT NULL,
    time_in_force TEXT NOT NULL,
    quantity TEXT NOT NULL,
    limit_price TEXT,
    filled_qty TEXT NOT NULL DEFAULT '0',
    filled_avg_cents INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    broker_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    decision_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    submitted_at TIMESTAMP,
    filled_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_orders_portfolio ON orders(portfolio_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_symbol ON orders(symbol);

CREATE TABLE IF NOT EXISTS politicians (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    chamber TEXT NOT NULL,
    party TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    track_weight REAL NOT NULL DEFAULT 1.0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS politician_trades (
    id TEXT PRIMARY KEY,
    politician_id TEXT REFERENCES politicians(id) ON DELETE SET NULL,
    politician_name TEXT NOT NULL,
    chamber TEXT NOT NULL,
    symbol TEXT NOT NULL,
    asset_name TEXT NOT NULL DEFAULT '',
    side TEXT NOT NULL,
    amount_min_usd INTEGER NOT NULL DEFAULT 0,
    amount_max_usd INTEGER NOT NULL DEFAULT 0,
    traded_at TIMESTAMP NOT NULL,
    disclosed_at TIMESTAMP NOT NULL,
    source TEXT NOT NULL,
    raw_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ptrades_symbol ON politician_trades(symbol, traded_at DESC);
CREATE INDEX IF NOT EXISTS idx_ptrades_politician ON politician_trades(politician_id);

CREATE TABLE IF NOT EXISTS news_items (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    symbols TEXT NOT NULL DEFAULT '',
    sentiment REAL NOT NULL DEFAULT 0,
    relevance REAL NOT NULL DEFAULT 0,
    pub_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_news_pub ON news_items(pub_at DESC);

CREATE TABLE IF NOT EXISTS signals (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    score REAL NOT NULL,
    confidence REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    ref_id TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_signals_symbol ON signals(symbol, created_at DESC);

CREATE TABLE IF NOT EXISTS decisions (
    id TEXT PRIMARY KEY,
    portfolio_id TEXT NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    action TEXT NOT NULL,
    score REAL NOT NULL,
    confidence REAL NOT NULL,
    target_usd TEXT NOT NULL DEFAULT '0',
    reasoning TEXT NOT NULL DEFAULT '',
    model_used TEXT NOT NULL DEFAULT '',
    signal_ids TEXT NOT NULL DEFAULT '[]',
    executed_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_decisions_portfolio ON decisions(portfolio_id, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_log (
    id TEXT PRIMARY KEY,
    entity TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    action TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_log(entity, entity_id);
