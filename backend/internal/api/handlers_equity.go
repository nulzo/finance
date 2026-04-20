package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// equityLive returns the freshest portfolio valuation: cash + open
// positions marked to market, plus realised / unrealised P&L and a
// per-position breakdown. The computation is done live against the
// QuoteProvider rather than reading the latest snapshot so the UI
// reflects up-to-the-second prices even between snapshot ticks.
//
// Response shape mirrors equity.Valuation; the frontend Overview and
// Analytics pages consume the same JSON.
func (s *Server) equityLive(c *gin.Context) {
	if s.Deps.Engine == nil {
		s.serverError(c, errNoEngine)
		return
	}
	v, err := s.Deps.Engine.LiveEquity(c.Request.Context())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// equityHistory returns the persisted time-series of equity snapshots
// for the portfolio. Accepts `since` (RFC3339 or duration like "24h")
// and `limit`; default window is 30 days (~8640 snapshots at
// 5-minute granularity, capped by the repo at 5000).
//
// Rows come back ordered oldest-first so the frontend can pipe them
// straight into recharts' `data` prop without re-sorting.
func (s *Server) equityHistory(c *gin.Context) {
	id := c.Param("id")
	since := parseSince(c, 30*24*time.Hour)
	limit := parseLimit(c, 2000)
	rows, err := s.Deps.Store.Equity.ListSince(c.Request.Context(), id, since, limit)
	if err != nil {
		s.serverError(c, err)
		return
	}
	// Gin serialises nil slices as null; return [] so the frontend
	// doesn't have to special-case "no history yet".
	if rows == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	c.JSON(http.StatusOK, rows)
}

// positionsPnL returns the per-position unrealised P&L table, keyed
// by symbol. It's a denormalised shortcut so the frontend doesn't
// have to join `/positions` with per-symbol quote calls — useful for
// the "open positions P&L" widget on the Overview page.
func (s *Server) positionsPnL(c *gin.Context) {
	if s.Deps.Engine == nil {
		s.serverError(c, errNoEngine)
		return
	}
	v, err := s.Deps.Engine.LiveEquity(c.Request.Context())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, v.Positions)
}

// analyticsSummary bundles the numbers the Overview and Analytics
// pages need for header stat cards into a single JSON payload. This
// keeps the front-page to one round-trip instead of three.
//
// The shape is deliberately flat — nested objects force the UI into
// `data?.x?.y` guards that are easy to get wrong. If the engine is
// missing we still return cash + realised so the dashboard renders
// instead of showing a server-error toast on fresh installs.
type analyticsSummary struct {
	PortfolioID      string `json:"portfolio_id"`
	CashCents        int64  `json:"cash_cents"`
	PositionsCost    int64  `json:"positions_cost"`
	PositionsMTM     int64  `json:"positions_mtm"`
	RealizedCents    int64  `json:"realized_cents"`
	UnrealizedCents  int64  `json:"unrealized_cents"`
	EquityCents      int64  `json:"equity_cents"`
	OpenPositions    int    `json:"open_positions"`
	PricedPositions  int    `json:"priced_positions"`
	RealizedToday    int64  `json:"realized_today_cents"`
	RealizedWeek     int64  `json:"realized_week_cents"`
	RealizedMonth    int64  `json:"realized_month_cents"`
	// DayChangeCents approximates the intraday equity delta by
	// diffing the latest snapshot against the first snapshot >=
	// UTC midnight. If we have <2 snapshots today, returns 0 with
	// `day_change_available=false` so the UI can show a muted
	// placeholder instead of a fake "$0".
	DayChangeCents     int64 `json:"day_change_cents"`
	DayChangeAvailable bool  `json:"day_change_available"`
}

func (s *Server) analyticsSummaryHandler(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	out := analyticsSummary{PortfolioID: id}

	// Live valuation supplies cash / cost / mtm / realised (cumulative)
	// / unrealised / equity.
	if s.Deps.Engine != nil {
		if v, err := s.Deps.Engine.LiveEquity(ctx); err == nil && v != nil {
			out.CashCents = int64(v.CashCents)
			out.PositionsCost = int64(v.PositionsCost)
			out.PositionsMTM = int64(v.PositionsMTM)
			out.RealizedCents = int64(v.RealizedCents)
			out.UnrealizedCents = int64(v.UnrealizedCents)
			out.EquityCents = int64(v.EquityCents)
			out.OpenPositions = v.OpenPositions
			out.PricedPositions = v.PricedPositions
		}
	}

	// Realised windowed sums. Each is independent so a single failure
	// doesn't poison the whole response. All windows are UTC-aligned
	// to match the rest of the app's P&L conventions.
	now := time.Now().UTC()
	todayStart := now.Truncate(24 * time.Hour)
	if v, err := s.Deps.Store.Realized.SumSince(ctx, id, todayStart); err == nil {
		out.RealizedToday = int64(v)
	}
	if v, err := s.Deps.Store.Realized.SumSince(ctx, id, now.Add(-7*24*time.Hour)); err == nil {
		out.RealizedWeek = int64(v)
	}
	if v, err := s.Deps.Store.Realized.SumSince(ctx, id, now.Add(-30*24*time.Hour)); err == nil {
		out.RealizedMonth = int64(v)
	}

	// Day-change: diff latest snapshot against the first snapshot
	// taken today. Uses the persisted history rather than recomputing
	// so the number is stable across ticks (live equity wobbles with
	// the cached quote TTL; the chart needs a point-in-time anchor).
	if s.Deps.Store.Equity != nil {
		rows, err := s.Deps.Store.Equity.ListSince(ctx, id, todayStart, 0)
		if err == nil && len(rows) >= 2 {
			first := rows[0]
			last := rows[len(rows)-1]
			out.DayChangeCents = int64(last.EquityCents - first.EquityCents)
			out.DayChangeAvailable = true
		}
	}
	c.JSON(http.StatusOK, out)
}

// errNoEngine is returned when an equity endpoint is hit on a server
// that was started without an engine (tools, migration scripts). The
// error text is terse; the 500 response body carries it verbatim.
var errNoEngine = &apiErr{msg: "engine unavailable"}

type apiErr struct{ msg string }

func (e *apiErr) Error() string { return e.msg }
