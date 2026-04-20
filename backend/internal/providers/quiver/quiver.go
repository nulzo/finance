// Package quiver is a thin client for the Quiver Quantitative alternative-data
// endpoints beyond congressional trading. The existing `providers/congress`
// package already handles `congresstrading`/`senatetrading`; this package
// picks up the rest: insider Form 4 filings, off-exchange short volume,
// WallStreetBets rollups, corporate lobbying, and federal contracts.
//
// The client deliberately mirrors the authentication shape of the official
// Python SDK (DRF `Authorization: Token <key>` + a hard-coded CSRF header),
// because Quiver's Django middleware has been observed to hang requests that
// omit the CSRF token rather than return a clean 4xx. See
// github.com/Quiver-Quantitative/python-api for reference.
package quiver

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/httpx"
)

// quiverCSRFToken is the same hard-coded CSRF token the official Quiver
// Python SDK ships with. Mirroring it avoids the Django middleware stalls
// we saw on unauthenticated requests.
const quiverCSRFToken = "TyTJwjuEC7VV7mOqZ622haRaaUr0x0Ng4nrwSRFKQs7vdoBcJlK9qjAS69ghzhFu"

// subscriptionGateMessage is the canonical string Quiver returns when the
// caller's API key lacks the required subscription tier. It comes back as
// a bare JSON string (not an HTTP error), so we sniff the body rather than
// the status code.
const subscriptionGateMessage = "Upgrade your subscription plan to access this dataset."

// ErrSubscriptionRequired is returned when a Quiver endpoint requires a
// higher-tier subscription than the caller is on. The engine treats this
// as a soft-failure (skip the dataset, do not retry).
var ErrSubscriptionRequired = errors.New("quiver: endpoint requires a higher subscription tier")

// Client is a token-authenticated Quiver HTTP client.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

// New builds a client with sane per-request timeouts. Quiver's /beta/live/*
// endpoints have been observed to take 20–40s on warm paths, so a short
// global timeout would produce spurious deadline-exceeded errors.
func New(token string) *Client {
	return &Client{
		Token:   token,
		BaseURL: "https://api.quiverquant.com",
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Available reports whether the client is usable at all (i.e. a token is
// configured). The engine guards every call path with this so a missing
// key short-circuits to "no data" without logging a warning per tick.
func (c *Client) Available() bool {
	return c != nil && strings.TrimSpace(c.Token) != ""
}

// get performs a GET against a Quiver path and decodes the JSON body
// into `out` (which should be a slice of row structs). The two common
// non-success surfaces — HTTP error codes and the bare subscription
// gate string — are mapped onto specific errors the caller can inspect.
func (c *Client) get(ctx context.Context, path string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.quiverquant.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("X-CSRFToken", quiverCSRFToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trader/1.0")
	// Mirror the congress package: short retry bounded to absorb the
	// occasional 502/504 during Quiver deploys without letting a blip
	// wipe an ingest cycle.
	resp, err := httpx.DoWithRetry(ctx, c.HTTP, req, httpx.RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("quiver %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("quiver %s: read: %w", path, err)
	}
	// Subscription-gate detection has to run *before* the generic 4xx
	// path. Quiver has shipped this sentinel in three different ways
	// over the years and we've observed all of them in the wild:
	//   1. HTTP 200 with a bare JSON string body.
	//   2. HTTP 200 with the bare (unquoted) string body.
	//   3. HTTP 403 with a `{"detail": "Upgrade your subscription..."}`
	//      body — the current behaviour as of this writing.
	// Mapping every shape to ErrSubscriptionRequired lets the engine
	// log it at debug once instead of warning every tick.
	if isSubscriptionGate(body) {
		return ErrSubscriptionRequired
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("quiver %s: %s: %s", path, resp.Status, summary(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("quiver %s: decode: %w", path, err)
	}
	return nil
}

// isSubscriptionGate reports whether a Quiver response body is the
// "upgrade your subscription" sentinel in any of its observed shapes.
// We sniff the body rather than parse it because Quiver's gate can
// arrive as a 200 (naked string or JSON-string) or a 403 with
// `{"detail": "..."}`, and we want one code path to handle all of
// them.
func isSubscriptionGate(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	if trimmed == subscriptionGateMessage || trimmed == `"`+subscriptionGateMessage+`"` {
		return true
	}
	// Fast substring check before a full JSON decode. The sentinel
	// message is specific enough that a raw substring match on the
	// body is a reliable signal; it avoids allocating a map just to
	// peek at a single field when a caller is on a limited plan.
	if strings.Contains(trimmed, subscriptionGateMessage) {
		return true
	}
	return false
}

func summary(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// flexFloat accepts JSON numbers *or* JSON strings that contain a
// number. Quiver's endpoints have shipped a single column as both
// shapes at various points (e.g. lobbying `Amount` flipped from
// float64 to "$12,345" formatted string without warning), so every
// money/count column that we care about is decoded through this
// type rather than plain float64. Strings are stripped of the
// usual formatting noise ("$", commas, whitespace) before parsing;
// unparseable strings decode as 0 rather than failing the whole
// batch.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	// JSON number — fast path, no allocation.
	if s[0] != '"' {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = flexFloat(v)
		return nil
	}
	// JSON string. Strip the quotes, dollar signs, commas, and any
	// trailing formatting characters (percent signs occasionally
	// appear on growth fields).
	s = strings.Trim(s, `"`)
	s = strings.TrimSpace(s)
	if s == "" {
		*f = 0
		return nil
	}
	cleaned := strings.NewReplacer("$", "", ",", "", " ", "", "%", "").Replace(s)
	if cleaned == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		// Unparseable string is treated as zero rather than aborting
		// the decode — losing one row is better than losing the
		// whole batch over a single exotic format.
		*f = 0
		return nil
	}
	*f = flexFloat(v)
	return nil
}

func (f flexFloat) float() float64 { return float64(f) }

// ------------------------------- Insiders ---------------------------------

// insiderRow is a permissive decoder for the /beta/live/insiders schema.
// Quiver has shipped multiple field variants over time (Shares vs Size,
// Price vs PricePerShare, Date vs FileDate). We accept the common ones
// and derive side/value from whichever is present.
type insiderRow struct {
	Ticker                string    `json:"Ticker"`
	Name                  string    `json:"Name"`
	Title                 string    `json:"Title"`
	Shares                flexFloat `json:"Shares"`
	SharesOwnedFollowing  flexFloat `json:"SharesOwnedFollowing"`
	PricePerShare         flexFloat `json:"PricePerShare"`
	Price                 flexFloat `json:"Price"`
	Value                 flexFloat `json:"Value"`
	TransactionCode       string    `json:"TransactionCode"`
	AcquiredDisposedCode  string    `json:"AcquiredDisposedCode"`
	Date                  string    `json:"Date"`
	FileDate              string    `json:"FileDate"`
}

// FetchInsiders returns recent Form 4 insider trades. `since` filters by
// file date; rows older than since are dropped to avoid re-ingesting the
// historical tail on every tick.
func (c *Client) FetchInsiders(ctx context.Context, since time.Time) ([]domain.InsiderTrade, error) {
	var rows []insiderRow
	if err := c.get(ctx, "/beta/live/insiders", &rows); err != nil {
		return nil, err
	}
	out := make([]domain.InsiderTrade, 0, len(rows))
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if sym == "" {
			continue
		}
		side := normaliseInsiderSide(r.AcquiredDisposedCode, r.TransactionCode)
		if side == "" {
			continue
		}
		transacted := parseDate(r.Date)
		filed := parseDate(r.FileDate)
		if transacted.IsZero() && filed.IsZero() {
			continue
		}
		if transacted.IsZero() {
			transacted = filed
		}
		if filed.IsZero() {
			filed = transacted
		}
		if !since.IsZero() && filed.Before(since) {
			continue
		}
		price := r.PricePerShare.float()
		if price == 0 {
			price = r.Price.float()
		}
		shares := int64(math.Round(math.Abs(r.Shares.float())))
		value := int64(math.Round(math.Abs(r.Value.float())))
		if value == 0 && shares > 0 && price > 0 {
			value = int64(math.Round(float64(shares) * price))
		}
		t := domain.InsiderTrade{
			Symbol:       sym,
			InsiderName:  strings.TrimSpace(r.Name),
			InsiderTitle: strings.TrimSpace(r.Title),
			Side:         side,
			Shares:       shares,
			PriceCents:   domain.Money(int64(math.Round(price * 100))),
			ValueUSD:     value,
			TransactedAt: transacted,
			FiledAt:      filed,
			Source:       "quiver",
		}
		t.RawHash = hash("insider", sym, t.InsiderName, string(t.Side), transacted, shares, value)
		out = append(out, t)
	}
	return out, nil
}

// normaliseInsiderSide maps Quiver's Form 4 transaction hints onto our
// SideBuy / SideSell enum. Acquired/Disposed is the authoritative flag
// on the SEC filing ("A" = acquired, "D" = disposed); TransactionCode
// is the fallback for feeds that omit it.
func normaliseInsiderSide(acqDisp, txCode string) domain.Side {
	switch strings.ToUpper(strings.TrimSpace(acqDisp)) {
	case "A":
		return domain.SideBuy
	case "D":
		return domain.SideSell
	}
	switch strings.ToUpper(strings.TrimSpace(txCode)) {
	case "P", "A":
		return domain.SideBuy
	case "S", "D", "F":
		return domain.SideSell
	}
	return ""
}

// ---------------------------- Off-exchange --------------------------------

type offExchRow struct {
	Ticker            string    `json:"Ticker"`
	Date              string    `json:"Date"`
	ShortVolume       flexFloat `json:"ShortVolume"`
	TotalVolume       flexFloat `json:"TotalVolume"`
	ShortExemptVolume flexFloat `json:"ShortExemptVolume"`
}

// FetchOffExchange returns per-symbol daily off-exchange short volume
// rows. When the `live` view omits the Date (it sometimes returns the
// latest row per symbol without an explicit date), we stamp "today" so
// downstream dedup still works.
func (c *Client) FetchOffExchange(ctx context.Context) ([]domain.ShortVolume, error) {
	var rows []offExchRow
	if err := c.get(ctx, "/beta/live/offexchange", &rows); err != nil {
		return nil, err
	}
	out := make([]domain.ShortVolume, 0, len(rows))
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if sym == "" {
			continue
		}
		day := parseDate(r.Date)
		if day.IsZero() {
			day = today
		}
		day = day.UTC().Truncate(24 * time.Hour)
		total := int64(math.Round(r.TotalVolume.float()))
		short := int64(math.Round(r.ShortVolume.float()))
		var ratio float64
		if total > 0 {
			ratio = float64(short) / float64(total)
		}
		out = append(out, domain.ShortVolume{
			Symbol:            sym,
			Day:               day,
			ShortVolume:       short,
			TotalVolume:       total,
			ShortExemptVolume: int64(math.Round(r.ShortExemptVolume.float())),
			ShortRatio:        ratio,
			Source:            "quiver",
		})
	}
	return out, nil
}

// --------------------------- WallStreetBets -------------------------------

type wsbRow struct {
	Ticker    string    `json:"Ticker"`
	Mentions  flexFloat `json:"Mentions"`
	Rank      flexFloat `json:"Rank"`
	Sentiment flexFloat `json:"Sentiment"`
	Date      string    `json:"Date"`
	Time      int64     `json:"Time"` // ms epoch, when present
}

// FetchWSB returns the latest WallStreetBets mention rollup. Buckets
// may be daily or intra-day depending on Quiver's current rollup
// window; we keep whatever they send.
func (c *Client) FetchWSB(ctx context.Context) ([]domain.SocialPost, error) {
	var rows []wsbRow
	if err := c.get(ctx, "/beta/live/wallstreetbets?count_all=true", &rows); err != nil {
		return nil, err
	}
	out := make([]domain.SocialPost, 0, len(rows))
	now := time.Now().UTC()
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if sym == "" {
			continue
		}
		bucket := parseDate(r.Date)
		if bucket.IsZero() && r.Time > 0 {
			bucket = time.Unix(0, r.Time*int64(time.Millisecond)).UTC()
		}
		if bucket.IsZero() {
			bucket = now
		}
		mentions := int64(math.Round(r.Mentions.float()))
		if mentions <= 0 {
			continue
		}
		p := domain.SocialPost{
			Symbol:    sym,
			Platform:  "wsb",
			Mentions:  mentions,
			Sentiment: clampFloat(r.Sentiment.float(), -1, 1),
			BucketAt:  bucket,
			Source:    "quiver",
		}
		p.RawHash = hash("wsb", sym, p.Platform, "", bucket, mentions, 0)
		out = append(out, p)
	}
	return out, nil
}

// ------------------------------ Twitter ------------------------------------

type twitterRow struct {
	Ticker    string    `json:"Ticker"`
	Date      string    `json:"Date"`
	Followers flexFloat `json:"Followers"`
	PctChange flexFloat `json:"pct_change_week"`
}

// FetchTwitter returns the latest Twitter-follower rollup per symbol.
// Quiver scrapes the corporate Twitter account's follower count; growth
// correlates (weakly) with retail attention.
func (c *Client) FetchTwitter(ctx context.Context) ([]domain.SocialPost, error) {
	var rows []twitterRow
	if err := c.get(ctx, "/beta/live/twitter", &rows); err != nil {
		return nil, err
	}
	out := make([]domain.SocialPost, 0, len(rows))
	now := time.Now().UTC()
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if sym == "" {
			continue
		}
		bucket := parseDate(r.Date)
		if bucket.IsZero() {
			bucket = now
		}
		followers := int64(math.Round(r.Followers.float()))
		if followers <= 0 {
			continue
		}
		p := domain.SocialPost{
			Symbol:    sym,
			Platform:  "twitter",
			Mentions:  0,
			Sentiment: clampFloat(r.PctChange.float()/100, -1, 1),
			Followers: followers,
			BucketAt:  bucket,
			Source:    "quiver",
		}
		p.RawHash = hash("twitter", sym, p.Platform, "", bucket, followers, 0)
		out = append(out, p)
	}
	return out, nil
}

// ------------------------------ Lobbying ----------------------------------

type lobbyingRow struct {
	Ticker     string    `json:"Ticker"`
	Client     string    `json:"Client"`
	Registrant string    `json:"Registrant"`
	Issue      string    `json:"Issue"`
	Amount     flexFloat `json:"Amount"`
	Date       string    `json:"Date"`
	Year       int       `json:"Year"`
	Period     string    `json:"Period"`
}

// FetchLobbying returns recent corporate lobbying filings. `since` is
// applied to the filed date.
func (c *Client) FetchLobbying(ctx context.Context, since time.Time) ([]domain.LobbyingEvent, error) {
	var rows []lobbyingRow
	if err := c.get(ctx, "/beta/live/lobbying", &rows); err != nil {
		return nil, err
	}
	out := make([]domain.LobbyingEvent, 0, len(rows))
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if sym == "" {
			continue
		}
		filed := parseDate(r.Date)
		if filed.IsZero() {
			continue
		}
		if !since.IsZero() && filed.Before(since) {
			continue
		}
		e := domain.LobbyingEvent{
			Symbol:     sym,
			Client:     strings.TrimSpace(r.Client),
			Registrant: strings.TrimSpace(r.Registrant),
			Issue:      strings.TrimSpace(r.Issue),
			AmountUSD:  int64(math.Round(math.Abs(r.Amount.float()))),
			FiledAt:    filed,
			Period:     strings.TrimSpace(r.Period),
			Source:     "quiver",
		}
		e.RawHash = hash("lobbying", sym, e.Registrant, e.Issue, filed, e.AmountUSD, 0)
		out = append(out, e)
	}
	return out, nil
}

// ---------------------------- Gov contracts -------------------------------

type govContractRow struct {
	Ticker      string    `json:"Ticker"`
	Agency      string    `json:"Agency"`
	Description string    `json:"Description"`
	Amount      flexFloat `json:"Amount"`
	Date        string    `json:"Date"`
}

// FetchGovContracts returns recent federal contract awards. `since` is
// applied to the award date.
func (c *Client) FetchGovContracts(ctx context.Context, since time.Time) ([]domain.GovContract, error) {
	var rows []govContractRow
	if err := c.get(ctx, "/beta/live/govcontractsall", &rows); err != nil {
		return nil, err
	}
	out := make([]domain.GovContract, 0, len(rows))
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Ticker))
		if sym == "" {
			continue
		}
		awarded := parseDate(r.Date)
		if awarded.IsZero() {
			continue
		}
		if !since.IsZero() && awarded.Before(since) {
			continue
		}
		cc := domain.GovContract{
			Symbol:      sym,
			Agency:      strings.TrimSpace(r.Agency),
			Description: strings.TrimSpace(r.Description),
			AmountUSD:   int64(math.Round(math.Abs(r.Amount.float()))),
			AwardedAt:   awarded,
			Source:      "quiver",
		}
		cc.RawHash = hash("gov", sym, cc.Agency, cc.Description, awarded, cc.AmountUSD, 0)
		out = append(out, cc)
	}
	return out, nil
}

// --------------------------------- Util -----------------------------------

// parseDate accepts the several date shapes Quiver has shipped over the
// years (YYYY-MM-DD, ISO8601, MM/DD/YYYY, unix-ms numeric). Unknown
// formats return the zero value and the caller drops the row.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	// Numeric epoch ms (wallstreetbets historical variant).
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		// Heuristic: values > 1e12 are epoch ms, else seconds.
		if n > 1_000_000_000_000 {
			return time.Unix(0, n*int64(time.Millisecond)).UTC()
		}
		return time.Unix(n, 0).UTC()
	}
	layouts := []string{
		"2006-01-02",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"01/02/2006",
		"2006/01/02",
		"January 2, 2006",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// hash produces a stable SHA-1 digest across the "identity" columns of
// a Quiver row so Insert(...ON CONFLICT(raw_hash) DO NOTHING) naturally
// dedups across ingest ticks without relying on Quiver IDs (they can
// shift when upstream rollups backfill).
func hash(kind, symbol, name, extra string, t time.Time, a, b int64) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%d|%d", kind,
		strings.ToLower(strings.TrimSpace(symbol)),
		strings.ToLower(strings.TrimSpace(name)),
		strings.ToLower(strings.TrimSpace(extra)),
		t.UTC().Format("2006-01-02T15:04:05Z"), a, b)
	return hex.EncodeToString(h.Sum(nil))
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
