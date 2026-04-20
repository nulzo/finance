package strategy

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nulzo/trader/internal/domain"
)

// SocialBuzz emits short-horizon signals from the WallStreetBets
// mention rollup. The working thesis: a ticker sitting atop r/WSB
// with aligned bullish sentiment usually gets a retail-driven bump
// over the following 24–48 hours, but the half-life is extremely
// short and the false-positive rate is high. Confidence is therefore
// capped lower than insider/politician signals, and the signal
// expires quickly.
//
// Sell signals require BOTH high mentions AND strongly negative
// sentiment — a bearish WSB chorus is rarer and somewhat more
// reliable than the bullish pump.
//
// Every generated signal carries a stable RefID of the form
//
//	social:SYMBOL:SIDE:YYYYMMDDHH
//
// so hourly buckets collapse together on re-ingest.
type SocialBuzz struct {
	// Recent resolves social posts bucketed on/after `since`.
	Recent func(ctx context.Context, since time.Time) ([]domain.SocialPost, error)
	// LookbackDur bounds the aggregation window. Defaults to 24h.
	LookbackDur time.Duration
	// MinMentions drops noise — symbols with only a handful of
	// mentions never justify a trade. Defaults to 100.
	MinMentions int64
	// ConfidenceCap is the hard ceiling on emitted confidence.
	// Defaults to 0.55 so social buzz never dominates more
	// fundamental signals during merge.
	ConfidenceCap float64
	// Now is injected for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Name implements Strategy.
func (s *SocialBuzz) Name() string { return "social_buzz" }

// Generate collapses social rollups per symbol into a single signal.
func (s *SocialBuzz) Generate(ctx context.Context) ([]domain.Signal, error) {
	if s.LookbackDur == 0 {
		s.LookbackDur = 24 * time.Hour
	}
	if s.MinMentions == 0 {
		s.MinMentions = 100
	}
	if s.ConfidenceCap == 0 {
		s.ConfidenceCap = 0.55
	}
	nowFn := s.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	now := nowFn()
	since := now.Add(-s.LookbackDur)
	posts, err := s.Recent(ctx, since)
	if err != nil {
		return nil, err
	}
	type agg struct {
		mentions  int64
		weighted  float64 // sum of mentions*sentiment
		buckets   int
		platforms map[string]struct{}
	}
	bySym := map[string]*agg{}
	for _, p := range posts {
		sym := strings.ToUpper(strings.TrimSpace(p.Symbol))
		if !ValidTicker(sym) {
			continue
		}
		// Twitter follower-count deltas are tracked as
		// sentiment-only rows with zero mentions; skip them for
		// this WSB-flavoured signal (they feed LLM context instead).
		if p.Mentions <= 0 {
			continue
		}
		a := bySym[sym]
		if a == nil {
			a = &agg{platforms: map[string]struct{}{}}
			bySym[sym] = a
		}
		a.mentions += p.Mentions
		a.weighted += float64(p.Mentions) * p.Sentiment
		a.buckets++
		a.platforms[p.Platform] = struct{}{}
	}
	out := make([]domain.Signal, 0, len(bySym))
	bucket := now.UTC().Format("2006010215")
	for sym, a := range bySym {
		if a.mentions < s.MinMentions {
			continue
		}
		avgSent := 0.0
		if a.mentions > 0 {
			avgSent = a.weighted / float64(a.mentions)
		}
		// Strong chorus in one direction > weak chorus, and more
		// buckets (sustained over several hours) > one spike.
		side := domain.SideBuy
		if avgSent < -0.15 {
			side = domain.SideSell
		} else if avgSent < 0.15 {
			continue // no directional conviction, skip
		}
		// Magnitude grows with mention volume on a log curve so
		// a 10k-mention day isn't literally 100x a 100-mention
		// day. Signed by side.
		mag := math.Tanh(math.Log10(float64(a.mentions)/100) / 2)
		if mag < 0 {
			mag = 0
		}
		score := mag
		if side == domain.SideSell {
			score = -mag
		}
		conf := math.Tanh(math.Abs(avgSent)*2) * mag
		if conf > s.ConfidenceCap {
			conf = s.ConfidenceCap
		}
		if conf < 0.1 {
			// Below the confidence floor every strategy uses;
			// skip instead of emitting noise.
			continue
		}
		platforms := make([]string, 0, len(a.platforms))
		for pl := range a.platforms {
			platforms = append(platforms, pl)
		}
		out = append(out, domain.Signal{
			Kind:       domain.SignalKindSocial,
			Symbol:     sym,
			Side:       side,
			Score:      round2(score),
			Confidence: round2(conf),
			Reason: fmt.Sprintf("%d mentions across %d bucket(s) on %s; avg sentiment %+.2f",
				a.mentions, a.buckets, strings.Join(platforms, ","), avgSent),
			RefID:     fmt.Sprintf("social:%s:%s:%s", sym, side, bucket),
			ExpiresAt: now.Add(6 * time.Hour),
		})
	}
	return out, nil
}
