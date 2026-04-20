package market_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/providers/market"
)

type stubProvider struct {
	quote *domain.Quote
	err   error
	calls int32
}

func (s *stubProvider) Quote(ctx context.Context, symbol string) (*domain.Quote, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.err != nil {
		return nil, s.err
	}
	q := *s.quote
	q.Symbol = symbol
	return &q, nil
}

func TestCachedProvider_UsesCache(t *testing.T) {
	p := &stubProvider{quote: &domain.Quote{Price: decimal.NewFromFloat(100)}}
	c := market.NewCachedProvider(p, nil, time.Second)
	for i := 0; i < 5; i++ {
		q, err := c.Quote(context.Background(), "AAPL")
		require.NoError(t, err)
		assert.Equal(t, "100", q.Price.String())
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&p.calls))
}

func TestCachedProvider_FallsBack(t *testing.T) {
	primary := &stubProvider{err: errors.New("nope")}
	fallback := &stubProvider{quote: &domain.Quote{Price: decimal.NewFromFloat(42)}}
	c := market.NewCachedProvider(primary, fallback, time.Second)
	q, err := c.Quote(context.Background(), "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "42", q.Price.String())
}

func TestCachedProvider_ErrorsWhenBothFail(t *testing.T) {
	primary := &stubProvider{err: errors.New("p")}
	fallback := &stubProvider{err: errors.New("f")}
	c := market.NewCachedProvider(primary, fallback, time.Second)
	_, err := c.Quote(context.Background(), "AAPL")
	require.Error(t, err)
}

func TestSynthetic_Deterministic(t *testing.T) {
	s := market.NewSynthetic()
	q1, err := s.Quote(context.Background(), "AAPL")
	require.NoError(t, err)
	q2, err := s.Quote(context.Background(), "AAPL")
	require.NoError(t, err)
	// Same minute bucket → identical price.
	assert.True(t, q1.Price.Equal(q2.Price), "expected stable price within bucket, got %s vs %s", q1.Price, q2.Price)
	// Different symbol → almost certainly different price.
	q3, err := s.Quote(context.Background(), "MSFT")
	require.NoError(t, err)
	assert.False(t, q1.Price.Equal(q3.Price))
	assert.True(t, q1.Price.GreaterThan(decimal.Zero))
}

func TestCachedChain_UsesAnyProviderInOrder(t *testing.T) {
	a := &stubProvider{err: errors.New("a")}
	b := &stubProvider{err: errors.New("b")}
	c := &stubProvider{quote: &domain.Quote{Price: decimal.NewFromFloat(7)}}
	chain := market.NewCachedChain(time.Second, a, b, c)
	q, err := chain.Quote(context.Background(), "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "7", q.Price.String())
}
