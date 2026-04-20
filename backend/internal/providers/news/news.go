// Package news aggregates market news from multiple providers and
// presents a unified stream. Providers are plug-and-play; if none are
// configured with credentials, an RSS fallback ensures the engine has
// at least something to reason about.
package news

import (
	"context"
	"encoding/json"
	"encoding/xml"
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

// sourceTimeout caps the wall-clock a single news provider may consume
// inside the aggregator. Exposed as a child context so one stalled source
// (a slow RSS origin, Finnhub under load, etc.) cannot starve its peers.
const sourceTimeout = 30 * time.Second

// breakerBaseDelay / breakerMaxDelay control per-source exponential
// backoff. News breakers are more lenient than politician-trade
// breakers because news sources recover much faster (e.g. Finnhub's
// CDN flaps resolve in tens of seconds) and the cost of missing a
// fetch is higher.
const (
	breakerBaseDelay = 15 * time.Second
	breakerMaxDelay  = 5 * time.Minute
)

// Source is a pluggable news provider.
type Source interface {
	Name() string
	Fetch(ctx context.Context, since time.Time) ([]domain.NewsItem, error)
}

// Aggregator calls each source and dedupes by URL.
//
// Sources are wrapped in a SourceBreaker so a provider that fails on
// several consecutive ticks (Finnhub's occasional 5xx, an RSS origin
// behind a broken CDN, newsapi throttling) is skipped for an
// exponentially growing window — without the aggregator itself having
// to track state across calls.
type Aggregator struct {
	Sources []Source
	Log     zerolog.Logger

	mu       sync.Mutex
	breakers map[string]*httpx.SourceBreaker
}

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

// Fetch returns the union of recent items, ignoring failing sources.
// Sources run concurrently under a per-source timeout so one slow origin
// (e.g. Finnhub cold cache, an RSS endpoint behind a glitchy CDN) cannot
// delay or kill the others.
func (a *Aggregator) Fetch(ctx context.Context, since time.Time) ([]domain.NewsItem, error) {
	type result struct {
		name    string
		items   []domain.NewsItem
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
			items, err := s.Fetch(sctx, since)
			if err != nil {
				br.RecordFailure()
			} else {
				br.RecordSuccess()
			}
			results <- result{name: name, items: items, err: err}
		}(s)
	}
	wg.Wait()
	close(results)

	seen := map[string]struct{}{}
	var out []domain.NewsItem
	var lastErr error
	for r := range results {
		if r.skipped {
			a.Log.Debug().Str("source", r.name).Msg("news source skipped (cooldown)")
			continue
		}
		if r.err != nil {
			br := a.breakerFor(r.name)
			a.Log.Warn().
				Err(r.err).
				Str("source", r.name).
				Int("failures", br.Failures()).
				Time("next_attempt", br.NextAttempt()).
				Msg("news source failed")
			lastErr = r.err
			continue
		}
		for _, n := range r.items {
			if n.URL == "" {
				continue
			}
			if _, ok := seen[n.URL]; ok {
				continue
			}
			seen[n.URL] = struct{}{}
			out = append(out, n)
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrProviderFailure, lastErr)
	}
	return out, nil
}

// ------------------------------- Finnhub ----------------------------------

// Finnhub uses the free general market news endpoint.
type Finnhub struct {
	APIKey string
	HTTP   *http.Client
}

// NewFinnhub constructs a Finnhub source. Returns nil if apiKey is empty.
func NewFinnhub(apiKey string) *Finnhub {
	if apiKey == "" {
		return nil
	}
	return &Finnhub{APIKey: apiKey, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Name implements Source.
func (f *Finnhub) Name() string { return "finnhub" }

// Fetch pulls general market news.
func (f *Finnhub) Fetch(ctx context.Context, since time.Time) ([]domain.NewsItem, error) {
	url := fmt.Sprintf("https://finnhub.io/api/v1/news?category=general&token=%s", f.APIKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	// Finnhub occasionally returns 5xx or drops the connection during
	// their CDN cache refresh windows. A bounded retry absorbs those
	// without propagating a misleading context-deadline error upstream.
	resp, err := httpx.DoWithRetry(ctx, f.HTTP, req, httpx.RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   400 * time.Millisecond,
		MaxDelay:    1500 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("finnhub: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rows []struct {
		Headline string `json:"headline"`
		Summary  string `json:"summary"`
		Source   string `json:"source"`
		URL      string `json:"url"`
		Related  string `json:"related"`
		Datetime int64  `json:"datetime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	out := make([]domain.NewsItem, 0, len(rows))
	for _, r := range rows {
		t := time.Unix(r.Datetime, 0).UTC()
		if !since.IsZero() && t.Before(since) {
			continue
		}
		out = append(out, domain.NewsItem{
			Source:  "finnhub:" + r.Source,
			URL:     r.URL,
			Title:   r.Headline,
			Summary: r.Summary,
			Symbols: strings.ToUpper(r.Related),
			PubAt:   t,
		})
	}
	return out, nil
}

// ------------------------------- NewsAPI ----------------------------------

// NewsAPI uses newsapi.org's /v2/top-headlines?category=business endpoint.
type NewsAPI struct {
	APIKey string
	HTTP   *http.Client
}

// NewNewsAPI constructs a NewsAPI source. Returns nil if apiKey is empty.
func NewNewsAPI(apiKey string) *NewsAPI {
	if apiKey == "" {
		return nil
	}
	return &NewsAPI{APIKey: apiKey, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Name implements Source.
func (n *NewsAPI) Name() string { return "newsapi" }

// Fetch pulls top business headlines.
func (n *NewsAPI) Fetch(ctx context.Context, since time.Time) ([]domain.NewsItem, error) {
	url := fmt.Sprintf("https://newsapi.org/v2/top-headlines?category=business&language=en&pageSize=100&apiKey=%s", n.APIKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("newsapi: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var parsed struct {
		Articles []struct {
			Source      struct{ Name string } `json:"source"`
			Title       string                `json:"title"`
			Description string                `json:"description"`
			URL         string                `json:"url"`
			PublishedAt time.Time             `json:"publishedAt"`
		} `json:"articles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]domain.NewsItem, 0, len(parsed.Articles))
	for _, a := range parsed.Articles {
		if !since.IsZero() && a.PublishedAt.Before(since) {
			continue
		}
		out = append(out, domain.NewsItem{
			Source:  "newsapi:" + a.Source.Name,
			URL:     a.URL,
			Title:   a.Title,
			Summary: a.Description,
			PubAt:   a.PublishedAt,
		})
	}
	return out, nil
}

// --------------------------------- RSS ------------------------------------

// RSS is a zero-config fallback. It pulls from a list of public finance feeds.
type RSS struct {
	URLs []string
	HTTP *http.Client
}

// NewRSS returns an RSS source seeded with well-known public feeds.
func NewRSS(extra ...string) *RSS {
	defaults := []string{
		"https://feeds.a.dj.com/rss/RSSMarketsMain.xml",
		"https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&type=8-K&dateb=&owner=include&count=40&output=atom",
		"https://seekingalpha.com/market_currents.xml",
	}
	return &RSS{URLs: append(defaults, extra...), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Name implements Source.
func (r *RSS) Name() string { return "rss" }

// Fetch reads each feed and collects items newer than `since`. Feeds are
// polled concurrently because origins are independent (dj.com, sec.gov,
// seekingalpha.com, …) and serialising them made a single slow origin
// block the whole RSS source from completing inside its timeout window.
func (r *RSS) Fetch(ctx context.Context, since time.Time) ([]domain.NewsItem, error) {
	type feedResult struct {
		items []domain.NewsItem
		err   error
	}
	results := make(chan feedResult, len(r.URLs))
	var wg sync.WaitGroup
	for _, u := range r.URLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			items, err := r.fetchOne(ctx, u, since)
			results <- feedResult{items: items, err: err}
		}(u)
	}
	wg.Wait()
	close(results)

	var out []domain.NewsItem
	for res := range results {
		if res.err != nil {
			continue
		}
		out = append(out, res.items...)
	}
	return out, nil
}

type rssDoc struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			Desc    string `xml:"description"`
			PubDate string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDoc struct {
	XMLName xml.Name `xml:"feed"`
	Entries []struct {
		Title   string `xml:"title"`
		Summary string `xml:"summary"`
		Link    struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Updated string `xml:"updated"`
	} `xml:"entry"`
}

func (r *RSS) fetchOne(ctx context.Context, url string, since time.Time) ([]domain.NewsItem, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "trader/1.0 (+https://github.com/nulzo/trader)")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rss: %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out []domain.NewsItem
	// Try RSS first, then Atom.
	var rss rssDoc
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		for _, it := range rss.Channel.Items {
			t, _ := parseLooseTime(it.PubDate)
			if !since.IsZero() && !t.IsZero() && t.Before(since) {
				continue
			}
			if t.IsZero() {
				t = time.Now().UTC()
			}
			out = append(out, domain.NewsItem{Source: "rss", URL: it.Link, Title: it.Title, Summary: stripHTML(it.Desc), PubAt: t})
		}
		return out, nil
	}
	var atom atomDoc
	if err := xml.Unmarshal(body, &atom); err == nil {
		for _, e := range atom.Entries {
			t, _ := parseLooseTime(e.Updated)
			if !since.IsZero() && !t.IsZero() && t.Before(since) {
				continue
			}
			if t.IsZero() {
				t = time.Now().UTC()
			}
			out = append(out, domain.NewsItem{Source: "rss", URL: e.Link.Href, Title: e.Title, Summary: stripHTML(e.Summary), PubAt: t})
		}
	}
	return out, nil
}

func parseLooseTime(s string) (time.Time, error) {
	layouts := []string{time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z07:00"}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown time format: %s", s)
}

func stripHTML(s string) string {
	// Very rough tag stripper; enough for summary snippets.
	var b strings.Builder
	in := false
	for _, r := range s {
		switch r {
		case '<':
			in = true
		case '>':
			in = false
		default:
			if !in {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}
