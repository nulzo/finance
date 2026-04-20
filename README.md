# trader

An autonomous trading daemon written in Go. It ingests **congressional /
senate stock disclosures**, **market news**, and **open events**, fuses
them into normalized trading signals, asks an **LLM** (via any
OpenAI-compatible endpoint – a local model-router, OpenRouter, or
OpenAI directly) to reason over the context, sizes the trade through a
**risk engine**, and executes through a broker abstraction (mock,
Alpaca paper, or Alpaca live). Everything is persisted so you can
build dashboards on top of the exposed REST API.

## Features

- **Politician-follow strategy** – tracks House + Senate disclosed
  trades from multiple providers (CapitolTrades, Quiver Quantitative)
  with deterministic dedupe and politician-level weighting.
- **News-sentiment strategy** – aggregates from Finnhub, NewsAPI.org and
  an RSS fallback (WSJ, SEC 8-K feed, Seeking Alpha). Each item is
  enriched by the LLM with a sentiment + relevance score and symbol tagging.
- **LLM-driven decisioning** – structured JSON output from any
  OpenAI-compatible endpoint with a primary model + fallback chain
  (`openai/gpt-4o-mini` → `anthropic/claude-3.5-haiku` →
  `google/gemini-2.0-flash` → `ollama/llama3.1` by default). By default
  the client points at a self-hosted model-router
  (`http://192.168.1.249:8080/v1`) and auth is optional so a router with
  `SERVER_AUTH_ENABLED=false` works out of the box. When the endpoint is
  unreachable the engine falls back to a deterministic heuristic so it
  still works end-to-end offline.
- **Risk engine** – per-order USD cap, per-symbol USD cap, fractional
  equity exposure cap, daily order count, daily realised-loss stop,
  symbol blacklist, and optional `REQUIRE_APPROVAL` manual gating.
- **Broker abstraction** – `MockBroker` (in-memory fills against a
  price feed) or Alpaca REST (paper + live). The Alpaca client also
  serves as the market-data primary with a Yahoo fallback.
- **Wallet accounting** – cash, reservations, positions, realised P&L,
  and audit log all persisted via SQLite (`sqlx` + embedded migrations).
- **REST API** – Gin-based, token-optional, covers portfolios, orders,
  politicians, news, signals, decisions, quotes, audit, and
  engine/broker controls. See [API](#rest-api) below.
- **Comprehensive tests** – unit tests across domain, money arithmetic,
  risk, mock broker, storage, strategies, LLM client (httptest), market
  cache + fallback, provider aggregators, api handlers, and the engine
  end-to-end.

## Layout

```
backend/          Go trader daemon + HTTP API (module: github.com/nulzo/trader)
frontend/         React + Vite dashboard (served via nginx in prod)
infra/            docker-compose for local full-stack dev
.github/          CI/CD — builds both images to GHCR, deploys via Helm
Makefile          Top-level dev/build/push targets (see `make help`)
```

## Quickstart

```bash
# Backend
cp backend/.env.example backend/.env   # edit as needed
make backend-run                       # go run ./cmd/trader on :8080

# Frontend (separate terminal)
cd frontend && bun install && bun run dev   # vite on :5173, proxies /api → :8080

# Or the whole stack in docker
make compose-up
```

With **no** API keys it runs against mock prices, the CapitolTrades BFF
(public), and deterministic heuristics. Point `LLM_BASE_URL` at any
OpenAI-compatible `/v1/chat/completions` endpoint (the default is a
local [`model-router-api`](https://github.com/nulzo/model-router-api)
at `http://192.168.1.249:8080/v1`) and – if the router requires auth –
set `LLM_API_KEY` to enable LLM-driven decisions. To send real orders,
set `ALPACA_API_*` + `BROKER_PROVIDER=alpaca` + `TRADER_MODE=paper` to
route through Alpaca paper.

The legacy `OPENROUTER_API_KEY` / `OPENROUTER_BASE_URL` environment
variables are still honored as fallbacks for backwards compatibility.

## Architecture

```
backend/
  cmd/trader/                    entry point – wiring only
  internal/
    config/      env-driven config with validation
    domain/      core types + Money value object
    storage/     sqlx repositories + embedded migrations
    llm/         OpenAI-compatible client + NewsAnalysis + TradeRationale
    providers/
      congress/  CapitolTrades + Quiver + Aggregator (dedup/fallback)
      news/      Finnhub + NewsAPI + RSS fallback + Aggregator
      market/    Broker-primary cached provider w/ Yahoo fallback
    broker/      Broker interface; MockBroker + AlpacaBroker
    risk/        Pre-trade risk engine (pure function)
    strategy/    PoliticianFollow + NewsSentiment + Merge
    engine/      Orchestrator: Ingest + DecideAndTrade loops
    api/         Gin handlers for dashboards + controls
    telemetry/   zerolog setup
frontend/        Vite + React dashboard (served via nginx in prod)
infra/           docker-compose.yml for local dev
```

## REST API

All endpoints other than `/health` and `/v1/version` live under `/v1`.
When `API_TOKEN` is set the client must send `Authorization: Bearer $TOKEN`.

| Method | Path | Purpose |
|--------|------|---------|
| GET  | `/health` | liveness |
| GET  | `/v1/version` | build metadata |
| GET  | `/v1/portfolios` | list portfolios |
| GET  | `/v1/portfolios/:id` | portfolio with positions |
| POST | `/v1/portfolios/:id/deposit` `{amount_cents}` | top up the wallet |
| POST | `/v1/portfolios/:id/withdraw` `{amount_cents}` | withdraw |
| GET  | `/v1/portfolios/:id/positions` | open positions |
| GET  | `/v1/portfolios/:id/orders` | recent orders |
| POST | `/v1/portfolios/:id/orders` `{symbol, side, quantity|notional_usd}` | manual order |
| GET  | `/v1/politicians` | tracked politicians |
| POST | `/v1/politicians` | upsert tracked politician |
| GET  | `/v1/politician-trades?limit=` | persisted disclosed trades |
| GET  | `/v1/news?limit=` | cached news items (with LLM sentiment) |
| GET  | `/v1/signals?symbol=` | active signals |
| GET  | `/v1/decisions?limit=` | decisions with reasoning & model |
| POST | `/v1/decisions/:id/execute` | force-execute (respects risk engine) |
| GET  | `/v1/audit?limit=` | audit trail |
| GET  | `/v1/llm/calls` | persisted LLM attempts (tokens, cost, latency) |
| GET  | `/v1/llm/usage?group_by=day` | cost/token aggregates |
| GET  | `/v1/quotes/:symbol` | latest price (cached) |
| GET  | `/v1/engine/status` | is the engine active? |
| POST | `/v1/engine/toggle` `{enabled}` | enable/disable the engine |
| POST | `/v1/engine/ingest` | run the ingestion loop immediately |
| POST | `/v1/engine/decide` | run the decision loop immediately |
| GET  | `/v1/broker/account` | broker-side account summary |
| GET  | `/v1/broker/positions` | broker-side positions |

### Shape of a Decision

```json
{
  "id": "uuid",
  "portfolio_id": "uuid",
  "symbol": "NVDA",
  "action": "buy",
  "score": 0.72,
  "confidence": 0.85,
  "target_usd": "420.00",
  "reasoning": "Strong politician-follow signal; news sentiment +0.6; healthy exposure headroom.",
  "model_used": "openai/gpt-4o-mini",
  "signal_ids": "[\"...\",\"...\"]",
  "executed_id": "order-uuid",
  "created_at": "2026-04-19T22:11:00Z"
}
```

This is what dashboards should render: score, confidence, reasoning,
model, and (if executed) the linked order.

### LLM cost tracking

Every call to the LLM — including each fallback attempt when the
primary model fails — is persisted to the `llm_calls` table so you can
audit exactly what was sent, what came back, and what it cost.

Schema highlights:

| column | meaning |
|--------|---------|
| `operation`         | caller tag, e.g. `news.analyse`, `engine.decide` |
| `attempt_index`     | 0 for primary, 1+ for fallbacks within one `Complete()` |
| `model_requested`   | the model we asked for |
| `model_used`        | the model that actually answered (from the API response) |
| `outcome`           | `ok` / `http_503` / `transport_error` / `decode_error` / `empty_choices` / ... |
| `prompt_tokens`, `completion_tokens`, `total_tokens` | usage reported by the provider |
| `prompt_cost_usd`, `completion_cost_usd`, `total_cost_usd` | computed from the price table, stored as 8-dp decimals |
| `latency_ms`        | wall-clock round trip for this attempt |
| `request_messages`  | full JSON of the messages sent (capped at 64KB) |
| `response_text`     | the assistant's reply, or the raw error body on 4xx/5xx |
| `trace_id`, `span_id` | correlate back to an OTel trace |
| `temperature`, `max_tokens`, `json_mode` | request params echoed for auditing |

Query it via:

```bash
# Recent attempts (paginated, filterable)
curl 'http://localhost:8080/v1/llm/calls?limit=50&operation=engine.decide'

# 30-day cost rollup grouped by model
curl 'http://localhost:8080/v1/llm/usage?group_by=model'

# Cost per day
curl 'http://localhost:8080/v1/llm/usage?group_by=day&since=2026-04-01T00:00:00Z'
```

Pricing lives in a built-in table covering common OpenAI / Anthropic /
Google / Meta / Mistral models, with zero cost applied to `ollama/*`
and `local/*` prefixes. Override or extend it via `LLM_PRICING_JSON`:

```bash
LLM_PRICING_JSON='{"openai/gpt-4o-mini":{"input_per_1m":"0.15","output_per_1m":"0.60"},"my-finetune":{"input_per_1m":"1.00","output_per_1m":"3.00"}}'
```

Values are USD per **million tokens** (the unit most providers publish).
Lookups try exact-match → provider-suffix-stripped → longest `*`
prefix → `default`. Unknown models fall through to $0 so rows still
land in the DB — useful for auditing even when pricing isn't set.

Persist writes are fire-and-forget: the LLM hot path never blocks on
SQLite. If the insert fails you'll see a debug log line but the call
itself is unaffected.

## Modes of operation

| Mode | Orders | Cash | Broker |
|------|--------|------|--------|
| `mock`  | fill instantly against `StaticPrices`/Yahoo quotes | internal wallet | `MockBroker` |
| `paper` | Alpaca paper endpoint | Alpaca paper | `AlpacaBroker` |
| `live`  | Alpaca live endpoint (requires credentials + account approval) | Alpaca live | `AlpacaBroker` |

All three honour the risk engine and persist identical domain objects,
so your dashboard code works without changes across modes.

## Extending

- **Add a broker** – implement `broker.Broker` (7 methods) and wire it in
  `backend/cmd/trader/main.go`.
- **Add a strategy** – implement `strategy.Strategy` and register it in
  `engine.regenerateSignals`.
- **Add a provider** – implement `congress.Source` or `news.Source` and
  append it to the aggregator in main.
- **Persist a new entity** – add a migration under
  `backend/internal/storage/migrations/` (applied automatically on boot)
  and a repo struct.

## Testing

```bash
make backend-test                     # unit tests
make backend-vet                      # static analysis
cd backend && go test -cover ./...    # with coverage
```

Tests use in-memory SQLite, `httptest` for HTTP providers, and never
touch the network or your broker.

## Observability

`trader` ships with end-to-end OpenTelemetry support:

- **Traces**: server spans on every HTTP request (via `otelgin`), client
  spans on every outbound HTTP call (via a `http.DefaultTransport`
  wrapped with `otelhttp` at startup), per-tick engine spans
  (`engine.tick` with `engine.loop` attribute), and per-call LLM spans
  (`llm.chat.completions` with standard `gen_ai.*` semantic attributes).
  Traces are exported via OTLP gRPC when `OTEL_EXPORTER_OTLP_ENDPOINT`
  is set; otherwise tracing is kept in-process (no network calls) so
  local dev stays quiet.
- **Metrics**: Go runtime metrics (goroutines, GC, heap), the standard
  `http.server.*` / `http.client.*` counters, and an app-specific set
  (`trader.orders.submitted`, `trader.orders.rejected`,
  `trader.orders.filled`, `trader.signals.generated`,
  `trader.decisions.made`, `trader.llm.calls`, `trader.llm.latency`,
  `trader.llm.tokens`, `trader.engine.tick.duration`,
  `trader.engine.tick.errors`, `trader.ingest.*`). Metrics are pushed
  via OTLP gRPC when the endpoint is configured **and** always
  exposed for Prometheus scraping at `GET /metrics` so a
  `ServiceMonitor` works with zero collector.
- **Logs**: structured zerolog with `trace_id` / `span_id` injected on
  every request log line, so logs join cleanly to traces in Tempo /
  Loki / Datadog / Grafana.
- **Health**: `/livez` (process up), `/readyz` (DB reachable),
  `/healthz` (legacy alias). Kubernetes probes map to these directly.
- **pprof**: optional, bound to `$PPROF_ADDR` (e.g. `:6060`). Keep it
  internal-only.

All of this is configured through the standard `OTEL_*` env vars — see
`.env.example` for the complete list.

## Deployment

The repo is packaged into a single `linux/arm64` Docker image (see
`Dockerfile`) that cross-compiles with Buildx and runs as the
unprivileged `trader` user with `/app/data` mounted as a writable
volume for the SQLite file.

`.github/workflows/deploy.yml` implements the full CD pipeline
(matching the pattern in the sibling `mochi` repo):

1. **Build** → `docker buildx build --platform linux/arm64` →
   push to GHCR as `ghcr.io/<owner>/<repo>:${sha}` + `latest`.
2. **Deploy** → join the cluster via Tailscale, load a base64 kubeconfig
   from GitHub Secrets, then `helm upgrade --install trader
   ./charts-repo/app` against the shared `nulzo/helm-charts` chart.
3. Trader-specific values are injected with `--set`: ingress at
   `trader.nulzo.dev` (traefik), a 2Gi PVC at `/app/data`, livenessProbe
   `/livez`, readinessProbe `/readyz`, `imagePullSecrets: ghcr-secret`,
   OTEL env pointing at the in-cluster collector, and secrets for
   `QUIVER_TOKEN` / `FINNHUB_KEY` / `NEWSAPI_KEY` / `ALPACA_*` /
   `TRADER_API_TOKEN`.

Required GitHub Secrets: `GH_PAT`, `TS_AUTHKEY`, `KUBECONFIG` (base64),
`TRADER_API_TOKEN`, `QUIVER_TOKEN`, `FINNHUB_KEY`, `NEWSAPI_KEY`,
`ALPACA_API_KEY`, `ALPACA_API_SECRET`.

## Disclaimer

This software is a reference implementation. Trading equities carries
risk. Review the risk configuration carefully before running in `paper`
or `live` modes. The authors take no responsibility for losses incurred.
