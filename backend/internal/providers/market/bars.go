package market

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
)

// Bar is a single OHLCV bar. All prices are decimal — no floats in the
// accounting paths even though these are post-factum historical prices
// (callers should still keep decimal math intact so downstream
// indicator computations are reproducible).
type Bar struct {
	Time   time.Time       `json:"time"`
	Open   decimal.Decimal `json:"open"`
	High   decimal.Decimal `json:"high"`
	Low    decimal.Decimal `json:"low"`
	Close  decimal.Decimal `json:"close"`
	Volume int64           `json:"volume"`
}

// BarProvider returns historical OHLCV bars for a symbol. Implementations
// are expected to return bars in ascending chronological order (oldest
// first) so indicator math can walk the slice forward without sorting.
//
// `lookback` is the number of trading days (for daily bars) requested.
// Providers are free to return fewer rows if the upstream data is thin.
type BarProvider interface {
	Bars(ctx context.Context, symbol string, lookback int) ([]Bar, error)
}

// CachedBarProvider wraps one or more BarProviders with a TTL cache and
// a fallback chain. Daily bars are cheap to cache because they only
// change once per trading day; 15 minutes is plenty of headroom for the
// decide loop while keeping the data fresh enough to reflect an
// intraday tear-down and restart.
type CachedBarProvider struct {
	Providers []BarProvider
	TTL       time.Duration

	mu    sync.RWMutex
	cache map[string]cachedBars
}

type cachedBars struct {
	bars []Bar
	exp  time.Time
}

// NewCachedBarChain builds a CachedBarProvider with an arbitrary
// fallback chain. Nil providers are dropped silently so callers can
// pass a configured Alpaca provider alongside the default Stooq
// provider without worrying about nil checks.
func NewCachedBarChain(ttl time.Duration, providers ...BarProvider) *CachedBarProvider {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	ps := make([]BarProvider, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			ps = append(ps, p)
		}
	}
	return &CachedBarProvider{Providers: ps, TTL: ttl, cache: map[string]cachedBars{}}
}

// Bars returns cached bars if available, otherwise walks the provider
// chain. A cache hit is served even if `lookback` asks for more bars
// than we have — the indicator code is robust to short histories and
// hammering the upstream every time someone asks for 260 bars would
// defeat the point of caching.
func (c *CachedBarProvider) Bars(ctx context.Context, symbol string, lookback int) ([]Bar, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("%w: empty symbol", domain.ErrValidation)
	}
	now := time.Now()
	c.mu.RLock()
	hit, ok := c.cache[sym]
	c.mu.RUnlock()
	if ok && now.Before(hit.exp) {
		return hit.bars, nil
	}
	var lastErr error
	for _, p := range c.Providers {
		bars, err := p.Bars(ctx, sym, lookback)
		if err != nil {
			lastErr = err
			continue
		}
		if len(bars) == 0 {
			continue
		}
		c.store(sym, bars)
		return bars, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no bars for %s", domain.ErrProviderFailure, sym)
	}
	return nil, lastErr
}

func (c *CachedBarProvider) store(sym string, bars []Bar) {
	c.mu.Lock()
	c.cache[sym] = cachedBars{bars: bars, exp: time.Now().Add(c.TTL)}
	c.mu.Unlock()
}

// StooqBars fetches daily OHLCV bars from stooq.com. No auth, no API
// key; a reliable free backbone for US equities. The endpoint is the
// same CSV endpoint as the quote provider but with the daily interval
// selector.
type StooqBars struct{ HTTP *http.Client }

// NewStooqBars builds a StooqBars provider with a sane default client.
func NewStooqBars() *StooqBars {
	return &StooqBars{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Bars returns up to `lookback` most-recent daily bars, oldest first.
// Stooq's CSV contains the full history; we slice the tail so callers
// only see the window they asked for (plus we always keep enough bars
// for a 52-week moving calc — ~260 trading days).
func (s *StooqBars) Bars(ctx context.Context, symbol string, lookback int) ([]Bar, error) {
	sym := strings.ToLower(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("%w: empty symbol", domain.ErrValidation)
	}
	u := fmt.Sprintf("https://stooq.com/q/d/l/?s=%s.us&i=d", sym)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: stooq bars req: %v", domain.ErrProviderFailure, err)
	}
	req.Header.Set("User-Agent", "trader/1.0")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: stooq bars: %v", domain.ErrProviderFailure, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: stooq bars %s", domain.ErrProviderFailure, resp.Status)
	}
	r := csv.NewReader(resp.Body)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: stooq bars csv: %v", domain.ErrProviderFailure, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%w: stooq bars empty", domain.ErrProviderFailure)
	}
	bars, err := parseStooqCSV(rows)
	if err != nil {
		return nil, err
	}
	if lookback > 0 && len(bars) > lookback {
		bars = bars[len(bars)-lookback:]
	}
	return bars, nil
}

// parseStooqCSV parses Stooq's daily CSV. The header row is
// `Date,Open,High,Low,Close,Volume` — rows are already in ascending
// order, so we preserve that and let callers walk forward.
func parseStooqCSV(rows [][]string) ([]Bar, error) {
	if len(rows) < 2 {
		return nil, fmt.Errorf("%w: too few rows", domain.ErrProviderFailure)
	}
	header := rows[0]
	// Defensive: find column indices rather than assume a fixed order.
	idx := map[string]int{}
	for i, name := range header {
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	col := func(name string) (int, bool) {
		v, ok := idx[name]
		return v, ok
	}
	dateI, ok1 := col("date")
	openI, ok2 := col("open")
	highI, ok3 := col("high")
	lowI, ok4 := col("low")
	closeI, ok5 := col("close")
	if !(ok1 && ok2 && ok3 && ok4 && ok5) {
		return nil, fmt.Errorf("%w: stooq missing OHLC columns: %v", domain.ErrProviderFailure, header)
	}
	volI, hasVol := col("volume")

	out := make([]Bar, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) <= closeI {
			continue
		}
		// Stooq marks missing rows as "N/D"; skip them rather than fail.
		if row[closeI] == "N/D" || row[closeI] == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", row[dateI])
		if err != nil {
			continue
		}
		o, err1 := decimal.NewFromString(row[openI])
		h, err2 := decimal.NewFromString(row[highI])
		l, err3 := decimal.NewFromString(row[lowI])
		c, err4 := decimal.NewFromString(row[closeI])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		var vol int64
		if hasVol && volI < len(row) && row[volI] != "" && row[volI] != "N/D" {
			if v, err := strconv.ParseInt(row[volI], 10, 64); err == nil {
				vol = v
			}
		}
		out = append(out, Bar{Time: t.UTC(), Open: o, High: h, Low: l, Close: c, Volume: vol})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: stooq parsed zero bars", domain.ErrProviderFailure)
	}
	return out, nil
}
