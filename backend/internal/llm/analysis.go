package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/nulzo/trader/internal/domain"
)

// NewsAnalysis is the structured output we request from the LLM when
// analysing a news item.
type NewsAnalysis struct {
	Sentiment float64  `json:"sentiment"` // -1..1
	Relevance float64  `json:"relevance"` // 0..1
	Symbols   []string `json:"symbols"`
	Summary   string   `json:"summary"`
}

// TradeRationale is the structured output used when deciding whether
// to open/adjust a position.
type TradeRationale struct {
	Action     string  `json:"action"` // buy|sell|hold
	Confidence float64 `json:"confidence"`
	TargetUSD  float64 `json:"target_usd"`
	Reasoning  string  `json:"reasoning"`
}

// AnalyseNews summarises a news item and classifies sentiment.
// It uses the web search extension to gather deeper context about the company
// and the news event, providing layered grounding beyond just the title/summary.
func (c *Client) AnalyseNews(ctx context.Context, title, body string) (*NewsAnalysis, string, error) {
	// System prompt references "internet access" only when web-search
	// is actually wired in (via Client.DefaultExtensions); otherwise
	// the model would hallucinate that it browsed. Keeping the bare
	// financial-analyst framing when extensions are off is both
	// cheaper and more honest.
	sysContent := "You are a financial news analyst. Respond with strict JSON of shape {sentiment: number -1..1, relevance: number 0..1, symbols: string[], summary: string (<=280 chars)}. Symbols must be uppercase US equity tickers, de-duplicated."
	userContent := fmt.Sprintf("Headline: %s\n\nBody:\n%s\n\nIdentify affected public companies and classify the market impact.", title, truncate(body, 4000))
	if c.hasWebSearch() {
		sysContent = "You are a financial news analyst with internet access. Before answering, search the web for recent news, context, and market reaction related to the headline to provide layered grounding. " + sysContent[len("You are a financial news analyst. "):]
		userContent = fmt.Sprintf("Headline: %s\n\nBody:\n%s\n\nIdentify affected public companies and classify the market impact. Use your web search tool to gather deeper context.", title, truncate(body, 4000))
	}
	sys := Message{Role: "system", Content: sysContent}
	user := Message{Role: "user", Content: userContent}
	var out NewsAnalysis

	model, err := c.CompleteJSON(ctx, []Message{sys, user}, &out, WithTemperature(0.1))
	if err != nil {
		return nil, model, err
	}
	out.Sentiment = clamp(out.Sentiment, -1, 1)
	out.Relevance = clamp(out.Relevance, 0, 1)
	for i, s := range out.Symbols {
		out.Symbols[i] = strings.ToUpper(strings.TrimSpace(s))
	}
	return &out, model, nil
}

// TechnicalContext is the compact snapshot of indicators the LLM
// decision prompt cares about. Defined here (not pulled directly from
// providers/market) so the llm package doesn't take a dependency on
// the market provider and callers can shape the payload as they see
// fit (zero fields are simply omitted from the prompt).
type TechnicalContext struct {
	Price  string  `json:"price"`
	SMA20  string  `json:"sma20,omitempty"`
	SMA50  string  `json:"sma50,omitempty"`
	RSI14  float64 `json:"rsi14,omitempty"`
	Hi52   string  `json:"hi52,omitempty"`
	Lo52   string  `json:"lo52,omitempty"`
	Chg1d  float64 `json:"chg_1d,omitempty"`
	Chg5d  float64 `json:"chg_5d,omitempty"`
	Chg30d float64 `json:"chg_30d,omitempty"`
}

// DecideRequest bundles context fed to the trade decision model.
type DecideRequest struct {
	Symbol            string
	CurrentPrice      string
	PositionQty       string
	PositionAvgCost   string
	CashAvailableUSD  string
	MaxOrderUSD       string
	Signals           []domain.Signal
	RecentNews        []domain.NewsItem
	RecentPoliticians []domain.PoliticianTrade
	// RecentInsiders, when non-empty, appends an Insider block so the
	// LLM can see the individual Form 4 filings underlying the
	// aggregated InsiderFollow signal.
	RecentInsiders []domain.InsiderTrade
	// RecentSocial lists WSB/Twitter bucket rows — sentiment, mention
	// volume, platform — for the symbol.
	RecentSocial []domain.SocialPost
	// RecentLobbying lists recent lobbying filings so the LLM can
	// reason about regulatory tailwinds/headwinds.
	RecentLobbying []domain.LobbyingEvent
	// RecentContracts lists federal contract awards, which can be
	// material revenue events for defense/aerospace/IT companies.
	RecentContracts []domain.GovContract
	// ShortInterest is the latest off-exchange short-volume snapshot.
	// Used by the LLM to reason about short-squeeze / distribution
	// dynamics alongside pure price structure.
	ShortInterest *domain.ShortVolume
	// Technicals is optional. When present the LLM prompt includes a
	// "Technicals:" block with the snapshot so the model can weigh
	// price structure alongside sentiment/politician signals.
	Technicals *TechnicalContext
}

// Decide asks the model to synthesise a trade rationale from the signal set.
func (c *Client) Decide(ctx context.Context, req DecideRequest) (*TradeRationale, string, error) {
	baseSys := "Given structured context you must respond with strict JSON {action: \"buy\"|\"sell\"|\"hold\", confidence: 0..1, target_usd: number, reasoning: string (<=400 chars)}. target_usd is the desired notional exposure to add (buy) or remove (sell). When uncertain choose \"hold\" with target_usd 0. Respect the MaxOrderUSD cap."
	sysContent := "You are an autonomous trading strategist. " + baseSys
	if c.hasWebSearch() {
		sysContent = "You are an autonomous trading strategist with internet access. Before deciding, search the web for the latest news, earnings, and market sentiment on the symbol to provide layered grounding. " + baseSys
	}
	sys := Message{Role: "system", Content: sysContent}
	var b strings.Builder
	fmt.Fprintf(&b, "Symbol: %s\nCurrentPrice: %s\nPositionQty: %s\nPositionAvgCost: %s\nCashAvailableUSD: %s\nMaxOrderUSD: %s\n\nSignals:\n", req.Symbol, req.CurrentPrice, req.PositionQty, req.PositionAvgCost, req.CashAvailableUSD, req.MaxOrderUSD)
	for _, s := range req.Signals {
		fmt.Fprintf(&b, "- kind=%s side=%s score=%.2f conf=%.2f reason=%s\n", s.Kind, s.Side, s.Score, s.Confidence, truncate(s.Reason, 180))
	}
	if len(req.RecentNews) > 0 {
		b.WriteString("\nRecentNews:\n")
		for _, n := range req.RecentNews {
			fmt.Fprintf(&b, "- [%s] %s (sent=%.2f rel=%.2f)\n", n.Source, truncate(n.Title, 120), n.Sentiment, n.Relevance)
		}
	}
	if len(req.RecentPoliticians) > 0 {
		b.WriteString("\nPoliticianTrades:\n")
		for _, p := range req.RecentPoliticians {
			fmt.Fprintf(&b, "- %s (%s) %s $%d-%d on %s\n", p.PoliticianName, p.Chamber, p.Side, p.AmountMinUSD, p.AmountMaxUSD, p.TradedAt.Format("2006-01-02"))
		}
	}
	if len(req.RecentInsiders) > 0 {
		b.WriteString("\nInsiderTrades (SEC Form 4):\n")
		for _, it := range req.RecentInsiders {
			title := it.InsiderTitle
			if title == "" {
				title = "insider"
			}
			fmt.Fprintf(&b, "- %s (%s) %s %d shares ($%s) on %s\n",
				it.InsiderName, truncate(title, 40), it.Side, it.Shares,
				formatUSDInt(it.ValueUSD), it.TransactedAt.Format("2006-01-02"))
		}
	}
	if len(req.RecentSocial) > 0 {
		b.WriteString("\nSocialBuzz:\n")
		for _, sp := range req.RecentSocial {
			fmt.Fprintf(&b, "- %s mentions=%d sentiment=%+.2f on %s\n",
				sp.Platform, sp.Mentions, sp.Sentiment, sp.BucketAt.Format("2006-01-02 15:00"))
		}
	}
	if len(req.RecentLobbying) > 0 {
		b.WriteString("\nLobbying:\n")
		for _, l := range req.RecentLobbying {
			fmt.Fprintf(&b, "- %s paid $%s re: %s (%s)\n",
				l.Registrant, formatUSDInt(l.AmountUSD), truncate(l.Issue, 80), l.FiledAt.Format("2006-01-02"))
		}
	}
	if len(req.RecentContracts) > 0 {
		b.WriteString("\nGovContracts:\n")
		for _, c := range req.RecentContracts {
			fmt.Fprintf(&b, "- %s $%s %s (%s)\n",
				c.Agency, formatUSDInt(c.AmountUSD), truncate(c.Description, 80), c.AwardedAt.Format("2006-01-02"))
		}
	}
	if sv := req.ShortInterest; sv != nil && sv.TotalVolume > 0 {
		fmt.Fprintf(&b, "\nShortInterest (off-exchange, %s): short=%d total=%d ratio=%.2f\n",
			sv.Day.Format("2006-01-02"), sv.ShortVolume, sv.TotalVolume, sv.ShortRatio)
	}
	if t := req.Technicals; t != nil {
		b.WriteString("\nTechnicals:\n")
		if t.SMA20 != "" {
			fmt.Fprintf(&b, "- SMA20=%s SMA50=%s\n", t.SMA20, t.SMA50)
		}
		if t.RSI14 > 0 {
			fmt.Fprintf(&b, "- RSI14=%.1f\n", t.RSI14)
		}
		if t.Hi52 != "" {
			fmt.Fprintf(&b, "- 52wk range: %s .. %s (current %s)\n", t.Lo52, t.Hi52, t.Price)
		}
		if t.Chg1d != 0 || t.Chg5d != 0 || t.Chg30d != 0 {
			fmt.Fprintf(&b, "- returns: 1d=%.2f%% 5d=%.2f%% 30d=%.2f%%\n", t.Chg1d*100, t.Chg5d*100, t.Chg30d*100)
		}
	}
	user := Message{Role: "user", Content: b.String()}
	var out TradeRationale

	model, err := c.CompleteJSON(ctx, []Message{sys, user}, &out, WithTemperature(0.2), WithMaxTokens(500))
	if err != nil {
		return nil, model, err
	}
	out.Action = strings.ToLower(strings.TrimSpace(out.Action))
	switch out.Action {
	case "buy", "sell", "hold":
	default:
		out.Action = "hold"
	}
	out.Confidence = clamp(out.Confidence, 0, 1)
	if out.TargetUSD < 0 {
		out.TargetUSD = 0
	}
	return &out, model, nil
}

// formatUSDInt renders a dollar integer with thousands separators.
// Kept local to the llm package so the DecideRequest builder doesn't
// import the strategy package (which would create a cycle).
func formatUSDInt(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d", v)
	n := len(s)
	if n <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	out := make([]byte, 0, n+n/3)
	pre := n % 3
	if pre > 0 {
		out = append(out, s[:pre]...)
		if n > pre {
			out = append(out, ',')
		}
	}
	for i := pre; i < n; i += 3 {
		out = append(out, s[i:i+3]...)
		if i+3 < n {
			out = append(out, ',')
		}
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
