import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

// ---------------------------------------------------------------------------
// Content model
//
// The FAQ is a flat array of entries grouped by category. Keeping it as data
// (instead of hardcoded markup) lets the page stay searchable/filterable and
// makes it trivial to keep growing it without rewriting the layout.
// ---------------------------------------------------------------------------

interface FaqEntry {
  id: string
  category: string
  term: string
  short: string
  body: React.ReactNode
  /** Optional list of related entry ids, rendered as links beneath the body. */
  related?: string[]
  /** Hidden keywords that boost search; these are not displayed. */
  keywords?: string[]
}

const categories = [
  { id: "portfolio", label: "Portfolio & cash" },
  { id: "positions", label: "Positions" },
  { id: "orders", label: "Orders" },
  { id: "decisions", label: "Decisions" },
  { id: "signals", label: "Signals" },
  { id: "news", label: "News" },
  { id: "politicians", label: "Politicians" },
  { id: "llm", label: "LLM & cost tracking" },
  { id: "engine", label: "Engine" },
  { id: "system", label: "System" },
  { id: "math", label: "Accounting & math" },
] as const

type CategoryId = (typeof categories)[number]["id"]

// Small helper to render inline code / labels consistently.
const C = ({ children }: { children: React.ReactNode }) => (
  <code className="bg-muted rounded px-1 py-0.5 text-[11.5px]">{children}</code>
)

const faqs: FaqEntry[] = [
  // ---------- Portfolio & cash ----------
  {
    id: "portfolio",
    category: "portfolio",
    term: "Portfolio",
    short: "The account wrapper that owns cash and positions.",
    body: (
      <>
        <p>
          A portfolio is a single account-like object with a name, a{" "}
          <b>mode</b>, a cash balance, a reserved balance, and a set of
          positions. The default portfolio is called <C>main</C> and is seeded
          at startup with <C>INITIAL_WALLET_CENTS</C> (default $10,000) the
          first time the app runs.
        </p>
        <p>
          Multiple portfolios are supported but the engine only trades against
          one at a time — select it with the portfolio switcher in the top
          bar.
        </p>
      </>
    ),
    related: ["mode", "cash", "reserved", "available", "cost-basis"],
  },
  {
    id: "mode",
    category: "portfolio",
    term: "Mode (mock / paper / live)",
    short: "Which broker backend fills orders for this portfolio.",
    body: (
      <>
        <ul className="list-disc pl-5">
          <li>
            <b>mock</b> — fills are simulated in-process. No network, no
            external account. Perfect for testing strategies against cached
            market data.
          </li>
          <li>
            <b>paper</b> — forwards orders to a broker's paper-trading
            endpoint (e.g. Alpaca paper). Real quotes, fake money.
          </li>
          <li>
            <b>live</b> — forwards orders to the broker's live endpoint. Real
            money. Guarded by <C>REQUIRE_APPROVAL</C>.
          </li>
        </ul>
        <p>
          Mode is a property of the portfolio row, not a global switch — you
          can run a mock portfolio alongside a paper one if the broker is
          configured.
        </p>
      </>
    ),
  },
  {
    id: "cash",
    category: "portfolio",
    term: "Cash",
    short: "Dollars sitting in the wallet, stored as integer cents.",
    body: (
      <>
        <p>
          <C>cash_cents</C> is the total un-invested money in the portfolio,
          stored as a 64-bit integer number of cents to avoid floating-point
          drift. Every buy decrements it by the filled notional; every sell
          increments it. Deposits / withdrawals adjust it directly.
        </p>
        <p>
          Displayed on the dashboard as dollars (<C>cents / 100</C>). A cash
          balance of <C>649987</C> means <b>$6,499.87</b>.
        </p>
      </>
    ),
    related: ["money", "reserved", "available", "deposit"],
  },
  {
    id: "reserved",
    category: "portfolio",
    term: "Reserved",
    short: "Cash set aside to back unfilled buy orders.",
    body: (
      <>
        <p>
          When you submit a buy order, the engine <b>reserves</b> an estimate
          of the notional so the same dollars can't be double-spent by a
          second concurrent order. Reserved cents stay out of{" "}
          <C>available_cents</C> until the order is filled, cancelled, or
          rejected.
        </p>
        <p>
          If you see <C>reserved_cents &gt; 0</C> with no pending orders,
          something failed to release the reservation — check the audit log
          and look for <C>risk_rejected</C> or <C>submitted</C> rows without
          matching <C>filled</C> ones.
        </p>
      </>
    ),
    related: ["cash", "available", "order-status"],
  },
  {
    id: "available",
    category: "portfolio",
    term: "Available",
    short: "Cash you can still spend right now.",
    body: (
      <>
        <p>
          <C>available = cash - reserved</C>. The risk engine sizes new orders
          against this number, not raw cash, so outstanding buy reservations
          can't push you into negative territory.
        </p>
      </>
    ),
    related: ["cash", "reserved"],
  },
  {
    id: "deposit",
    category: "portfolio",
    term: "Deposit / Withdraw",
    short: "Directly credit or debit the cash balance.",
    body: (
      <>
        <p>
          <C>POST /v1/portfolios/:id/deposit</C> and{" "}
          <C>POST /v1/portfolios/:id/withdraw</C> move cash in and out
          atomically and write a row to the audit log. These are the
          in-process equivalent of ACH transfers — no broker involvement, no
          positions touched.
        </p>
      </>
    ),
    related: ["cash", "audit-log"],
  },

  // ---------- Positions ----------
  {
    id: "position",
    category: "positions",
    term: "Position",
    short: "Your current holding of a single symbol in a portfolio.",
    body: (
      <>
        <p>
          One row per <C>(portfolio_id, symbol)</C>. Closing a position (qty
          reaching 0) doesn't delete the row — it stays around so cumulative{" "}
          <C>realized_cents</C> is preserved for that symbol.
        </p>
      </>
    ),
    related: ["quantity", "avg-cost", "cost-basis", "realized"],
  },
  {
    id: "quantity",
    category: "positions",
    term: "Quantity",
    short: "How many shares you hold. Fractional shares are supported.",
    body: (
      <>
        <p>
          Stored as a high-precision decimal (<C>decimal.Decimal</C> on the
          server) so fractional shares like <C>32.7003</C> round-trip without
          loss. On the wire it is a JSON string, not a number, for the same
          reason.
        </p>
      </>
    ),
  },
  {
    id: "avg-cost",
    category: "positions",
    term: "Average cost (avg_cost_cents)",
    short: "Weighted average price paid per share, in integer cents.",
    body: (
      <>
        <p>
          The average price you paid for the shares you still hold. When you
          add to a position, it becomes the weighted average of the old
          average and the fill price of the new lot. Sells don't change it —
          they only reduce quantity and roll P&amp;L into{" "}
          <C>realized_cents</C>.
        </p>
        <p>
          The value is stored as an <b>integer number of cents</b>, which
          means fractional-cent precision is truncated on every update. For a
          position worth a few thousand dollars this usually costs a few
          cents of bookkeeping drift — see{" "}
          <a href="#rounding-drift">"Why is my cost basis off by a few
          cents?"</a>.
        </p>
      </>
    ),
    related: ["cost-basis", "rounding-drift"],
  },
  {
    id: "cost-basis",
    category: "positions",
    term: "Cost basis",
    short: "qty × avg_cost — the dollars tied up in a position.",
    body: (
      <>
        <p>
          Not a stored field — computed as <C>quantity * avg_cost_cents</C>.
          This is what you would get back in cash if you could sell the
          entire position at your average cost.
        </p>
        <p>
          Sum of cost basis across all positions + portfolio cash should
          approximately equal total deposits. Small drift is expected; see{" "}
          <a href="#rounding-drift">rounding drift</a>.
        </p>
      </>
    ),
    related: ["avg-cost", "market-value", "unrealized"],
  },
  {
    id: "realized",
    category: "positions",
    term: "Realized P&L",
    short: "Cumulative profit / loss from closed portions of a position.",
    body: (
      <>
        <p>
          Every sell crystallises P&amp;L equal to{" "}
          <C>(sale_price - avg_cost) * sold_qty</C> and adds it to{" "}
          <C>realized_cents</C> on the position row. It never decreases and
          is never reset — even if you fully close and re-open the position.
        </p>
      </>
    ),
    related: ["unrealized"],
  },
  {
    id: "unrealized",
    category: "positions",
    term: "Unrealized P&L",
    short: "Mark-to-market gain on open shares.",
    body: (
      <>
        <p>
          Not stored — derived at display time from a live quote:{" "}
          <C>(current_price - avg_cost) * qty</C>. If quoting is unavailable
          (e.g. market closed, offline) the dashboard falls back to cost
          basis.
        </p>
      </>
    ),
    related: ["realized", "market-value"],
  },
  {
    id: "market-value",
    category: "positions",
    term: "Market value",
    short: "qty × current quote — what the position is worth right now.",
    body: (
      <p>
        Different from cost basis: cost basis is what you <i>paid</i>, market
        value is what it's worth <i>now</i>. The delta between them is
        unrealized P&amp;L.
      </p>
    ),
    related: ["cost-basis", "unrealized"],
  },

  // ---------- Orders ----------
  {
    id: "order",
    category: "orders",
    term: "Order",
    short: "A single buy/sell instruction sent to the broker.",
    body: (
      <>
        <p>
          Every order row is an instruction plus its lifecycle: the requested
          quantity, the fill quantity, the fill price, and the current
          status. The engine emits one order per decision; a single decision
          is never split across multiple orders.
        </p>
      </>
    ),
    related: [
      "order-type",
      "order-status",
      "tif",
      "decision-link",
      "filled-avg",
    ],
  },
  {
    id: "order-type",
    category: "orders",
    term: "Order type (market / limit)",
    short: "How the broker chooses a fill price.",
    body: (
      <ul className="list-disc pl-5">
        <li>
          <b>market</b> — take the best available price immediately. No
          price guarantee; fast.
        </li>
        <li>
          <b>limit</b> — only fill at <C>limit_price</C> or better. May never
          fill.
        </li>
      </ul>
    ),
  },
  {
    id: "tif",
    category: "orders",
    term: "Time-in-force (TIF)",
    short: "How long an unfilled order stays live.",
    body: (
      <ul className="list-disc pl-5">
        <li>
          <b>day</b> — expires at market close.
        </li>
        <li>
          <b>gtc</b> — "good till cancelled"; stays open until you cancel.
        </li>
        <li>
          <b>ioc</b> — "immediate or cancel"; whatever doesn't fill at once
          is dropped.
        </li>
      </ul>
    ),
  },
  {
    id: "order-status",
    category: "orders",
    term: "Order status",
    short: "Where an order sits in its lifecycle.",
    body: (
      <ul className="list-disc pl-5">
        <li>
          <b>pending</b> — accepted locally, not yet sent to broker.
        </li>
        <li>
          <b>submitted</b> — broker acknowledged, not yet filled.
        </li>
        <li>
          <b>partially_filled</b> — some shares filled; remainder still live.
        </li>
        <li>
          <b>filled</b> — fully executed.
        </li>
        <li>
          <b>cancelled</b> — cancelled before completion.
        </li>
        <li>
          <b>rejected</b> — broker or risk engine refused.
        </li>
        <li>
          <b>expired</b> — TIF elapsed before fill.
        </li>
      </ul>
    ),
  },
  {
    id: "filled-avg",
    category: "orders",
    term: "Filled avg (filled_avg_cents)",
    short: "Volume-weighted average fill price for this order.",
    body: (
      <p>
        If the order filled across multiple lots, this is the VWAP in cents.
        For a fully filled market order,{" "}
        <C>filled_qty * filled_avg_cents</C> is the cash actually spent /
        received.
      </p>
    ),
  },
  {
    id: "decision-link",
    category: "orders",
    term: "Decision link (decision_id)",
    short: "Which strategy decision produced this order (if any).",
    body: (
      <p>
        Orders created by the engine carry a FK back to the decision that
        produced them. Manual orders created via the API have{" "}
        <C>decision_id = null</C>.
      </p>
    ),
  },

  // ---------- Decisions ----------
  {
    id: "decision",
    category: "decisions",
    term: "Decision",
    short: "A resolved buy / sell / hold recommendation per symbol.",
    body: (
      <>
        <p>
          The engine's <C>decide</C> step groups all active signals for each
          symbol and collapses them into a single <b>action</b> (<C>buy</C>,{" "}
          <C>sell</C>, <C>hold</C>) with a <b>score</b>, a <b>confidence</b>,
          and a <b>target_usd</b> notional. The LLM writes the human-readable{" "}
          <C>reasoning</C>.
        </p>
        <p>
          A decision is not an order — it's the <i>intent</i>. Execution is a
          separate step that can be auto or manual.
        </p>
      </>
    ),
    related: ["decision-action", "confidence", "target-usd", "reasoning"],
  },
  {
    id: "decision-action",
    category: "decisions",
    term: "Action",
    short: "buy / sell / hold.",
    body: (
      <p>
        The resolved direction. <C>hold</C> decisions are persisted for the
        audit trail but produce no order.
      </p>
    ),
  },
  {
    id: "score",
    category: "decisions",
    term: "Score",
    short: "Directional strength, −1..+1.",
    body: (
      <p>
        Signed aggregate of the supporting signals. Positive = bullish,
        negative = bearish, magnitude = conviction. The risk engine refuses
        to trade below a configurable minimum magnitude.
      </p>
    ),
    related: ["confidence"],
  },
  {
    id: "confidence",
    category: "decisions",
    term: "Confidence",
    short: "How sure the model is, 0..1.",
    body: (
      <p>
        Orthogonal to score: a decision can be bullish (score &gt; 0) but
        low-confidence (0.2), which the risk engine sizes down accordingly.
        Confidence multiplies into the dollar target.
      </p>
    ),
    related: ["score", "target-usd"],
  },
  {
    id: "target-usd",
    category: "decisions",
    term: "Target USD",
    short: "How many dollars the decision wants to commit.",
    body: (
      <p>
        The engine clamps this against <C>MAX_ORDER_USD</C>, the portfolio's{" "}
        <C>available</C> cash, and the daily order cap before producing an
        order.
      </p>
    ),
    related: ["available", "risk"],
  },
  {
    id: "reasoning",
    category: "decisions",
    term: "Reasoning",
    short: "LLM-written explanation of why this action was chosen.",
    body: (
      <p>
        Free-text rationale produced by the decision LLM. Useful when
        reviewing surprising trades. Pair it with the <b>LLM calls</b> page
        to see the exact prompt that generated it.
      </p>
    ),
  },

  // ---------- Signals ----------
  {
    id: "signal",
    category: "signals",
    term: "Signal",
    short: "A normalized, single-source trading hint.",
    body: (
      <>
        <p>
          Signals are produced by ingestion pipelines (news, politician
          trades, momentum) and consumed by the decision step. Each has a{" "}
          <b>kind</b>, a <b>symbol</b>, a <b>side</b>, a <b>score</b>, a{" "}
          <b>confidence</b>, and an <b>expires_at</b> timestamp after which
          it is ignored.
        </p>
      </>
    ),
    related: ["signal-kind", "score", "confidence"],
  },
  {
    id: "signal-kind",
    category: "signals",
    term: "Signal kind",
    short: "Where the signal came from.",
    body: (
      <ul className="list-disc pl-5">
        <li>
          <b>politician</b> — a disclosed congressional / senate trade
          matched to a ticker.
        </li>
        <li>
          <b>news</b> — an article's sentiment × relevance surfaced a
          tradeable ticker.
        </li>
        <li>
          <b>momentum</b> — computed from recent price / volume action
          against cached quotes.
        </li>
        <li>
          <b>manual</b> — inserted via API; used for scripted backfills or
          manual overrides.
        </li>
      </ul>
    ),
  },
  {
    id: "ref-id",
    category: "signals",
    term: "Ref ID",
    short: "Pointer back to the upstream source row.",
    body: (
      <p>
        For <C>news</C> signals this is the <C>news_items.id</C>; for{" "}
        <C>politician</C> it's the <C>politician_trades.id</C>. Click through
        to see the original evidence.
      </p>
    ),
  },

  // ---------- News ----------
  {
    id: "news",
    category: "news",
    term: "News item",
    short: "One article ingested from an upstream feed.",
    body: (
      <>
        <p>
          Sources are RSS / API feeds configured via env; each article is
          deduped by URL. The LLM annotates <b>sentiment</b> (−1..+1) and{" "}
          <b>relevance</b> (0..1) and extracts a comma-separated list of
          affected tickers.
        </p>
      </>
    ),
    related: ["sentiment", "relevance"],
  },
  {
    id: "sentiment",
    category: "news",
    term: "Sentiment",
    short: "−1 = very bearish, 0 = neutral, +1 = very bullish.",
    body: (
      <p>
        Produced by the news LLM. Combined with relevance to produce the
        magnitude of the resulting trading signal.
      </p>
    ),
  },
  {
    id: "relevance",
    category: "news",
    term: "Relevance",
    short: "How directly this article affects the listed tickers, 0..1.",
    body: (
      <p>
        Articles with relevance near 0 are filtered out — e.g. macro
        commentary that only tangentially mentions a ticker.
      </p>
    ),
  },

  // ---------- Politicians ----------
  {
    id: "politician",
    category: "politicians",
    term: "Politician",
    short: "A tracked member of Congress or the Senate.",
    body: (
      <p>
        Scraped from disclosure feeds. <C>track_weight</C> lets you bias the
        strategy toward better-performing members; it multiplies into the
        signal score for any trade they disclose.
      </p>
    ),
    related: ["track-weight", "politician-trade"],
  },
  {
    id: "track-weight",
    category: "politicians",
    term: "Track weight",
    short: "Per-politician score multiplier.",
    body: (
      <p>
        Default 1.0. Set to 0 to silence a politician; set above 1 to
        amplify their signals. Updated via{" "}
        <C>PATCH /v1/politicians/:id</C>.
      </p>
    ),
  },
  {
    id: "politician-trade",
    category: "politicians",
    term: "Politician trade",
    short: "A disclosed purchase or sale by a tracked politician.",
    body: (
      <>
        <p>
          Disclosures don't give an exact dollar amount — they give a{" "}
          <b>range</b> (<C>amount_min_usd</C>..<C>amount_max_usd</C>). The
          midpoint of the range is used for sizing the resulting signal.
        </p>
        <p>
          Disclosure timing matters: the SEC allows up to 45 days between
          execution and disclosure, so <C>traded_at</C> and{" "}
          <C>disclosed_at</C> often diverge significantly. The signal fires
          off <C>disclosed_at</C>.
        </p>
      </>
    ),
  },

  // ---------- LLM ----------
  {
    id: "llm-call",
    category: "llm",
    term: "LLM call",
    short: "One attempt at a chat-completion request.",
    body: (
      <>
        <p>
          Every call against your configured LLM endpoint is persisted —
          including fallback attempts if the primary model fails. Each row
          captures the full request, the full response, token counts,
          latency, and dollar cost.
        </p>
      </>
    ),
    related: ["operation", "attempt-index", "model-used", "tokens", "cost"],
  },
  {
    id: "operation",
    category: "llm",
    term: "Operation",
    short: "Which subsystem made the call.",
    body: (
      <p>
        Set via <C>llm.WithOperation(ctx, "news.analyse")</C> at the call
        site. Lets you slice spend by subsystem on the LLM usage page.
      </p>
    ),
  },
  {
    id: "attempt-index",
    category: "llm",
    term: "Attempt index",
    short: "0 for the primary model, 1+ for each fallback.",
    body: (
      <p>
        A single <C>Complete()</C> call that has to fall back across 3 models
        produces 3 rows, each with its own cost and outcome.
      </p>
    ),
  },
  {
    id: "model-used",
    category: "llm",
    term: "Model requested vs used",
    short: "What we asked for vs what the provider actually ran.",
    body: (
      <>
        <p>
          OpenRouter (and some local routers) occasionally rewrite the{" "}
          <C>model</C> field on the response — stripping the provider prefix
          or resolving a wildcard. We persist both so you can see the
          rewrite.
        </p>
        <p>
          Pricing is resolved against <C>model_used</C> first, falling back
          to <C>model_requested</C> when the rewritten name isn't in the
          price table.
        </p>
      </>
    ),
    related: ["cost"],
  },
  {
    id: "tokens",
    category: "llm",
    term: "Tokens",
    short: "prompt_tokens + completion_tokens = total_tokens.",
    body: (
      <p>
        Copied verbatim from the provider's <C>usage</C> block. Some local
        routers don't report usage — in that case the tokens are 0 and cost
        is 0, regardless of how big the prompt actually was.
      </p>
    ),
  },
  {
    id: "cost",
    category: "llm",
    term: "Cost",
    short: "USD billed by the provider for this attempt.",
    body: (
      <>
        <p>
          Computed at insert time from the configured{" "}
          <C>PriceTable</C>:{" "}
          <C>prompt_tokens × input_per_1M + completion_tokens × output_per_1M</C>
          . Override per-model via the <C>LLM_PRICING_JSON</C> env var.
        </p>
        <p>
          Stored as a decimal string to 8dp. Aggregates on the LLM usage
          page sum these exactly — no float round-trips.
        </p>
      </>
    ),
    related: ["tokens", "model-used"],
  },

  // ---------- Engine ----------
  {
    id: "engine",
    category: "engine",
    term: "Engine",
    short: "The ingest → signals → decide → execute loop.",
    body: (
      <>
        <p>
          The engine has two manual triggers you'll see in the UI:{" "}
          <C>POST /v1/engine/ingest</C> (pulls news / politician disclosures,
          produces signals) and <C>POST /v1/engine/decide</C> (reads active
          signals and produces decisions, optionally auto-executing them).
        </p>
      </>
    ),
    related: ["ingest", "decide"],
  },
  {
    id: "ingest",
    category: "engine",
    term: "Ingest",
    short: "Pull fresh news + politician trades and score them.",
    body: (
      <p>
        Deduped against existing rows; only new items become signals. This
        is the LLM-heavy step — expect tens of calls per ingest cycle.
      </p>
    ),
  },
  {
    id: "decide",
    category: "engine",
    term: "Decide",
    short: "Turn active signals into decisions (and optionally orders).",
    body: (
      <p>
        One decision per symbol with any live signal. <C>hold</C> decisions
        are logged but do not produce orders. If{" "}
        <C>REQUIRE_APPROVAL=false</C>, executable decisions are auto-placed
        against the portfolio; otherwise they sit waiting for manual
        <C>POST /v1/decisions/:id/execute</C>.
      </p>
    ),
    related: ["risk"],
  },
  {
    id: "risk",
    category: "engine",
    term: "Risk engine",
    short: "Guardrails that can veto an order before it leaves the host.",
    body: (
      <p>
        Enforces <C>MAX_ORDER_USD</C>, the daily order count cap, minimum
        decision confidence, and sufficient available cash. Vetos are written
        to the audit log with <C>action = risk_rejected</C>.
      </p>
    ),
  },

  // ---------- System ----------
  {
    id: "audit-log",
    category: "system",
    term: "Audit log",
    short: "Append-only history of state changes.",
    body: (
      <p>
        One row per <C>(entity, action)</C>. Used to reconstruct what
        happened after the fact — deposits, order submissions, risk vetoes,
        etc. Never truncated.
      </p>
    ),
  },
  {
    id: "broker",
    category: "system",
    term: "Broker",
    short: "The thing that actually places orders.",
    body: (
      <p>
        Configured per portfolio via <C>mode</C>. The broker is also the
        source of <b>live account</b> info on the Broker page — cash balance
        and positions as reported by the upstream, which may briefly differ
        from our DB during fills.
      </p>
    ),
    related: ["mode"],
  },
  {
    id: "quote",
    category: "system",
    term: "Quote",
    short: "A single price snapshot for a symbol.",
    body: (
      <p>
        Pulled on demand from the quote provider; not persisted. Used for
        mark-to-market and for sizing limit orders.
      </p>
    ),
  },
  {
    id: "healthz",
    category: "system",
    term: "Health / ready / version",
    short: "Standard Kubernetes-style probes.",
    body: (
      <p>
        <C>/healthz</C> = process alive. <C>/readyz</C> = DB reachable.{" "}
        <C>/version</C> = build SHA and tag. The Overview page surfaces all
        three.
      </p>
    ),
  },

  // ---------- Accounting & math ----------
  {
    id: "money",
    category: "math",
    term: "Money (cents vs dollars)",
    short: "Everything financial is integer cents on the wire.",
    body: (
      <>
        <p>
          The <C>domain.Money</C> type is an <C>int64</C> in cents. 100 cents
          = $1.00. JSON encodes it as a plain integer, and the frontend
          divides by 100 for display.
        </p>
        <p>
          Token / share quantities and LLM cost, which need sub-cent
          precision, are instead <C>decimal.Decimal</C> and serialized as
          strings to avoid JSON number drift.
        </p>
      </>
    ),
    related: ["cash", "cost-basis", "rounding-drift"],
  },
  {
    id: "rounding-drift",
    category: "math",
    term: "Why is my cost basis off by a few cents?",
    short: "avg_cost_cents is integer; reconstructing notional loses sub-cent precision.",
    body: (
      <>
        <p>
          When you buy <C>32.7003</C> shares for $2,499.68, the true average
          price per share is <C>$2,499.68 / 32.7003 ≈ $76.43908</C>. We store
          that as <C>avg_cost_cents = 7644</C> (i.e. $76.44, rounded).
          Multiplying back: <C>32.7003 × $76.44 = $2,499.61</C> — 7¢ less
          than you actually paid.
        </p>
        <p>
          Across 6 positions you'll typically accumulate a quarter to a
          dollar of drift vs your filled-order notional. The drift lives
          entirely in the <b>cost basis display</b>; the actual cash balance
          is computed from filled-order amounts (not avg_cost) and stays
          penny-accurate.
        </p>
        <p>
          Sanity check: <b>cash + Σ filled-buy notional ≈ total deposits</b>.
          That's the ledger invariant; cost basis is a convenience
          reconstruction on top of it.
        </p>
      </>
    ),
    related: ["avg-cost", "cost-basis", "money"],
  },
  {
    id: "conservation",
    category: "math",
    term: "Money conservation",
    short: "Cash should equal deposits minus what you spent.",
    body: (
      <p>
        At any point:{" "}
        <C>cash + Σ(filled_buy_notional) - Σ(filled_sell_notional) + realized ≈ total_deposits - total_withdrawals</C>
        . The equality is exact for filled orders; drift only appears when
        reconstructing from <C>avg_cost × qty</C>.
      </p>
    ),
    related: ["rounding-drift"],
  },
]

function flatten(n: React.ReactNode): string {
  if (n == null || typeof n === "boolean") return ""
  if (typeof n === "string" || typeof n === "number") return String(n)
  if (Array.isArray(n)) return n.map(flatten).join(" ")
  if (typeof n === "object" && "props" in n) {
    return flatten((n as { props: { children?: React.ReactNode } }).props.children)
  }
  return ""
}

export function FaqRoute() {
  const [query, setQuery] = useState("")
  const [active, setActive] = useState<CategoryId | "all">("all")

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return faqs.filter((f) => {
      if (active !== "all" && f.category !== active) return false
      if (!q) return true
      const hay = [
        f.term,
        f.short,
        flatten(f.body),
        ...(f.keywords ?? []),
      ]
        .join(" ")
        .toLowerCase()
      return hay.includes(q)
    })
  }, [query, active])

  const byCat = useMemo(() => {
    const map = new Map<CategoryId, FaqEntry[]>()
    for (const f of filtered) {
      const arr = map.get(f.category as CategoryId) ?? []
      arr.push(f)
      map.set(f.category as CategoryId, arr)
    }
    return map
  }, [filtered])

  const termIndex = useMemo(() => {
    const m = new Map<string, FaqEntry>()
    for (const f of faqs) m.set(f.id, f)
    return m
  }, [])

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="FAQ & glossary"
        description="Every concept in the trader app, plus a few accounting gotchas."
      />

      <Card>
        <CardHeader>
          <CardTitle>Search</CardTitle>
          <CardDescription>
            Filter by keyword. Click a category to narrow further.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <Input
            placeholder="Search terms, e.g. 'reserved', 'confidence', 'tokens'"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="flex flex-wrap gap-1.5">
            <CategoryChip
              active={active === "all"}
              onClick={() => setActive("all")}
            >
              All ({faqs.length})
            </CategoryChip>
            {categories.map((c) => {
              const n = faqs.filter((f) => f.category === c.id).length
              return (
                <CategoryChip
                  key={c.id}
                  active={active === c.id}
                  onClick={() => setActive(c.id)}
                >
                  {c.label} ({n})
                </CategoryChip>
              )
            })}
          </div>
        </CardContent>
      </Card>

      {filtered.length === 0 ? (
        <Card>
          <CardContent className="text-muted-foreground py-10 text-center text-sm">
            No entries match <b>"{query}"</b>. Try a broader term.
          </CardContent>
        </Card>
      ) : (
        categories
          .filter((c) => byCat.has(c.id))
          .map((c) => {
            const entries = byCat.get(c.id)!
            return (
              <Card key={c.id}>
                <CardHeader>
                  <CardTitle>{c.label}</CardTitle>
                  <CardDescription>
                    {entries.length} entr{entries.length === 1 ? "y" : "ies"}
                  </CardDescription>
                </CardHeader>
                <CardContent className="pt-0">
                  <Accordion type="multiple" className="w-full">
                    {entries.map((f) => (
                      <AccordionItem key={f.id} value={f.id} id={f.id}>
                        <AccordionTrigger className="gap-3">
                          <div className="flex min-w-0 flex-1 flex-col items-start text-left">
                            <span className="font-medium">{f.term}</span>
                            <span className="text-muted-foreground text-[12.5px] font-normal">
                              {f.short}
                            </span>
                          </div>
                        </AccordionTrigger>
                        <AccordionContent className="text-muted-foreground flex flex-col gap-3 text-sm leading-relaxed [&>p]:text-foreground/90">
                          {f.body}
                          {f.related && f.related.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1.5 pt-1">
                              <span className="text-muted-foreground text-[11px] uppercase tracking-wider">
                                See also
                              </span>
                              {f.related.map((rid) => {
                                const r = termIndex.get(rid)
                                if (!r) return null
                                return (
                                  <a
                                    key={rid}
                                    href={`#${rid}`}
                                    onClick={() => setActive("all")}
                                    className="hover:text-foreground no-underline"
                                  >
                                    <Badge variant="outline">{r.term}</Badge>
                                  </a>
                                )
                              })}
                            </div>
                          )}
                        </AccordionContent>
                      </AccordionItem>
                    ))}
                  </Accordion>
                </CardContent>
              </Card>
            )
          })
      )}

      <Card>
        <CardHeader>
          <CardTitle>API endpoints quick reference</CardTitle>
          <CardDescription>
            Every route exposed by the Go backend, with the matching
            dashboard page.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable data={endpointRows} columns={endpointColumns} />
        </CardContent>
      </Card>
    </div>
  )
}

function CategoryChip({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-md border px-2 py-0.5 text-xs transition-colors",
        active
          ? "bg-primary text-primary-foreground border-primary"
          : "bg-background hover:bg-muted",
      )}
    >
      {children}
    </button>
  )
}

// ---------------------------------------------------------------------------
// Endpoint quick-reference table
// ---------------------------------------------------------------------------

interface EndpointRow {
  method: string
  path: string
  dashboard: string
  description: string
}

const endpointRows: EndpointRow[] = [
  { method: "GET", path: "/healthz", dashboard: "Overview", description: "Process liveness probe." },
  { method: "GET", path: "/readyz", dashboard: "Overview", description: "DB-ready readiness probe." },
  { method: "GET", path: "/version", dashboard: "Overview", description: "Build SHA / tag." },
  { method: "GET", path: "/v1/portfolios", dashboard: "Portfolios", description: "List all portfolios." },
  { method: "GET", path: "/v1/portfolios/:id", dashboard: "Portfolio detail", description: "One portfolio with positions." },
  { method: "POST", path: "/v1/portfolios/:id/deposit", dashboard: "Portfolio detail", description: "Credit cash." },
  { method: "POST", path: "/v1/portfolios/:id/withdraw", dashboard: "Portfolio detail", description: "Debit cash." },
  { method: "GET", path: "/v1/positions", dashboard: "Positions", description: "Open positions across portfolios." },
  { method: "GET", path: "/v1/orders", dashboard: "Orders", description: "Paginated order history." },
  { method: "POST", path: "/v1/orders", dashboard: "Orders", description: "Manually submit an order." },
  { method: "GET", path: "/v1/decisions", dashboard: "Decisions", description: "Most recent strategy decisions." },
  { method: "POST", path: "/v1/decisions/:id/execute", dashboard: "Decisions", description: "Turn a pending decision into an order." },
  { method: "GET", path: "/v1/signals", dashboard: "Signals", description: "Active signals." },
  { method: "GET", path: "/v1/news", dashboard: "News", description: "Ingested articles." },
  { method: "GET", path: "/v1/politicians", dashboard: "Politicians", description: "Tracked members." },
  { method: "GET", path: "/v1/trades", dashboard: "Politician trades", description: "Disclosed transactions." },
  { method: "GET", path: "/v1/audit", dashboard: "Audit log", description: "State-change history." },
  { method: "GET", path: "/v1/llm/calls", dashboard: "LLM calls", description: "Per-attempt LLM invocations." },
  { method: "GET", path: "/v1/llm/usage", dashboard: "LLM usage", description: "Cost/token rollup, groupable by day/model/op." },
  { method: "POST", path: "/v1/engine/ingest", dashboard: "Engine", description: "Run ingest step." },
  { method: "POST", path: "/v1/engine/decide", dashboard: "Engine", description: "Run decide step." },
  { method: "GET", path: "/v1/engine/status", dashboard: "Engine", description: "Engine config + last run." },
  { method: "GET", path: "/v1/broker/account", dashboard: "Broker", description: "Live broker cash + equity." },
  { method: "GET", path: "/v1/broker/positions", dashboard: "Broker", description: "Positions as known to the broker." },
  { method: "GET", path: "/v1/quotes/:symbol", dashboard: "Quote lookup", description: "Latest quote for one ticker." },
]

const endpointColumns: ColumnDef<EndpointRow>[] = [
  {
    accessorKey: "method",
    header: "Method",
    cell: ({ row }) => (
      <Badge
        variant="outline"
        className={cn(
          "font-mono text-[10px]",
          row.original.method === "GET" && "border-blue-500/40 text-blue-600",
          row.original.method === "POST" &&
            "border-emerald-500/40 text-emerald-600",
        )}
      >
        {row.original.method}
      </Badge>
    ),
  },
  {
    accessorKey: "path",
    header: "Path",
    cell: ({ row }) => <code className="text-[12.5px]">{row.original.path}</code>,
  },
  { accessorKey: "dashboard", header: "Dashboard page" },
  { accessorKey: "description", header: "What it returns" },
]
