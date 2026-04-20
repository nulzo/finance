package risk_test

import (
	"context"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/risk"
)

// UpdateLimits must swap the whole struct atomically and Approve
// calls right after must reflect the new values (blacklist, caps,
// etc.). Guards the control-API contract.
func TestRisk_UpdateLimitsAppliesLive(t *testing.T) {
	e := risk.NewEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(500)})
	req := func() risk.Request {
		return risk.Request{
			Symbol: "AAPL", Side: domain.SideBuy,
			TargetUSD:     decimal.NewFromInt(200),
			Price:         decimal.NewFromInt(10),
			PortfolioCash: decimal.NewFromInt(10_000),
		}
	}
	r := e.Approve(context.Background(), req())
	require.True(t, r.Approved)

	// Flip the blacklist on. The same request must now reject.
	l := e.GetLimits()
	l.Blacklist = []string{"AAPL"}
	e.UpdateLimits(l)
	r = e.Approve(context.Background(), req())
	assert.False(t, r.Approved)
	assert.Contains(t, r.Reason, "blacklist")
}

// GetLimits returns an independent copy — mutating the caller's copy
// must not leak back into the engine.
func TestRisk_GetLimitsIsSnapshot(t *testing.T) {
	e := risk.NewEngine(risk.Limits{Blacklist: []string{"GME"}})
	l := e.GetLimits()
	l.Blacklist[0] = "TSLA"
	live := e.GetLimits()
	assert.Equal(t, "GME", live.Blacklist[0], "engine snapshot must not share memory with caller")
}

// Blacklist helpers are idempotent and case-insensitive.
func TestRisk_BlacklistHelpersIdempotent(t *testing.T) {
	e := risk.NewEngine(risk.Limits{})
	e.AddBlacklist("aapl")
	e.AddBlacklist("AAPL")
	e.AddBlacklist("  aapl  ")
	l := e.GetLimits()
	assert.Len(t, l.Blacklist, 1)
	e.RemoveBlacklist("aapl")
	assert.Empty(t, e.GetLimits().Blacklist)
	e.RemoveBlacklist("nope") // no-op, must not panic
}

// The mutex must actually serialise concurrent updates + reads.
// Without the lock the race detector trips on this test (go test -race).
func TestRisk_ConcurrentAccess(t *testing.T) {
	e := risk.NewEngine(risk.Limits{MaxOrderUSD: decimal.NewFromInt(500)})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			l := e.GetLimits()
			l.MaxOrderUSD = decimal.NewFromInt(int64(100 + i))
			e.UpdateLimits(l)
		}()
		go func() {
			defer wg.Done()
			_ = e.Approve(context.Background(), risk.Request{
				Symbol: "AAPL", Side: domain.SideBuy,
				TargetUSD: decimal.NewFromInt(50), Price: decimal.NewFromInt(10),
				PortfolioCash: decimal.NewFromInt(10_000),
			})
		}()
	}
	wg.Wait()
}
