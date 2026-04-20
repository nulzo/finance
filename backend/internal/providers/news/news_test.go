package news_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/providers/news"
)

type fakeSource struct {
	items []domain.NewsItem
	err   error
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Fetch(_ context.Context, _ time.Time) ([]domain.NewsItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func TestAggregator_DedupesByURL(t *testing.T) {
	now := time.Now()
	src1 := &fakeSource{items: []domain.NewsItem{{URL: "a", Title: "A", PubAt: now}}}
	src2 := &fakeSource{items: []domain.NewsItem{
		{URL: "a", Title: "A", PubAt: now},
		{URL: "b", Title: "B", PubAt: now},
	}}
	agg := &news.Aggregator{Sources: []news.Source{src1, src2}, Log: zerolog.Nop()}
	out, err := agg.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestAggregator_SurvivesFailingSource(t *testing.T) {
	good := &fakeSource{items: []domain.NewsItem{{URL: "ok", Title: "T", PubAt: time.Now()}}}
	bad := &fakeSource{err: context.DeadlineExceeded}
	agg := &news.Aggregator{Sources: []news.Source{bad, good}, Log: zerolog.Nop()}
	out, err := agg.Fetch(context.Background(), time.Time{})
	require.NoError(t, err)
	assert.Len(t, out, 1)
}

func TestRSS_ParsesFeed(t *testing.T) {
	t.Skip("network-dependent; covered by manual fetch tests")
}
