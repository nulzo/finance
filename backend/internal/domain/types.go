// Package domain defines the core entities and value objects used throughout
// the trader application. Packages outside of domain depend on these types
// but not vice versa.
package domain

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Side represents a buy or sell side.
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Valid returns true if the side is a known value.
func (s Side) Valid() bool { return s == SideBuy || s == SideSell }

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusPartial   OrderStatus = "partially_filled"
	OrderStatusCancelled OrderStatus = "cancelled"
	OrderStatusRejected  OrderStatus = "rejected"
	OrderStatusExpired   OrderStatus = "expired"
)

// OrderType describes an order pricing style.
type OrderType string

const (
	OrderTypeMarket OrderType = "market"
	OrderTypeLimit  OrderType = "limit"
)

// TimeInForce values.
type TimeInForce string

const (
	TIFDay TimeInForce = "day"
	TIFGTC TimeInForce = "gtc"
	TIFIOC TimeInForce = "ioc"
)

// Order is a trade instruction against the broker.
type Order struct {
	ID            string          `db:"id" json:"id"`
	PortfolioID   string          `db:"portfolio_id" json:"portfolio_id"`
	Symbol        string          `db:"symbol" json:"symbol"`
	Side          Side            `db:"side" json:"side"`
	Type          OrderType       `db:"type" json:"type"`
	TimeInForce   TimeInForce     `db:"time_in_force" json:"time_in_force"`
	Quantity      decimal.Decimal `db:"quantity" json:"quantity"`
	LimitPrice    *decimal.Decimal `db:"limit_price" json:"limit_price,omitempty"`
	FilledQty     decimal.Decimal `db:"filled_qty" json:"filled_qty"`
	FilledAvgCents Money          `db:"filled_avg_cents" json:"filled_avg_cents"`
	Status        OrderStatus     `db:"status" json:"status"`
	BrokerID      string          `db:"broker_id" json:"broker_id"`
	Reason        string          `db:"reason" json:"reason"`
	DecisionID    *string         `db:"decision_id" json:"decision_id,omitempty"`
	CreatedAt     time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at" json:"updated_at"`
	SubmittedAt   *time.Time      `db:"submitted_at" json:"submitted_at,omitempty"`
	FilledAt      *time.Time      `db:"filled_at" json:"filled_at,omitempty"`
}

// Position represents an open position within a portfolio.
type Position struct {
	ID             string          `db:"id" json:"id"`
	PortfolioID    string          `db:"portfolio_id" json:"portfolio_id"`
	Symbol         string          `db:"symbol" json:"symbol"`
	Quantity       decimal.Decimal `db:"quantity" json:"quantity"`
	AvgCostCents   Money           `db:"avg_cost_cents" json:"avg_cost_cents"`
	RealizedCents  Money           `db:"realized_cents" json:"realized_cents"`
	CreatedAt      time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at" json:"updated_at"`
}

// Portfolio owns a wallet balance and positions.
type Portfolio struct {
	ID            string    `db:"id" json:"id"`
	Name          string    `db:"name" json:"name"`
	Mode          string    `db:"mode" json:"mode"`
	CashCents     Money     `db:"cash_cents" json:"cash_cents"`
	ReservedCents Money     `db:"reserved_cents" json:"reserved_cents"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at"`
}

// AvailableCents is wallet minus reserved.
func (p Portfolio) AvailableCents() Money { return p.CashCents - p.ReservedCents }

// Politician represents a tracked congressional or senate member.
type Politician struct {
	ID          string    `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Chamber     string    `db:"chamber" json:"chamber"` // house|senate
	Party       string    `db:"party" json:"party"`
	State       string    `db:"state" json:"state"`
	TrackWeight float64   `db:"track_weight" json:"track_weight"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// PoliticianTrade is a disclosed transaction by a tracked politician.
//
// PoliticianID is a nullable FK into politicians(id). Most feeds (Quiver,
// Lambda Finance, CapitolTrades) don't give us a stable upstream ID at
// trade-ingest time, so the column is left NULL and the FK is resolved
// later (or never). Using a pointer here matches the convention used by
// other nullable FKs in this package (Order.DecisionID, Decision.ExecutedID).
type PoliticianTrade struct {
	ID             string    `db:"id" json:"id"`
	PoliticianID   *string   `db:"politician_id" json:"politician_id,omitempty"`
	PoliticianName string    `db:"politician_name" json:"politician_name"`
	Chamber       string     `db:"chamber" json:"chamber"`
	Symbol        string     `db:"symbol" json:"symbol"`
	AssetName     string     `db:"asset_name" json:"asset_name"`
	Side          Side       `db:"side" json:"side"`
	AmountMinUSD  int64      `db:"amount_min_usd" json:"amount_min_usd"`
	AmountMaxUSD  int64      `db:"amount_max_usd" json:"amount_max_usd"`
	TradedAt      time.Time  `db:"traded_at" json:"traded_at"`
	DisclosedAt   time.Time  `db:"disclosed_at" json:"disclosed_at"`
	Source        string     `db:"source" json:"source"`
	RawHash       string     `db:"raw_hash" json:"raw_hash"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
}

// NewsItem represents a piece of market-relevant news.
type NewsItem struct {
	ID        string    `db:"id" json:"id"`
	Source    string    `db:"source" json:"source"`
	URL       string    `db:"url" json:"url"`
	Title     string    `db:"title" json:"title"`
	Summary   string    `db:"summary" json:"summary"`
	Symbols   string    `db:"symbols" json:"symbols"` // comma-separated tickers
	Sentiment float64   `db:"sentiment" json:"sentiment"`
	Relevance float64   `db:"relevance" json:"relevance"`
	PubAt     time.Time `db:"pub_at" json:"pub_at"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// SymbolList returns tickers as a slice.
func (n NewsItem) SymbolList() []string {
	if n.Symbols == "" {
		return nil
	}
	parts := strings.Split(n.Symbols, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(strings.ToUpper(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SignalKind enumerates supported signal sources.
type SignalKind string

const (
	SignalKindPolitician SignalKind = "politician"
	SignalKindNews       SignalKind = "news"
	SignalKindMomentum   SignalKind = "momentum"
	SignalKindInsider    SignalKind = "insider"
	SignalKindSocial     SignalKind = "social"
	SignalKindManual     SignalKind = "manual"
)

// Signal is a normalized trading signal with confidence and reasoning.
type Signal struct {
	ID         string     `db:"id" json:"id"`
	Kind       SignalKind `db:"kind" json:"kind"`
	Symbol     string     `db:"symbol" json:"symbol"`
	Side       Side       `db:"side" json:"side"`
	Score      float64    `db:"score" json:"score"`      // -1..1
	Confidence float64    `db:"confidence" json:"confidence"` // 0..1
	Reason     string     `db:"reason" json:"reason"`
	RefID      string     `db:"ref_id" json:"ref_id"`
	ExpiresAt  time.Time  `db:"expires_at" json:"expires_at"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`
}

// DecisionAction enumerates resolved strategy actions.
type DecisionAction string

const (
	DecisionActionBuy  DecisionAction = "buy"
	DecisionActionSell DecisionAction = "sell"
	DecisionActionHold DecisionAction = "hold"
)

// Decision captures the reasoning behind a trade attempt.
type Decision struct {
	ID          string          `db:"id" json:"id"`
	PortfolioID string          `db:"portfolio_id" json:"portfolio_id"`
	Symbol      string          `db:"symbol" json:"symbol"`
	Action      DecisionAction  `db:"action" json:"action"`
	Score       float64         `db:"score" json:"score"`
	Confidence  float64         `db:"confidence" json:"confidence"`
	TargetUSD   decimal.Decimal `db:"target_usd" json:"target_usd"`
	Reasoning   string          `db:"reasoning" json:"reasoning"`
	ModelUsed   string          `db:"model_used" json:"model_used"`
	SignalIDs   string          `db:"signal_ids" json:"signal_ids"` // json array
	ExecutedID  *string         `db:"executed_id" json:"executed_id,omitempty"`
	CreatedAt   time.Time       `db:"created_at" json:"created_at"`
}

// LLMCall records a single LLM inference attempt — including fallbacks.
// Every attempt (primary model + any fallback attempts triggered by
// errors) becomes its own row so cost accounting matches what was
// actually paid for.
//
// Cost fields are stored as high-precision decimal strings. Token-count
// fields are zero when the upstream API did not return usage (rare, but
// happens on transport errors and some local routers).
//
// The full request/response payloads are persisted so you can audit
// exactly what was sent and what came back. For privacy, consider
// truncating the prompt upstream before it enters this record.
type LLMCall struct {
	ID                string          `db:"id" json:"id"`
	CreatedAt         time.Time       `db:"created_at" json:"created_at"`
	// Operation is a short subsystem tag (e.g. "news.analyse",
	// "engine.decide") derived from the call-site via
	// llm.WithOperation(ctx, op).
	Operation         string          `db:"operation" json:"operation"`
	// AttemptIndex starts at 0 for the primary model and increments
	// with each fallback tried within a single Complete() call.
	AttemptIndex      int             `db:"attempt_index" json:"attempt_index"`
	ModelRequested    string          `db:"model_requested" json:"model_requested"`
	ModelUsed         string          `db:"model_used" json:"model_used"`
	// Outcome is one of: "ok", "http_<status>", "transport_error",
	// "decode_error", "empty_choices", "marshal_error", "request_error".
	Outcome           string          `db:"outcome" json:"outcome"`
	PromptTokens      int             `db:"prompt_tokens" json:"prompt_tokens"`
	CompletionTokens  int             `db:"completion_tokens" json:"completion_tokens"`
	TotalTokens       int             `db:"total_tokens" json:"total_tokens"`
	PromptCostUSD     decimal.Decimal `db:"prompt_cost_usd" json:"prompt_cost_usd"`
	CompletionCostUSD decimal.Decimal `db:"completion_cost_usd" json:"completion_cost_usd"`
	TotalCostUSD      decimal.Decimal `db:"total_cost_usd" json:"total_cost_usd"`
	LatencyMS         int64           `db:"latency_ms" json:"latency_ms"`
	RequestBytes      int             `db:"request_bytes" json:"request_bytes"`
	ResponseBytes     int             `db:"response_bytes" json:"response_bytes"`
	RequestMessages   string          `db:"request_messages" json:"request_messages"`
	ResponseText      string          `db:"response_text" json:"response_text"`
	ErrorMessage      string          `db:"error_message" json:"error_message,omitempty"`
	TraceID           string          `db:"trace_id" json:"trace_id,omitempty"`
	SpanID            string          `db:"span_id" json:"span_id,omitempty"`
	Temperature       float64         `db:"temperature" json:"temperature"`
	MaxTokens         int             `db:"max_tokens" json:"max_tokens"`
	JSONMode          bool            `db:"json_mode" json:"json_mode"`
}

// Quote is a single price snapshot.
type Quote struct {
	Symbol    string          `json:"symbol"`
	Price     decimal.Decimal `json:"price"`
	Bid       decimal.Decimal `json:"bid,omitempty"`
	Ask       decimal.Decimal `json:"ask,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// InsiderTrade is an SEC Form 4 filing normalised across providers.
// Open-market buys by insiders (especially clusters across multiple
// officers) are the highest-conviction variant of "smart money" data
// since the filer is personally on the hook for 10b-5 / Section 16.
type InsiderTrade struct {
	ID            string    `db:"id" json:"id"`
	Symbol        string    `db:"symbol" json:"symbol"`
	InsiderName   string    `db:"insider_name" json:"insider_name"`
	InsiderTitle  string    `db:"insider_title" json:"insider_title"`
	Side          Side      `db:"side" json:"side"`
	Shares        int64     `db:"shares" json:"shares"`
	PriceCents    Money     `db:"price_cents" json:"price_cents"`
	ValueUSD      int64     `db:"value_usd" json:"value_usd"`
	TransactedAt  time.Time `db:"transacted_at" json:"transacted_at"`
	FiledAt       time.Time `db:"filed_at" json:"filed_at"`
	Source        string    `db:"source" json:"source"`
	RawHash       string    `db:"raw_hash" json:"raw_hash"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
}

// SocialPost is a rolled-up social-media mention bucket. Quiver
// aggregates the WallStreetBets firehose into a daily rollup; we
// store per-bucket rows so intraday spikes are visible.
type SocialPost struct {
	ID        string    `db:"id" json:"id"`
	Symbol    string    `db:"symbol" json:"symbol"`
	Platform  string    `db:"platform" json:"platform"` // wsb | twitter | ...
	Mentions  int64     `db:"mentions" json:"mentions"`
	Sentiment float64   `db:"sentiment" json:"sentiment"` // -1..1
	Followers int64     `db:"followers" json:"followers"`
	BucketAt  time.Time `db:"bucket_at" json:"bucket_at"`
	Source    string    `db:"source" json:"source"`
	RawHash   string    `db:"raw_hash" json:"raw_hash"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// LobbyingEvent is an LDA (Lobbying Disclosure Act) filing attached
// to a public company. Used purely as LLM decision context.
type LobbyingEvent struct {
	ID         string    `db:"id" json:"id"`
	Symbol     string    `db:"symbol" json:"symbol"`
	Client     string    `db:"client" json:"client"`
	Registrant string    `db:"registrant" json:"registrant"`
	Issue      string    `db:"issue" json:"issue"`
	AmountUSD  int64     `db:"amount_usd" json:"amount_usd"`
	FiledAt    time.Time `db:"filed_at" json:"filed_at"`
	Period     string    `db:"period" json:"period"`
	Source     string    `db:"source" json:"source"`
	RawHash    string    `db:"raw_hash" json:"raw_hash"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

// GovContract is a federal contract award tied to a ticker.
type GovContract struct {
	ID          string    `db:"id" json:"id"`
	Symbol      string    `db:"symbol" json:"symbol"`
	Agency      string    `db:"agency" json:"agency"`
	Description string    `db:"description" json:"description"`
	AmountUSD   int64     `db:"amount_usd" json:"amount_usd"`
	AwardedAt   time.Time `db:"awarded_at" json:"awarded_at"`
	Source      string    `db:"source" json:"source"`
	RawHash     string    `db:"raw_hash" json:"raw_hash"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
}

// ShortVolume is a daily off-exchange (FINRA ATS) short-volume
// snapshot. Feeds momentum analytics via TechnicalContext.
type ShortVolume struct {
	Symbol            string    `db:"symbol" json:"symbol"`
	Day               time.Time `db:"day" json:"day"`
	ShortVolume       int64     `db:"short_volume" json:"short_volume"`
	TotalVolume       int64     `db:"total_volume" json:"total_volume"`
	ShortExemptVolume int64     `db:"short_exempt_volume" json:"short_exempt_volume"`
	ShortRatio        float64   `db:"short_ratio" json:"short_ratio"`
	Source            string    `db:"source" json:"source"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
}
