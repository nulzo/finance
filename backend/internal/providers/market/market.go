// Package market provides price lookup with broker-primary, free
// fallbacks (Yahoo, Stooq), and a deterministic synthetic last-resort
// so the engine never becomes price-blind. Quotes are cached briefly
// to avoid hammering upstream providers.
package market

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/broker"
	"github.com/nulzo/trader/internal/domain"
)

// QuoteProvider returns quotes for a symbol.
type QuoteProvider interface {
	Quote(ctx context.Context, symbol string) (*domain.Quote, error)
}

// CachedProvider caches quotes for TTL duration and walks a fallback chain.
type CachedProvider struct {
	Providers []QuoteProvider
	TTL       time.Duration
	mu        sync.RWMutex
	cache     map[string]cachedQuote
}

type cachedQuote struct {
	q   domain.Quote
	exp time.Time
}

// NewCachedProvider wraps one or more providers with a TTL cache.
// The first non-nil provider that returns a quote wins.
func NewCachedProvider(primary, fallback QuoteProvider, ttl time.Duration) *CachedProvider {
	chain := make([]QuoteProvider, 0, 2)
	if primary != nil {
		chain = append(chain, primary)
	}
	if fallback != nil {
		chain = append(chain, fallback)
	}
	return NewCachedChain(ttl, chain...)
}

// NewCachedChain builds a CachedProvider with an arbitrary fallback chain.
func NewCachedChain(ttl time.Duration, providers ...QuoteProvider) *CachedProvider {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	ps := make([]QuoteProvider, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			ps = append(ps, p)
		}
	}
	return &CachedProvider{Providers: ps, TTL: ttl, cache: map[string]cachedQuote{}}
}

// Quote returns a symbol's price, checking cache then walking the chain.
func (c *CachedProvider) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	sym := strings.ToUpper(symbol)
	now := time.Now()
	c.mu.RLock()
	cached, ok := c.cache[sym]
	c.mu.RUnlock()
	if ok && now.Before(cached.exp) {
		q := cached.q
		return &q, nil
	}
	var lastErr error
	for _, p := range c.Providers {
		q, err := p.Quote(ctx, sym)
		if err != nil {
			lastErr = err
			continue
		}
		if q == nil {
			continue
		}
		c.store(sym, *q)
		return q, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no quote for %s", domain.ErrProviderFailure, sym)
	}
	return nil, lastErr
}

func (c *CachedProvider) store(sym string, q domain.Quote) {
	c.mu.Lock()
	c.cache[sym] = cachedQuote{q: q, exp: time.Now().Add(c.TTL)}
	c.mu.Unlock()
}

// BrokerAdapter turns a broker.Broker into a QuoteProvider.
type BrokerAdapter struct{ B broker.Broker }

// Quote forwards to the underlying broker.
func (b BrokerAdapter) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	return b.B.Quote(ctx, symbol)
}

// Stooq is a public CSV endpoint that returns last-trade data for
// US equities without authentication. It is a reliable open fallback
// when broker-side and Yahoo endpoints are unavailable.
type Stooq struct{ HTTP *http.Client }

// NewStooq builds a Stooq quote provider.
func NewStooq() *Stooq { return &Stooq{HTTP: &http.Client{Timeout: 10 * time.Second}} }

// Quote returns a price from stooq.com for a US ticker.
func (s *Stooq) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	sym := strings.ToLower(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("%w: empty symbol", domain.ErrValidation)
	}
	u := fmt.Sprintf("https://stooq.com/q/l/?s=%s.us&f=sd2t2ohlcv&h&e=csv", sym)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "trader/1.0")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: stooq: %v", domain.ErrProviderFailure, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: stooq %s", domain.ErrProviderFailure, resp.Status)
	}
	r := csv.NewReader(resp.Body)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: stooq csv: %v", domain.ErrProviderFailure, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%w: stooq empty", domain.ErrProviderFailure)
	}
	// Columns: Symbol,Date,Time,Open,High,Low,Close,Volume
	row := rows[1]
	if len(row) < 7 {
		return nil, fmt.Errorf("%w: stooq row too short", domain.ErrProviderFailure)
	}
	priceStr := row[6]
	if priceStr == "" || priceStr == "N/D" {
		return nil, fmt.Errorf("%w: stooq no price for %s", domain.ErrProviderFailure, sym)
	}
	p, err := decimal.NewFromString(priceStr)
	if err != nil || p.IsZero() {
		return nil, fmt.Errorf("%w: stooq price parse: %v", domain.ErrProviderFailure, err)
	}
	ts := time.Now().UTC()
	if len(row) >= 3 {
		if t, err := time.Parse("2006-01-02 15:04:05", row[1]+" "+row[2]); err == nil {
			ts = t.UTC()
		}
	}
	return &domain.Quote{Symbol: strings.ToUpper(sym), Price: p, Timestamp: ts}, nil
}

// Synthetic produces a deterministic, plausible price per symbol. It is
// the last-resort provider when no external market data is reachable
// (e.g. offline test environments). Prices are stable within a given
// bucket so the engine behaves predictably.
type Synthetic struct {
	// Base price range in dollars.
	Min, Max decimal.Decimal
	// Bucket time so the price only moves once per interval.
	Bucket time.Duration
}

// NewSynthetic builds a Synthetic with sensible defaults.
func NewSynthetic() *Synthetic {
	return &Synthetic{Min: decimal.NewFromInt(20), Max: decimal.NewFromInt(500), Bucket: time.Minute}
}

// Quote returns a pseudo-random but stable price for a symbol.
func (s *Synthetic) Quote(_ context.Context, symbol string) (*domain.Quote, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("%w: empty symbol", domain.ErrValidation)
	}
	bucket := s.Bucket
	if bucket <= 0 {
		bucket = time.Minute
	}
	min := s.Min
	max := s.Max
	if min.IsZero() {
		min = decimal.NewFromInt(20)
	}
	if max.LessThanOrEqual(min) {
		max = min.Add(decimal.NewFromInt(100))
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(sym))
	_, _ = h.Write([]byte(time.Now().UTC().Truncate(bucket).Format(time.RFC3339)))
	pctile := float64(h.Sum64()%10_000) / 10_000
	span := max.Sub(min)
	price := min.Add(span.Mul(decimal.NewFromFloat(pctile))).Round(2)
	return &domain.Quote{Symbol: sym, Price: price, Timestamp: time.Now().UTC()}, nil
}

// Yahoo is a zero-credential price provider using the public
// query1.finance.yahoo.com endpoint. Useful as a fallback, though
// Yahoo has been known to rotate auth requirements.
type Yahoo struct{ HTTP *http.Client }

// NewYahoo constructs a Yahoo client.
func NewYahoo() *Yahoo {
	return &Yahoo{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Quote returns a recent price for a symbol via Yahoo.
func (y *Yahoo) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	u := fmt.Sprintf("https://query1.finance.yahoo.com/v7/finance/quote?symbols=%s", sym)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (trader)")
	resp, err := y.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: yahoo: %v", domain.ErrProviderFailure, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: yahoo %s: %s", domain.ErrProviderFailure, resp.Status, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		QuoteResponse struct {
			Result []struct {
				Symbol             string  `json:"symbol"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				Bid                float64 `json:"bid"`
				Ask                float64 `json:"ask"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
			} `json:"result"`
		} `json:"quoteResponse"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%w: yahoo decode: %v", domain.ErrProviderFailure, err)
	}
	if len(parsed.QuoteResponse.Result) == 0 {
		return nil, fmt.Errorf("%w: yahoo no result", domain.ErrProviderFailure)
	}
	r := parsed.QuoteResponse.Result[0]
	if r.RegularMarketPrice <= 0 {
		return nil, fmt.Errorf("%w: yahoo zero price", domain.ErrProviderFailure)
	}
	return &domain.Quote{
		Symbol:    r.Symbol,
		Price:     decimal.NewFromFloat(r.RegularMarketPrice),
		Bid:       decimal.NewFromFloat(r.Bid),
		Ask:       decimal.NewFromFloat(r.Ask),
		Timestamp: time.Unix(r.RegularMarketTime, 0).UTC(),
	}, nil
}
