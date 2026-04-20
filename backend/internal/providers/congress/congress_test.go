package congress_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/providers/congress"
)

const sampleCapitol = `{
  "data": [
    {"politician": "Jane Doe", "chamber": "senate", "ticker": "AAPL", "issuer": "Apple Inc", "txType": "Purchase", "sizeMin": 50000, "sizeMax": 100000, "txDate": "2026-04-01", "filed": "2026-04-10"},
    {"politician": "John Doe", "chamber": "house", "ticker": "MSFT", "issuer": "Microsoft", "txType": "Sale", "sizeMin": 15000, "sizeMax": 50000, "txDate": "2026-04-02", "filed": "2026-04-10"}
  ]
}`

func TestCapitolTrades_Fetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleCapitol))
	}))
	defer srv.Close()
	c := congress.NewCapitolTrades(srv.URL)
	trades, err := c.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Len(t, trades, 2)
	assert.Equal(t, "AAPL", trades[0].Symbol)
	assert.Equal(t, "capitoltrades", trades[0].Source)
	assert.NotEmpty(t, trades[0].RawHash)
}

const sampleLambdaFin = `{
  "trades": [
    {"symbol": "VA", "representative": "Jefferson Shreve", "transactionDate": "2026-03-15", "disclosureDate": "2026-03-20", "type": "Purchase", "amount": "$1,001 - $15,000", "owner": "Self", "chamber": "house"},
    {"symbol": "NVDA", "representative": "Jane Smith", "transactionDate": "2026-03-14", "disclosureDate": "2026-03-18", "type": "Sale", "amount": "$15,001 - $50,000", "owner": "Spouse", "chamber": "senate"},
    {"symbol": "N/A", "representative": "No Ticker", "transactionDate": "2026-03-13", "disclosureDate": "2026-03-17", "type": "Purchase", "amount": "$1 - $1,000", "chamber": "house"},
    {"symbol": "AMZN", "representative": "Junk Tx", "transactionDate": "2026-03-12", "disclosureDate": "2026-03-16", "type": "Exchange", "amount": "$1,001 - $15,000", "chamber": "house"}
  ]
}`

func TestLambdaFinance_Fetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleLambdaFin))
	}))
	defer srv.Close()
	c := congress.NewLambdaFinance()
	c.BaseURL = srv.URL
	trades, err := c.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	// N/A ticker and Exchange tx type should both be skipped.
	require.Len(t, trades, 2)
	assert.Equal(t, "VA", trades[0].Symbol)
	assert.Equal(t, "lambdafin", trades[0].Source)
	assert.Equal(t, int64(1001), trades[0].AmountMinUSD)
	assert.Equal(t, int64(15000), trades[0].AmountMaxUSD)
	assert.Equal(t, "NVDA", trades[1].Symbol)
	assert.Equal(t, "senate", trades[1].Chamber)
	assert.NotEmpty(t, trades[0].RawHash)
}

func TestLambdaFinance_HonoursSince(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleLambdaFin))
	}))
	defer srv.Close()
	c := congress.NewLambdaFinance()
	c.BaseURL = srv.URL
	// `since` after the latest disclosure date should filter everything out.
	trades, err := c.Fetch(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Empty(t, trades)
}

// Quiver uses Django REST framework's "Token <key>" scheme and a
// hard-coded X-CSRFToken header that the official Python client also
// sends. Sending "Bearer" causes the upstream to hang; locking the
// headers with a test prevents that regression.
func TestQuiver_UsesTokenAuthAndCSRFHeader(t *testing.T) {
	sample := `[
		{"Representative": "Jane Doe", "Ticker": "AAPL", "Transaction": "Purchase",
		 "Range": "$1,001 - $15,000", "TransactionDate": "2026-04-01", "ReportDate": "2026-04-10",
		 "House": "Representatives"},
		{"Senator": "John Doe", "Ticker": "MSFT", "Transaction": "Sale",
		 "Range": "$15,001 - $50,000", "TransactionDate": "2026-04-02", "ReportDate": "2026-04-11"}
	]`
	var gotAuth, gotCSRF, gotAccept string
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotAuth = r.Header.Get("Authorization")
		gotCSRF = r.Header.Get("X-CSRFToken")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	q := congress.NewQuiver("test-tok-123")
	q.BaseURL = srv.URL
	trades, err := q.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	// Two endpoints called (congress + senate).
	assert.Equal(t, 2, hits)
	assert.Equal(t, "Token test-tok-123", gotAuth, "must use DRF 'Token <key>' scheme, not Bearer")
	assert.NotEmpty(t, gotCSRF, "X-CSRFToken must be set to avoid upstream stalls")
	assert.Equal(t, "application/json", gotAccept)
	assert.NotEmpty(t, trades)
}

// A 4xx on one endpoint shouldn't blank the whole fetch – the other
// endpoint's rows should still come through.
func TestQuiver_SurvivesOneEndpointFailing(t *testing.T) {
	sample := `[
		{"Representative": "Jane Doe", "Ticker": "AAPL", "Transaction": "Purchase",
		 "Range": "$1,001 - $15,000", "TransactionDate": "2026-04-01", "ReportDate": "2026-04-10"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "senate") {
			http.Error(w, "upstream", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()
	q := congress.NewQuiver("k")
	q.BaseURL = srv.URL
	trades, err := q.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Len(t, trades, 1)
	assert.Equal(t, "AAPL", trades[0].Symbol)
}

// When Quiver returns a 401 on both endpoints the caller should see a
// concrete error, not a silent empty result.
func TestQuiver_BothEndpointsFailReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"Invalid token."}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	q := congress.NewQuiver("bad")
	q.BaseURL = srv.URL
	_, err := q.Fetch(context.Background(), time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestQuiver_EmptyTokenReturnsError(t *testing.T) {
	q := congress.NewQuiver("")
	_, err := q.Fetch(context.Background(), time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestAggregator_DedupesAndSurvivesFailures(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleCapitol))
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", 500)
	}))
	defer bad.Close()

	agg := &congress.Aggregator{
		Sources: []congress.Source{
			congress.NewCapitolTrades(ok.URL),
			congress.NewCapitolTrades(bad.URL), // same source but broken — dedupe keeps unique
		},
		Log: zerolog.Nop(),
	}
	trades, err := agg.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	assert.Len(t, trades, 2)
}
