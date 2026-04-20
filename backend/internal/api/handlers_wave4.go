package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nulzo/trader/internal/domain"
)

// Wave 4 handlers expose the alternative-data feeds Quiver powers
// (insiders, social, lobbying, gov contracts, short volume). All
// endpoints are read-only; ingestion happens on the engine's
// schedule. Each accepts an optional `symbol` query param for
// per-ticker views used by the "Intelligence" drawers on the
// frontend, plus a shared `since` / `limit` convention.

// listInsiders returns recent SEC Form 4 filings. Optional filters:
//
//	?symbol=AAPL   – restrict to one ticker
//	?side=buy|sell – restrict by side (applied client-side; cheap)
//	?since=24h     – RFC3339 or Go duration (default 30d)
//	?limit=N       – cap (default 100, max 500)
func (s *Server) listInsiders(c *gin.Context) {
	sym := strings.ToUpper(strings.TrimSpace(c.Query("symbol")))
	side := strings.ToLower(strings.TrimSpace(c.Query("side")))
	since := parseSince(c, 30*24*time.Hour)
	limit := capLimit(parseLimit(c, 100), 500)

	var (
		rows []domain.InsiderTrade
		err  error
	)
	if sym != "" {
		rows, err = s.Deps.Store.Insiders.BySymbol(c.Request.Context(), sym, since)
	} else {
		rows, err = s.Deps.Store.Insiders.Since(c.Request.Context(), since)
	}
	if err != nil {
		s.serverError(c, err)
		return
	}
	if side == "buy" || side == "sell" {
		out := rows[:0]
		for _, r := range rows {
			if string(r.Side) == side {
				out = append(out, r)
			}
		}
		rows = out
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []domain.InsiderTrade{}
	}
	c.JSON(http.StatusOK, rows)
}

// listSocial returns recent social-media rollups (WSB + Twitter).
// Filters:
//
//	?symbol=...    – one ticker
//	?platform=wsb  – wsb|twitter (applied client-side)
//	?since=24h     – default 48h
//	?limit=N       – default 200, max 1000
func (s *Server) listSocial(c *gin.Context) {
	sym := strings.ToUpper(strings.TrimSpace(c.Query("symbol")))
	platform := strings.ToLower(strings.TrimSpace(c.Query("platform")))
	since := parseSince(c, 48*time.Hour)
	limit := capLimit(parseLimit(c, 200), 1000)

	var (
		rows []domain.SocialPost
		err  error
	)
	if sym != "" {
		rows, err = s.Deps.Store.Social.BySymbol(c.Request.Context(), sym, since)
	} else {
		rows, err = s.Deps.Store.Social.Since(c.Request.Context(), since)
	}
	if err != nil {
		s.serverError(c, err)
		return
	}
	if platform != "" {
		out := rows[:0]
		for _, r := range rows {
			if strings.EqualFold(r.Platform, platform) {
				out = append(out, r)
			}
		}
		rows = out
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []domain.SocialPost{}
	}
	c.JSON(http.StatusOK, rows)
}

// listLobbying returns recent corporate lobbying filings. Symbol
// filter is optional; `since` defaults to 180d because LDA filings
// are a quarterly cycle and the "recent" window is naturally longer
// than insider or social data.
func (s *Server) listLobbying(c *gin.Context) {
	sym := strings.ToUpper(strings.TrimSpace(c.Query("symbol")))
	since := parseSince(c, 180*24*time.Hour)
	limit := capLimit(parseLimit(c, 100), 500)

	var (
		rows []domain.LobbyingEvent
		err  error
	)
	if sym != "" {
		rows, err = s.Deps.Store.Lobbying.BySymbol(c.Request.Context(), sym, since)
	} else {
		rows, err = s.Deps.Store.Lobbying.ListRecent(c.Request.Context(), limit)
	}
	if err != nil {
		s.serverError(c, err)
		return
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []domain.LobbyingEvent{}
	}
	c.JSON(http.StatusOK, rows)
}

// listContracts returns recent federal contract awards.
func (s *Server) listContracts(c *gin.Context) {
	sym := strings.ToUpper(strings.TrimSpace(c.Query("symbol")))
	since := parseSince(c, 90*24*time.Hour)
	limit := capLimit(parseLimit(c, 100), 500)

	var (
		rows []domain.GovContract
		err  error
	)
	if sym != "" {
		rows, err = s.Deps.Store.Contracts.BySymbol(c.Request.Context(), sym, since)
	} else {
		rows, err = s.Deps.Store.Contracts.ListRecent(c.Request.Context(), limit)
	}
	if err != nil {
		s.serverError(c, err)
		return
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []domain.GovContract{}
	}
	c.JSON(http.StatusOK, rows)
}

// listShortVolume returns the daily off-exchange short-volume
// timeseries for a symbol. Requires :symbol in the path because the
// whole-market feed is too large to meaningfully render as a list.
func (s *Server) listShortVolume(c *gin.Context) {
	sym := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))
	if sym == "" {
		s.badRequest(c, "symbol required")
		return
	}
	since := parseSince(c, 30*24*time.Hour)
	rows, err := s.Deps.Store.Shorts.BySymbol(c.Request.Context(), sym, since)
	if err != nil {
		s.serverError(c, err)
		return
	}
	if rows == nil {
		rows = []domain.ShortVolume{}
	}
	c.JSON(http.StatusOK, rows)
}

// capLimit clamps a user-supplied limit to a ceiling. Keeps memory +
// network bounded even when a client hand-crafts ?limit=999999.
func capLimit(req, max int) int {
	if req <= 0 {
		return max
	}
	if req > max {
		return max
	}
	return req
}
