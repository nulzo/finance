// Package congress aggregates disclosures of congressional and senate
// stock trades. Several free providers exist (CapitolTrades, House eFD
// exports, and paid sources like Quiver Quantitative); each is exposed
// behind the Source interface so the engine can try them in order.
package congress

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/httpx"
)

// breakerBaseDelay / breakerMaxDelay control how aggressively we back
// off a source that fails in a row. The values assume several ingest
// ticks per hour — a 10-minute cap means a persistently-down upstream
// is tried at most ~6x per hour instead of every ~15m ingest tick.
const (
	breakerBaseDelay = 30 * time.Second
	breakerMaxDelay  = 10 * time.Minute
)

// sourceTimeout is the per-source deadline inside the aggregator. It is
// applied as a child context so a slow or hung provider cannot starve its
// siblings. The value is intentionally generous (upstreams like Quiver's
// /senatetrading endpoint routinely take 20–40s) but is bounded so the
// engine loop always makes forward progress.
const sourceTimeout = 75 * time.Second

// Source is a pluggable congressional trade provider.
type Source interface {
	Name() string
	Fetch(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error)
}

// Aggregator tries each source in order and returns the union of
// successfully fetched trades. A failing source does not abort the chain.
//
// Each source is wrapped in a SourceBreaker so a provider that returns
// errors repeatedly (CapitolTrades during its CloudFront outages, Quiver
// when its CSRF middleware chokes) is skipped for an exponentially
// growing window — stopping the same "503" / "deadline exceeded"
// warning from appearing on every single ingest tick.
type Aggregator struct {
	Sources []Source
	Log     zerolog.Logger

	mu       sync.Mutex
	breakers map[string]*httpx.SourceBreaker
}

// breakerFor returns (and lazily creates) the breaker for a named source.
func (a *Aggregator) breakerFor(name string) *httpx.SourceBreaker {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.breakers == nil {
		a.breakers = map[string]*httpx.SourceBreaker{}
	}
	b, ok := a.breakers[name]
	if !ok {
		b = httpx.NewSourceBreaker(breakerBaseDelay, breakerMaxDelay)
		a.breakers[name] = b
	}
	return b
}

// Fetch merges trades from every configured source, deduplicating by
// RawHash. Sources are queried concurrently so a single slow or broken
// provider (e.g. CapitolTrades' CloudFront outages, Quiver's /senatetrading
// stalls) cannot delay or block the others. Each source is given its own
// child context with sourceTimeout applied.
func (a *Aggregator) Fetch(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) {
	type result struct {
		name    string
		trades  []domain.PoliticianTrade
		err     error
		skipped bool
	}
	results := make(chan result, len(a.Sources))
	var wg sync.WaitGroup
	for _, s := range a.Sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			name := s.Name()
			br := a.breakerFor(name)
			if !br.Ready() {
				results <- result{name: name, skipped: true}
				return
			}
			sctx, cancel := context.WithTimeout(ctx, sourceTimeout)
			defer cancel()
			trades, err := s.Fetch(sctx, since)
			if err != nil {
				br.RecordFailure()
			} else {
				br.RecordSuccess()
			}
			results <- result{name: name, trades: trades, err: err}
		}(s)
	}
	wg.Wait()
	close(results)

	seen := map[string]struct{}{}
	var out []domain.PoliticianTrade
	var lastErr error
	for r := range results {
		if r.skipped {
			a.Log.Debug().Str("source", r.name).Msg("congress source skipped (cooldown)")
			continue
		}
		if r.err != nil {
			br := a.breakerFor(r.name)
			a.Log.Warn().
				Err(r.err).
				Str("source", r.name).
				Int("failures", br.Failures()).
				Time("next_attempt", br.NextAttempt()).
				Msg("congress source failed")
			lastErr = r.err
			continue
		}
		for _, t := range r.trades {
			if _, ok := seen[t.RawHash]; ok {
				continue
			}
			seen[t.RawHash] = struct{}{}
			out = append(out, t)
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrProviderFailure, lastErr)
	}
	return out, nil
}

// hashTrade builds a deterministic hash for dedupe.
func hashTrade(source, politician, symbol, side string, traded time.Time, minUSD, maxUSD int64) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%d|%d|%s", source,
		strings.ToLower(politician), strings.ToUpper(symbol), strings.ToLower(side),
		minUSD, maxUSD, traded.UTC().Format("2006-01-02"))
	return hex.EncodeToString(h.Sum(nil))
}

// ----------------------------- CapitolTrades ------------------------------

// CapitolTrades hits CapitolTrades' public bff endpoint. The response is
// not contractually stable — the client tolerates missing fields and
// falls back gracefully.
type CapitolTrades struct {
	BaseURL string
	HTTP    *http.Client
}

// NewCapitolTrades builds a client.
func NewCapitolTrades(baseURL string) *CapitolTrades {
	if baseURL == "" {
		baseURL = "https://bff.capitoltrades.com/trades"
	}
	return &CapitolTrades{BaseURL: baseURL, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Name implements Source.
func (c *CapitolTrades) Name() string { return "capitoltrades" }

type capitolResp struct {
	Data []struct {
		PoliticianName  string `json:"politician"`
		Chamber         string `json:"chamber"`
		Party           string `json:"party"`
		State           string `json:"state"`
		Ticker          string `json:"ticker"`
		Issuer          string `json:"issuer"`
		TxType          string `json:"txType"`
		SizeMin         int64  `json:"sizeMin"`
		SizeMax         int64  `json:"sizeMax"`
		TxDate          string `json:"txDate"`
		FiledDate       string `json:"filed"`
	} `json:"data"`
}

// Fetch pulls recent trades filed since `since`.
func (c *CapitolTrades) Fetch(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) {
	u := fmt.Sprintf("%s?pageSize=200&sortBy=-filingDate", c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trader/1.0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		// The CapitolTrades BFF is a CloudFront → Lambda service that
		// frequently returns 1KB+ of HTML on outages. Collapse that to a
		// single readable line so it doesn't poison the log.
		return nil, fmt.Errorf("capitoltrades: %s: %s", resp.Status, summariseHTTPBody(b))
	}
	var parsed capitolResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]domain.PoliticianTrade, 0, len(parsed.Data))
	for _, r := range parsed.Data {
		traded, _ := time.Parse("2006-01-02", r.TxDate)
		filed, _ := time.Parse("2006-01-02", r.FiledDate)
		if !since.IsZero() && filed.Before(since) {
			continue
		}
		side := normaliseTxType(r.TxType)
		if side == "" {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if symbol == "" || symbol == "N/A" {
			continue
		}
		trade := domain.PoliticianTrade{
			PoliticianName: r.PoliticianName,
			Chamber:        strings.ToLower(r.Chamber),
			Symbol:         symbol,
			AssetName:      r.Issuer,
			Side:           side,
			AmountMinUSD:   r.SizeMin,
			AmountMaxUSD:   r.SizeMax,
			TradedAt:       traded,
			DisclosedAt:    filed,
			Source:         c.Name(),
		}
		trade.RawHash = hashTrade(trade.Source, trade.PoliticianName, trade.Symbol, string(trade.Side),
			trade.TradedAt, trade.AmountMinUSD, trade.AmountMaxUSD)
		out = append(out, trade)
	}
	return out, nil
}

// summariseHTTPBody returns a compact one-line description of a response
// body. HTML bodies (common on CloudFront/Cloudflare outages) are reduced
// to their <title> text when present; otherwise the body is trimmed to
// 160 chars with newlines collapsed.
func summariseHTTPBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "<empty body>"
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		if i := strings.Index(lower, "<title>"); i >= 0 {
			if j := strings.Index(lower[i:], "</title>"); j > 0 {
				title := strings.TrimSpace(s[i+len("<title>") : i+j])
				return "html: " + truncate(title, 200)
			}
		}
		// Detect the specific LambdaExecutionError signature.
		if strings.Contains(lower, "lambda function") {
			return "upstream lambda misconfigured (CapitolTrades outage)"
		}
		return "html response (upstream error)"
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return truncate(s, 240)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func normaliseTxType(t string) domain.Side {
	t = strings.ToLower(t)
	switch {
	case strings.Contains(t, "buy") || strings.Contains(t, "purchase") || t == "p":
		return domain.SideBuy
	case strings.Contains(t, "sell") || strings.Contains(t, "sale") || t == "s":
		return domain.SideSell
	}
	return ""
}

// ------------------------------- Quiver -----------------------------------

// quiverCSRFToken matches the hard-coded CSRF header the official Quiver
// Python client sends. Quiver's Django middleware can stall on requests
// that lack it, which manifests to our client as a context deadline –
// not a clean 4xx. Mirroring the upstream client avoids that.
//
// Source: github.com/Quiver-Quantitative/python-api/blob/main/quiverquant.py
const quiverCSRFToken = "TyTJwjuEC7VV7mOqZ622haRaaUr0x0Ng4nrwSRFKQs7vdoBcJlK9qjAS69ghzhFu"

// Quiver hits the Quiver Quantitative congressional trading API.
//
// Quiver authenticates with DRF-style `Authorization: Token <key>` headers,
// NOT OAuth2 bearer tokens. The client also sends the same CSRF header as
// the official Python SDK to avoid unauthenticated request stalls.
type Quiver struct {
	Token string
	HTTP  *http.Client
	// BaseURL defaults to https://api.quiverquant.com. Overridable for tests.
	BaseURL string
}

// NewQuiver builds a client. The per-call timeout is deliberately high:
// Quiver's /senatetrading endpoint has been observed to take 30–45s on
// warm paths, so short timeouts frequently caused spurious "context
// deadline exceeded" warnings despite the endpoint eventually returning
// clean 200 responses.
func NewQuiver(token string) *Quiver {
	return &Quiver{
		Token:   token,
		BaseURL: "https://api.quiverquant.com",
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements Source.
func (q *Quiver) Name() string { return "quiver" }

type quiverRow struct {
	Representative  string `json:"Representative"`
	Senator         string `json:"Senator"`
	Ticker          string `json:"Ticker"`
	Transaction     string `json:"Transaction"`
	Range           string `json:"Range"`
	TransactionDate string `json:"TransactionDate"`
	ReportDate      string `json:"ReportDate"`
	House           string `json:"House"`
	Party           string `json:"Party"`
	State           string `json:"State"`
}

// Fetch returns congressional + senate trades disclosed since `since`.
// A missing token is treated as an ordinary Source failure so the
// Aggregator can continue with other providers.
func (q *Quiver) Fetch(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) {
	if q.Token == "" {
		return nil, errors.New("quiver: token not configured")
	}
	base := q.BaseURL
	if base == "" {
		base = "https://api.quiverquant.com"
	}
	endpoints := []struct {
		url     string
		chamber string
	}{
		{base + "/beta/live/congresstrading", "house"},
		{base + "/beta/live/senatetrading", "senate"},
	}

	// Fetch both chambers concurrently. Quiver serves each endpoint from
	// separate Django views with independent latencies; serialising them
	// doubles the wall-clock time for no benefit.
	type chamberResult struct {
		chamber string
		rows    []quiverRow
		err     error
	}
	results := make(chan chamberResult, len(endpoints))
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		wg.Add(1)
		go func(url, chamber string) {
			defer wg.Done()
			rows, err := q.fetchEndpoint(ctx, url)
			results <- chamberResult{chamber: chamber, rows: rows, err: err}
		}(ep.url, ep.chamber)
	}
	wg.Wait()
	close(results)

	var all []domain.PoliticianTrade
	var lastErr error
	for res := range results {
		if res.err != nil {
			lastErr = res.err
			continue
		}
		for _, r := range res.rows {
			name := strings.TrimSpace(r.Representative)
			chamber := res.chamber
			if name == "" {
				name = strings.TrimSpace(r.Senator)
				chamber = "senate"
			}
			side := normaliseTxType(r.Transaction)
			if side == "" {
				continue
			}
			traded := parseQuiverDate(r.TransactionDate)
			filed := parseQuiverDate(r.ReportDate)
			// Drop rows we can't timestamp: without a real TradedAt
			// the 21-day strategy lookback has no way to reason about
			// them, and they pollute the DB with year-0001 rows. If
			// only `filed` is missing, fall back to `traded` (they
			// agree for most sources).
			if traded.IsZero() {
				continue
			}
			if filed.IsZero() {
				filed = traded
			}
			if !since.IsZero() && filed.Before(since) {
				continue
			}
			minUSD, maxUSD := parseAmountRange(r.Range)
			t := domain.PoliticianTrade{
				PoliticianName: name,
				Chamber:        chamber,
				Symbol:         strings.ToUpper(strings.TrimSpace(r.Ticker)),
				Side:           side,
				AmountMinUSD:   minUSD,
				AmountMaxUSD:   maxUSD,
				TradedAt:       traded,
				DisclosedAt:    filed,
				Source:         q.Name(),
			}
			if t.Symbol == "" {
				continue
			}
			t.RawHash = hashTrade(t.Source, t.PoliticianName, t.Symbol, string(t.Side), t.TradedAt, t.AmountMinUSD, t.AmountMaxUSD)
			all = append(all, t)
		}
	}
	// Only surface an error when *both* endpoints failed and produced no
	// rows – otherwise a single broken chamber shouldn't blank out the run.
	if len(all) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return all, nil
}

func (q *Quiver) fetchEndpoint(ctx context.Context, u string) ([]quiverRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// DRF TokenAuthentication – NOT OAuth2 Bearer. Using "Bearer" causes
	// Quiver to hang instead of returning a clean 401.
	req.Header.Set("Authorization", "Token "+q.Token)
	req.Header.Set("X-CSRFToken", quiverCSRFToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trader/1.0")
	// Quiver's public endpoints occasionally 502/504 during rollouts and
	// our connections sometimes die mid-response. A short bounded retry
	// absorbs these without letting a single blip wipe the ingest cycle.
	resp, err := httpx.DoWithRetry(ctx, q.HTTP, req, httpx.RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("quiver %s: %w", shortPath(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("quiver %s: %s: %s", shortPath(u), resp.Status, summariseHTTPBody(b))
	}
	var rows []quiverRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("quiver %s: decode: %w", shortPath(u), err)
	}
	return rows, nil
}

// parseQuiverDate is lenient: Quiver has shipped several date formats
// over time (YYYY-MM-DD, RFC3339, MM/DD/YYYY, "April 7, 2026"). We
// try each before giving up. Unknown formats return the zero Time,
// and the caller is expected to drop that row rather than persist
// year-0001 rows.
func parseQuiverDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z",
		"01/02/2006",
		"1/2/2006",
		"January 2, 2006",
		"Jan 2, 2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func shortPath(u string) string {
	if i := strings.Index(u, "/beta/"); i >= 0 {
		return u[i:]
	}
	return u
}

// ---------------------------- Lambda Finance ------------------------------

// LambdaFinance hits lambdafin.com's free, unauthenticated congressional
// trades endpoint. Their coverage is broader than the CapitolTrades BFF
// and it is a useful zero-config fallback while CapitolTrades is degraded.
//
// Endpoint: GET /api/congressional/recent?days=<N>&limit=<N>
// Docs:     https://www.lambdafin.com/articles/capitol-trades-api
type LambdaFinance struct {
	BaseURL string
	Days    int
	Limit   int
	HTTP    *http.Client
}

// NewLambdaFinance builds a client with sensible defaults.
func NewLambdaFinance() *LambdaFinance {
	return &LambdaFinance{
		BaseURL: "https://www.lambdafin.com/api/congressional/recent",
		Days:    90,
		Limit:   500,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Name implements Source.
func (l *LambdaFinance) Name() string { return "lambdafin" }

type lambdaFinResp struct {
	Trades []struct {
		Symbol           string `json:"symbol"`
		Representative   string `json:"representative"`
		TransactionDate  string `json:"transactionDate"`
		DisclosureDate   string `json:"disclosureDate"`
		Type             string `json:"type"`
		Amount           string `json:"amount"`
		Owner            string `json:"owner"`
		AssetDescription string `json:"assetDescription"`
		Party            string `json:"party"`
		State            string `json:"state"`
		Chamber          string `json:"chamber"`
		PTRLink          string `json:"ptrLink"`
	} `json:"trades"`
}

// Fetch pulls recent trades. `since` is honoured after fetch since the
// upstream uses a fixed `days` window.
func (l *LambdaFinance) Fetch(ctx context.Context, since time.Time) ([]domain.PoliticianTrade, error) {
	days := l.Days
	if days <= 0 {
		days = 90
	}
	// Expand the window when the caller asks for older data.
	if !since.IsZero() {
		if d := int(time.Since(since).Hours()/24) + 7; d > days {
			days = d
		}
	}
	u := fmt.Sprintf("%s?days=%d&limit=%d", l.BaseURL, days, l.Limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trader/1.0")
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lambdafin: %s: %s", resp.Status, summariseHTTPBody(b))
	}
	var parsed lambdaFinResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("lambdafin: decode: %w", err)
	}
	out := make([]domain.PoliticianTrade, 0, len(parsed.Trades))
	for _, r := range parsed.Trades {
		side := normaliseTxType(r.Type)
		if side == "" {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(r.Symbol))
		if symbol == "" || symbol == "N/A" || symbol == "--" {
			continue
		}
		traded, _ := time.Parse("2006-01-02", r.TransactionDate)
		filed, _ := time.Parse("2006-01-02", r.DisclosureDate)
		if !since.IsZero() && !filed.IsZero() && filed.Before(since) {
			continue
		}
		minUSD, maxUSD := parseAmountRange(r.Amount)
		chamber := strings.ToLower(strings.TrimSpace(r.Chamber))
		if chamber == "" {
			chamber = "house"
		}
		t := domain.PoliticianTrade{
			PoliticianName: strings.TrimSpace(r.Representative),
			Chamber:        chamber,
			Symbol:         symbol,
			AssetName:      r.AssetDescription,
			Side:           side,
			AmountMinUSD:   minUSD,
			AmountMaxUSD:   maxUSD,
			TradedAt:       traded,
			DisclosedAt:    filed,
			Source:         l.Name(),
		}
		t.RawHash = hashTrade(t.Source, t.PoliticianName, t.Symbol, string(t.Side), t.TradedAt, t.AmountMinUSD, t.AmountMaxUSD)
		out = append(out, t)
	}
	return out, nil
}

// parseAmountRange converts strings like "$1,001 - $15,000" to (1001, 15000).
func parseAmountRange(s string) (int64, int64) {
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return 0, 0
	}
	var a, b int64
	_, _ = fmt.Sscan(parts[0], &a)
	if len(parts) > 1 {
		_, _ = fmt.Sscan(parts[1], &b)
	} else {
		b = a
	}
	return a, b
}
