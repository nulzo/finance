package quiver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nulzo/trader/internal/domain"
)

// newServer spins up a mock Quiver endpoint for one specific path. We
// verify the client sends the DRF-style Authorization header and the
// same CSRF header the upstream Django middleware expects.
func newServer(t *testing.T, path, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token test-key" {
			t.Errorf("auth header: got %q want Token test-key", got)
		}
		if got := r.Header.Get("X-CSRFToken"); got == "" {
			t.Errorf("missing csrf header; quiver middleware stalls without it")
		}
		if r.URL.Path != path {
			// Expected when we hit a path we haven't stubbed — log
			// and fall through so Client surfaces a 404.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{Token: "test-key", BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestFetchInsiders_DecodesQuiverShape(t *testing.T) {
	body := `[
		{"Ticker":"AAPL","Name":"Tim Cook","Title":"CEO","Shares":1000,"PricePerShare":185.10,"AcquiredDisposedCode":"A","Date":"2026-04-15","FileDate":"2026-04-16"},
		{"Ticker":"TSLA","Name":"Elon Musk","Title":"CEO","Shares":5000,"Price":210.5,"AcquiredDisposedCode":"D","Date":"2026-04-10","FileDate":"2026-04-12"},
		{"Ticker":"","Name":"missing ticker","Shares":100,"AcquiredDisposedCode":"A","Date":"2026-04-01"}
	]`
	c := newServer(t, "/beta/live/insiders", body)
	trades, err := c.FetchInsiders(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("want 2 trades (blank-ticker row dropped), got %d", len(trades))
	}
	if trades[0].Symbol != "AAPL" || trades[0].Side != domain.SideBuy || trades[0].ValueUSD == 0 {
		t.Errorf("aapl decode: %+v", trades[0])
	}
	if trades[1].Side != domain.SideSell {
		t.Errorf("tsla disposed should be sell, got %s", trades[1].Side)
	}
	for _, tr := range trades {
		if tr.RawHash == "" {
			t.Errorf("raw hash should be populated: %+v", tr)
		}
	}
}

func TestFetchWSB_SkipsZeroMentionRows(t *testing.T) {
	body := `[
		{"Ticker":"GME","Mentions":5000,"Sentiment":0.72,"Date":"2026-04-19"},
		{"Ticker":"AMC","Mentions":0,"Sentiment":0.3,"Date":"2026-04-19"},
		{"Ticker":"XYZ","Mentions":100,"Sentiment":-0.6,"Date":"2026-04-19"}
	]`
	c := newServer(t, "/beta/live/wallstreetbets", body)
	posts, err := c.FetchWSB(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("want 2 (zero-mention dropped), got %d", len(posts))
	}
	for _, p := range posts {
		if p.Platform != "wsb" {
			t.Errorf("platform should be wsb, got %s", p.Platform)
		}
	}
}

func TestFetchOffExchange_ComputesShortRatio(t *testing.T) {
	body := `[
		{"Ticker":"AAPL","Date":"2026-04-19","ShortVolume":4000000,"TotalVolume":10000000,"ShortExemptVolume":50000}
	]`
	c := newServer(t, "/beta/live/offexchange", body)
	rows, err := c.FetchOffExchange(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.ShortRatio < 0.399 || r.ShortRatio > 0.401 {
		t.Errorf("short ratio should be ~0.40, got %.4f", r.ShortRatio)
	}
}

func TestGet_SubscriptionGateIsSoftFailure(t *testing.T) {
	body := `"Upgrade your subscription plan to access this dataset."`
	c := newServer(t, "/beta/live/lobbying", body)
	_, err := c.FetchLobbying(context.Background(), time.Time{})
	if err == nil {
		t.Fatalf("expected subscription-gate error, got nil")
	}
	if !errors.Is(err, ErrSubscriptionRequired) {
		t.Fatalf("expected ErrSubscriptionRequired, got %v", err)
	}
}

func TestFetchLobbying_SinceFilter(t *testing.T) {
	body := `[
		{"Ticker":"AAPL","Registrant":"Cravath","Issue":"App Store","Amount":500000,"Date":"2026-03-01"},
		{"Ticker":"AAPL","Registrant":"Cravath","Issue":"Privacy","Amount":300000,"Date":"2025-01-01"}
	]`
	c := newServer(t, "/beta/live/lobbying", body)
	rows, err := c.FetchLobbying(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row after since filter, got %d", len(rows))
	}
}

func TestFetchGovContracts_ParsesDollarAmount(t *testing.T) {
	body := `[
		{"Ticker":"LMT","Agency":"DoD","Description":"F-35","Amount":5000000000,"Date":"2026-04-01"}
	]`
	c := newServer(t, "/beta/live/govcontractsall", body)
	rows, err := c.FetchGovContracts(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 contract, got %d", len(rows))
	}
	if rows[0].AmountUSD != 5_000_000_000 {
		t.Errorf("amount usd: %d", rows[0].AmountUSD)
	}
}

// TestFetchLobbying_AmountAsString guards the regression we saw in
// prod where Quiver shipped `Amount` as a dollar-formatted JSON string
// instead of a number. flexFloat is supposed to accept either shape;
// if anyone downgrades the field back to float64 the unmarshal fails
// the whole batch with "cannot unmarshal string into ...".
func TestFetchLobbying_AmountAsString(t *testing.T) {
	body := `[
		{"Ticker":"AAPL","Registrant":"Cravath","Issue":"App Store","Amount":"$1,250,000","Date":"2026-03-01"},
		{"Ticker":"MSFT","Registrant":"DLA","Issue":"AI policy","Amount":"750000.50","Date":"2026-03-05"},
		{"Ticker":"GOOG","Registrant":"Latham","Issue":"Antitrust","Amount":null,"Date":"2026-03-10"}
	]`
	c := newServer(t, "/beta/live/lobbying", body)
	rows, err := c.FetchLobbying(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (incl. null-amount), got %d", len(rows))
	}
	byTicker := map[string]int64{}
	for _, r := range rows {
		byTicker[r.Symbol] = r.AmountUSD
	}
	if byTicker["AAPL"] != 1_250_000 {
		t.Errorf("aapl amount: got %d want 1250000", byTicker["AAPL"])
	}
	if byTicker["MSFT"] != 750_001 { // rounded from 750000.50
		t.Errorf("msft amount: got %d want 750001", byTicker["MSFT"])
	}
	if byTicker["GOOG"] != 0 {
		t.Errorf("goog null amount should decode to 0, got %d", byTicker["GOOG"])
	}
}

// TestGet_SubscriptionGate_On403 covers the current Quiver behaviour:
// gated endpoints return HTTP 403 with a DRF-style detail body. The
// client must treat that as ErrSubscriptionRequired, not a generic
// 4xx, so the engine's once-per-run suppression kicks in instead of
// warn-logging every tick.
func TestGet_SubscriptionGate_On403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Upgrade your subscription plan to access this dataset."}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{Token: "test-key", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.FetchInsiders(context.Background(), time.Time{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrSubscriptionRequired) {
		t.Fatalf("expected ErrSubscriptionRequired on 403, got %v", err)
	}
}

// TestGet_Generic4xxNotGate ensures we don't accidentally swallow
// legitimate 4xx errors (e.g. 401 from a revoked key) as subscription
// gates — only the upgrade-plan sentinel should be demoted.
func TestGet_Generic4xxNotGate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid token."}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{Token: "test-key", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.FetchInsiders(context.Background(), time.Time{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if errors.Is(err, ErrSubscriptionRequired) {
		t.Fatalf("401 should NOT be classified as subscription gate")
	}
}

// TestFlexFloat_Decode documents every shape we've observed in the
// wild so that a regression is caught at the unit-test level rather
// than by a warn-log in prod.
func TestFlexFloat_Decode(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{`123`, 123},
		{`123.45`, 123.45},
		{`"123"`, 123},
		{`"$1,250,000"`, 1250000},
		{`"  42.0 "`, 42},
		{`"12.5%"`, 12.5},
		{`null`, 0},
		{`""`, 0},
		{`"not a number"`, 0}, // permissive: row keeps going
	}
	for _, tc := range cases {
		var f flexFloat
		if err := f.UnmarshalJSON([]byte(tc.in)); err != nil {
			t.Errorf("%s: unexpected err: %v", tc.in, err)
			continue
		}
		if got := f.float(); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestUnavailable_MissingToken(t *testing.T) {
	c := New("")
	if c.Available() {
		t.Errorf("empty token should be unavailable")
	}
	c2 := New("x")
	if !c2.Available() {
		t.Errorf("non-empty token should be available")
	}
}
