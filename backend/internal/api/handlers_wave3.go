package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/risk"
	"github.com/nulzo/trader/internal/storage"
)

// parseSince pulls an RFC3339 or duration-style `since` query param and
// falls back to `def` when missing/invalid. Duration parsing is
// supported so operators can say `?since=24h` in a URL bar without
// typing a full timestamp; the result is resolved to `now - dur`.
func parseSince(c *gin.Context, def time.Duration) time.Time {
	now := time.Now().UTC()
	raw := strings.TrimSpace(c.Query("since"))
	if raw == "" {
		return now.Add(-def)
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return now.Add(-d)
	}
	return now.Add(-def)
}

// --------------------------------------------------------------------- Rejections

// listRejections returns structured rejection rows for a portfolio,
// filtered by `source` (risk|broker|engine — optional) and `since`
// (RFC3339 or duration — default 24h). The frontend Rejections page
// hits this every 30s; filtering in SQL keeps the UI snappy without
// a WebSocket channel.
func (s *Server) listRejections(c *gin.Context) {
	id := c.Param("id")
	since := parseSince(c, 24*time.Hour)
	limit := parseLimit(c, 200)
	rows, err := s.Deps.Store.Rejections.ListSince(c.Request.Context(), id, since, limit)
	if err != nil {
		s.serverError(c, err)
		return
	}
	if src := strings.TrimSpace(c.Query("source")); src != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if strings.EqualFold(string(r.Source), src) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if rows == nil {
		rows = []storage.Rejection{}
	}
	c.JSON(http.StatusOK, rows)
}

// --------------------------------------------------------------------- Cooldowns

// clearCooldown removes a single (portfolio, symbol) cooldown. The
// frontend calls this from the Cooldowns page to manually resume
// trading on a symbol the engine auto-suspended.
func (s *Server) clearCooldown(c *gin.Context) {
	id := c.Param("id")
	sym := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))
	if sym == "" {
		s.badRequest(c, "symbol required")
		return
	}
	if err := s.Deps.Store.Cooldowns.Clear(c.Request.Context(), id, sym); err != nil {
		s.serverError(c, err)
		return
	}
	s.Deps.Store.Audit.Record(c.Request.Context(), "cooldown", id+":"+sym, "cleared", "manual")
	c.JSON(http.StatusOK, gin.H{"status": "cleared", "symbol": sym})
}

// --------------------------------------------------------------------- Risk limits

// riskLimitsResponse wraps the engine's current limits for the
// frontend. Decimals are serialised as strings so the frontend doesn't
// lose precision on very large wallet configurations.
type riskLimitsResponse struct {
	MaxOrderUSD            string   `json:"max_order_usd"`
	MaxPositionUSD         string   `json:"max_position_usd"`
	MaxDailyLossUSD        string   `json:"max_daily_loss_usd"`
	MaxDailyOrders         int      `json:"max_daily_orders"`
	MaxSymbolExposure      string   `json:"max_symbol_exposure"`
	MaxConcurrentPositions int      `json:"max_concurrent_positions"`
	Blacklist              []string `json:"blacklist"`
	RequireApproval        bool     `json:"require_approval"`
}

func toLimitsResponse(l risk.Limits) riskLimitsResponse {
	bl := l.Blacklist
	if bl == nil {
		bl = []string{}
	}
	return riskLimitsResponse{
		MaxOrderUSD:            l.MaxOrderUSD.String(),
		MaxPositionUSD:         l.MaxPositionUSD.String(),
		MaxDailyLossUSD:        l.MaxDailyLossUSD.String(),
		MaxDailyOrders:         l.MaxDailyOrders,
		MaxSymbolExposure:      l.MaxSymbolExposure.String(),
		MaxConcurrentPositions: l.MaxConcurrentPositions,
		Blacklist:              bl,
		RequireApproval:        l.RequireApproval,
	}
}

// getRiskLimits returns the currently active risk limits.
func (s *Server) getRiskLimits(c *gin.Context) {
	if s.Deps.Risk == nil {
		s.serverError(c, errors.New("risk engine unavailable"))
		return
	}
	c.JSON(http.StatusOK, toLimitsResponse(s.Deps.Risk.GetLimits()))
}

// riskLimitsPatch is the PATCH body. Every field is a pointer so
// callers can PATCH just a subset without resetting unspecified
// fields to zero — zero-valued fields in a JSON PATCH are a classic
// footgun we deliberately avoid here.
type riskLimitsPatch struct {
	MaxOrderUSD            *string   `json:"max_order_usd,omitempty"`
	MaxPositionUSD         *string   `json:"max_position_usd,omitempty"`
	MaxDailyLossUSD        *string   `json:"max_daily_loss_usd,omitempty"`
	MaxDailyOrders         *int      `json:"max_daily_orders,omitempty"`
	MaxSymbolExposure      *string   `json:"max_symbol_exposure,omitempty"`
	MaxConcurrentPositions *int      `json:"max_concurrent_positions,omitempty"`
	Blacklist              *[]string `json:"blacklist,omitempty"`
	RequireApproval        *bool     `json:"require_approval,omitempty"`
}

// patchRiskLimits updates the active risk limits in-place. The call
// is synchronous and persists only in-memory — restarting the daemon
// reloads from env. This is deliberate: the UI toggle is for "pause
// trading now", not "permanently lower the ceiling". Editing
// defaults belongs in .env / deployment config.
func (s *Server) patchRiskLimits(c *gin.Context) {
	if s.Deps.Risk == nil {
		s.serverError(c, errors.New("risk engine unavailable"))
		return
	}
	var body riskLimitsPatch
	if err := c.BindJSON(&body); err != nil {
		s.badRequest(c, err.Error())
		return
	}
	next := s.Deps.Risk.GetLimits()
	parseDec := func(s string, cur decimal.Decimal) (decimal.Decimal, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return cur, nil
		}
		d, err := decimal.NewFromString(s)
		if err != nil {
			return cur, err
		}
		if d.IsNegative() {
			return cur, errors.New("must be non-negative")
		}
		return d, nil
	}
	if body.MaxOrderUSD != nil {
		v, err := parseDec(*body.MaxOrderUSD, next.MaxOrderUSD)
		if err != nil {
			s.badRequest(c, "max_order_usd: "+err.Error())
			return
		}
		next.MaxOrderUSD = v
	}
	if body.MaxPositionUSD != nil {
		v, err := parseDec(*body.MaxPositionUSD, next.MaxPositionUSD)
		if err != nil {
			s.badRequest(c, "max_position_usd: "+err.Error())
			return
		}
		next.MaxPositionUSD = v
	}
	if body.MaxDailyLossUSD != nil {
		v, err := parseDec(*body.MaxDailyLossUSD, next.MaxDailyLossUSD)
		if err != nil {
			s.badRequest(c, "max_daily_loss_usd: "+err.Error())
			return
		}
		next.MaxDailyLossUSD = v
	}
	if body.MaxSymbolExposure != nil {
		v, err := parseDec(*body.MaxSymbolExposure, next.MaxSymbolExposure)
		if err != nil {
			s.badRequest(c, "max_symbol_exposure: "+err.Error())
			return
		}
		if v.GreaterThan(decimal.NewFromInt(1)) {
			s.badRequest(c, "max_symbol_exposure: must be in [0, 1]")
			return
		}
		next.MaxSymbolExposure = v
	}
	if body.MaxDailyOrders != nil {
		if *body.MaxDailyOrders < 0 {
			s.badRequest(c, "max_daily_orders: must be non-negative")
			return
		}
		next.MaxDailyOrders = *body.MaxDailyOrders
	}
	if body.MaxConcurrentPositions != nil {
		if *body.MaxConcurrentPositions < 0 {
			s.badRequest(c, "max_concurrent_positions: must be non-negative")
			return
		}
		next.MaxConcurrentPositions = *body.MaxConcurrentPositions
	}
	if body.Blacklist != nil {
		cleaned := make([]string, 0, len(*body.Blacklist))
		seen := map[string]bool{}
		for _, raw := range *body.Blacklist {
			sym := strings.ToUpper(strings.TrimSpace(raw))
			if sym == "" || seen[sym] {
				continue
			}
			seen[sym] = true
			cleaned = append(cleaned, sym)
		}
		next.Blacklist = cleaned
	}
	if body.RequireApproval != nil {
		next.RequireApproval = *body.RequireApproval
	}
	s.Deps.Risk.UpdateLimits(next)
	s.Deps.Store.Audit.Record(c.Request.Context(), "risk", "limits", "updated",
		"patched via api")
	c.JSON(http.StatusOK, toLimitsResponse(s.Deps.Risk.GetLimits()))
}

// --------------------------------------------------------------------- P&L

// pnlSeries returns a zero-filled daily realized-P&L series. The
// frontend Overview page renders this as an area chart so operators
// can eyeball whether the engine is compounding or bleeding.
func (s *Server) pnlSeries(c *gin.Context) {
	id := c.Param("id")
	// Default to 30 days so the chart always has enough shape to be
	// useful on a fresh install. Duration-style `since` also works
	// for operators spelunking by URL.
	since := parseSince(c, 30*24*time.Hour)
	series, err := s.Deps.Store.Realized.DailySince(c.Request.Context(), id, since)
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, series)
}
