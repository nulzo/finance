// Types mirror the Go domain types in /internal/domain/types.go.
//
// Monetary values on the wire:
//   - *_cents fields are int64 (stringified JSON numbers safe under 2^53).
//   - shopspring/decimal marshals as a JSON string (e.g. "0.003214"). We
//     therefore type every decimal on the wire as `string` and rely on
//     the formatter to convert when needed.

export type Side = "buy" | "sell"

export type OrderStatus =
  | "pending"
  | "submitted"
  | "filled"
  | "partially_filled"
  | "cancelled"
  | "rejected"
  | "expired"

export type OrderType = "market" | "limit"
export type TimeInForce = "day" | "gtc" | "ioc"

export type SignalKind =
  | "politician"
  | "news"
  | "momentum"
  | "insider"
  | "social"
  | "manual"
export type DecisionAction = "buy" | "sell" | "hold"

/** Decimal-as-string on the wire. */
export type Dec = string

export interface Order {
  id: string
  portfolio_id: string
  symbol: string
  side: Side
  type: OrderType
  time_in_force: TimeInForce
  quantity: Dec
  limit_price?: Dec | null
  filled_qty: Dec
  filled_avg_cents: number
  status: OrderStatus
  broker_id: string
  reason: string
  decision_id?: string | null
  created_at: string
  updated_at: string
  submitted_at?: string | null
  filled_at?: string | null
}

export interface Position {
  id: string
  portfolio_id: string
  symbol: string
  quantity: Dec
  avg_cost_cents: number
  realized_cents: number
  created_at: string
  updated_at: string
}

export interface Portfolio {
  id: string
  name: string
  mode: string
  cash_cents: number
  reserved_cents: number
  created_at: string
  updated_at: string
}

export interface PortfolioDetail {
  portfolio: Portfolio
  positions: Position[]
}

export interface Politician {
  id: string
  name: string
  chamber: string
  party: string
  state: string
  track_weight: number
  created_at: string
  updated_at: string
}

export interface PoliticianTrade {
  id: string
  politician_id?: string | null
  politician_name: string
  chamber: string
  symbol: string
  asset_name: string
  side: Side
  amount_min_usd: number
  amount_max_usd: number
  traded_at: string
  disclosed_at: string
  source: string
  raw_hash: string
  created_at: string
}

export interface NewsItem {
  id: string
  source: string
  url: string
  title: string
  summary: string
  symbols: string
  sentiment: number
  relevance: number
  pub_at: string
  created_at: string
}

export interface Signal {
  id: string
  kind: SignalKind
  symbol: string
  side: Side
  score: number
  confidence: number
  reason: string
  ref_id: string
  expires_at: string
  created_at: string
}

export interface Decision {
  id: string
  portfolio_id: string
  symbol: string
  action: DecisionAction
  score: number
  confidence: number
  target_usd: Dec
  reasoning: string
  model_used: string
  signal_ids: string
  executed_id?: string | null
  created_at: string
}

export interface LLMCall {
  id: string
  created_at: string
  operation: string
  attempt_index: number
  model_requested: string
  model_used: string
  outcome: string
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  prompt_cost_usd: Dec
  completion_cost_usd: Dec
  total_cost_usd: Dec
  latency_ms: number
  request_bytes: number
  response_bytes: number
  request_messages: string
  response_text: string
  error_message?: string
  trace_id?: string
  span_id?: string
  temperature: number
  max_tokens: number
  json_mode: boolean
}

export interface UsageTotals {
  calls: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  total_cost_usd: Dec
  avg_latency_ms: number
  error_calls: number
}

export interface UsageRow {
  bucket: string
  calls: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  total_cost_usd: Dec
  avg_latency_ms: number
  error_calls: number
}

export interface UsageResponse {
  since: string
  group_by: string
  totals: UsageTotals
  buckets: UsageRow[]
}

export interface Quote {
  symbol: string
  price: Dec
  bid?: Dec
  ask?: Dec
  timestamp: string
}

export interface EngineStatus {
  enabled: boolean
}

export interface BrokerAccount {
  [k: string]: unknown
}

export interface BrokerPosition {
  [k: string]: unknown
}

/** Generic audit row returned by /v1/audit — the backend ships a loose
 *  structure so we accept any record shape here and render opportunistically. */
export interface AuditRow {
  id?: string
  entity?: string
  entity_id?: string
  action?: string
  detail?: string
  created_at?: string
  [k: string]: unknown
}

export interface Cooldown {
  portfolio_id: string
  symbol: string
  until_ts: string
  reason: string
  updated_at: string
}

/** Source of a rejection — who said "no". */
export type RejectionSource = "risk" | "broker" | "engine"

/** A single rejected trade attempt, produced by the risk engine, the
 *  broker, or the trading engine's own short-circuits. */
export interface Rejection {
  id: string
  portfolio_id: string
  symbol: string
  decision_id?: string | null
  side: Side | ""
  source: RejectionSource
  reason: string
  target_usd: Dec
  created_at: string
}

/** Daily realized P&L row. `realized_cents` is int64 cents. */
export interface DailyPnL {
  day: string
  realized_cents: number
  event_count: number
}

/** Per-position valuation returned by the equity endpoints. A
 *  position with `priced=false` had no live quote and is marked
 *  at cost — the UI surfaces that so "flat" rows aren't mistaken
 *  for "stock didn't move". */
export interface PositionPnL {
  symbol: string
  quantity: Dec
  avg_cost_cents: number
  mark_cents: number
  cost_basis_cents: number
  market_value_cents: number
  realized_cents: number
  unrealized_cents: number
  unrealized_pct: number
  quote_at?: string
  priced: boolean
}

/** Live portfolio valuation: cash, open positions (marked to
 *  market), realised and unrealised P&L, and per-position
 *  breakdown. Mirrors `equity.Valuation` on the server. */
export interface EquityValuation {
  portfolio_id: string
  taken_at: string
  cash_cents: number
  positions_cost: number
  positions_mtm: number
  realized_cents: number
  unrealized_cents: number
  equity_cents: number
  open_positions: number
  priced_positions: number
  positions: PositionPnL[]
}

/** One persisted row from the equity_snapshots table — same
 *  shape as EquityValuation but without the per-position
 *  breakdown (too expensive to persist per snapshot). */
export interface EquitySnapshot {
  id: string
  portfolio_id: string
  taken_at: string
  cash_cents: number
  positions_cost: number
  positions_mtm: number
  realized_cents: number
  unrealized_cents: number
  equity_cents: number
  open_positions: number
  priced_positions: number
}

/** Header roll-up powering the Overview / Analytics stat cards.
 *  All numbers are integer cents; `day_change_available` is false
 *  when we don't yet have ≥2 snapshots for today. */
export interface AnalyticsSummary {
  portfolio_id: string
  cash_cents: number
  positions_cost: number
  positions_mtm: number
  realized_cents: number
  unrealized_cents: number
  equity_cents: number
  open_positions: number
  priced_positions: number
  realized_today_cents: number
  realized_week_cents: number
  realized_month_cents: number
  day_change_cents: number
  day_change_available: boolean
}

/** Active risk-engine limits. Monetary values arrive as decimal
 *  strings — see the Dec type above. */
export interface RiskLimits {
  max_order_usd: Dec
  max_position_usd: Dec
  max_daily_loss_usd: Dec
  max_daily_orders: number
  max_symbol_exposure: Dec
  max_concurrent_positions: number
  blacklist: string[]
  require_approval: boolean
}

export type RiskLimitsPatch = Partial<{
  max_order_usd: Dec
  max_position_usd: Dec
  max_daily_loss_usd: Dec
  max_daily_orders: number
  max_symbol_exposure: Dec
  max_concurrent_positions: number
  blacklist: string[]
  require_approval: boolean
}>

/** --------------------------------------------------------------- Wave 4 */

/** SEC Form 4 insider transaction. Dollar fields use int64 USD so
 *  they display with a simple `num()` formatter rather than decimal
 *  string parsing. */
export interface InsiderTrade {
  id: string
  symbol: string
  insider_name: string
  insider_title: string
  side: Side
  shares: number
  price_cents: number
  value_usd: number
  transacted_at: string
  filed_at: string
  source: string
  raw_hash: string
  created_at: string
}

/** Rolled-up social-media mention bucket (WSB, Twitter). Bucket
 *  granularity is whatever Quiver shipped (usually hourly for WSB,
 *  daily for Twitter). */
export interface SocialPost {
  id: string
  symbol: string
  platform: string
  mentions: number
  sentiment: number
  followers: number
  bucket_at: string
  source: string
  raw_hash: string
  created_at: string
}

/** LDA (Lobbying Disclosure Act) filing attached to a ticker. */
export interface LobbyingEvent {
  id: string
  symbol: string
  client: string
  registrant: string
  issue: string
  amount_usd: number
  filed_at: string
  period: string
  source: string
  raw_hash: string
  created_at: string
}

/** Federal contract award. */
export interface GovContract {
  id: string
  symbol: string
  agency: string
  description: string
  amount_usd: number
  awarded_at: string
  source: string
  raw_hash: string
  created_at: string
}

/** Daily off-exchange short-volume snapshot. */
export interface ShortVolume {
  symbol: string
  day: string
  short_volume: number
  total_volume: number
  short_exempt_volume: number
  short_ratio: number
  source: string
  created_at: string
}

export interface VersionInfo {
  version: string
  commit: string
}

export interface HealthInfo {
  status: string
  time?: string
  error?: string
}

export interface ApiError {
  error: string
}
