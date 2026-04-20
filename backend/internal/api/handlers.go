package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/nulzo/trader/internal/domain"
	"github.com/nulzo/trader/internal/storage"
)

// --------------------------------------------------------------------- Helpers

func (s *Server) badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

func (s *Server) notFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"error": msg})
}

func (s *Server) serverError(c *gin.Context, err error) {
	s.Deps.Log.Error().Err(err).Str("path", c.Request.URL.Path).Msg("api error")
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// --------------------------------------------------------------------- Handlers

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC()})
}

// livez returns 200 as long as the process is running. It is
// intentionally cheap — a stuck readiness dependency should NOT kill
// the pod.
func (s *Server) livez(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// readyz returns 200 only when the service can accept real traffic.
// It pings the configured dependency checker (typically the database)
// with a short timeout.
func (s *Server) readyz(c *gin.Context) {
	if s.Deps.ReadinessCheck == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if err := s.Deps.ReadinessCheck(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

// metrics forwards scrape requests to the injected Prometheus handler.
// When telemetry has not been initialised (e.g. tests) the endpoint
// returns 501 so scrapers receive a clear signal instead of silently
// serving nothing.
func (s *Server) metrics(c *gin.Context) {
	if s.Deps.MetricsHandler == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "metrics disabled"})
		return
	}
	s.Deps.MetricsHandler.ServeHTTP(c.Writer, c.Request)
}

func (s *Server) version(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": s.Deps.Version, "commit": s.Deps.BuildCommit})
}

func (s *Server) listPortfolios(c *gin.Context) {
	ps, err := s.Deps.Store.Portfolios.List(c.Request.Context())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, ps)
}

func (s *Server) getPortfolio(c *gin.Context) {
	p, err := s.Deps.Store.Portfolios.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		if storage.IsNotFound(err) {
			s.notFound(c, "portfolio not found")
			return
		}
		s.serverError(c, err)
		return
	}
	positions, _ := s.Deps.Store.Positions.List(c.Request.Context(), p.ID)
	c.JSON(http.StatusOK, gin.H{"portfolio": p, "positions": positions})
}

type amountReq struct {
	AmountCents int64 `json:"amount_cents" binding:"required,gt=0"`
}

func (s *Server) deposit(c *gin.Context) {
	var req amountReq
	if err := c.BindJSON(&req); err != nil {
		s.badRequest(c, err.Error())
		return
	}
	if err := s.Deps.Store.Portfolios.UpdateCash(c.Request.Context(), c.Param("id"), domain.Money(req.AmountCents), 0); err != nil {
		s.serverError(c, err)
		return
	}
	s.Deps.Store.Audit.Record(c.Request.Context(), "portfolio", c.Param("id"), "deposit", strconv.FormatInt(req.AmountCents, 10))
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) withdraw(c *gin.Context) {
	var req amountReq
	if err := c.BindJSON(&req); err != nil {
		s.badRequest(c, err.Error())
		return
	}
	p, err := s.Deps.Store.Portfolios.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.notFound(c, "portfolio not found")
		return
	}
	if int64(p.AvailableCents()) < req.AmountCents {
		s.badRequest(c, "insufficient available cash")
		return
	}
	if err := s.Deps.Store.Portfolios.UpdateCash(c.Request.Context(), p.ID, -domain.Money(req.AmountCents), 0); err != nil {
		s.serverError(c, err)
		return
	}
	s.Deps.Store.Audit.Record(c.Request.Context(), "portfolio", p.ID, "withdraw", strconv.FormatInt(req.AmountCents, 10))
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) listPositions(c *gin.Context) {
	pos, err := s.Deps.Store.Positions.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, pos)
}

// listCooldowns returns currently-active (`until_ts > now`) cooldowns
// for a portfolio. The frontend Overview and Cooldowns pages read this
// so operators can see at a glance which symbols the engine is steering
// around — e.g. risk-rejected buys or broker-rejected sells.
func (s *Server) listCooldowns(c *gin.Context) {
	rows, err := s.Deps.Store.Cooldowns.ListActive(c.Request.Context(), c.Param("id"), time.Now().UTC())
	if err != nil {
		s.serverError(c, err)
		return
	}
	if rows == nil {
		rows = []storage.Cooldown{}
	}
	c.JSON(http.StatusOK, rows)
}

func (s *Server) listOrders(c *gin.Context) {
	limit := parseLimit(c, 50)
	os, err := s.Deps.Store.Orders.List(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, os)
}

type createOrderReq struct {
	Symbol      string  `json:"symbol" binding:"required"`
	Side        string  `json:"side" binding:"required,oneof=buy sell"`
	Quantity    float64 `json:"quantity,omitempty"`
	NotionalUSD float64 `json:"notional_usd,omitempty"`
	Reason      string  `json:"reason"`
}

func (s *Server) createOrder(c *gin.Context) {
	var req createOrderReq
	if err := c.BindJSON(&req); err != nil {
		s.badRequest(c, err.Error())
		return
	}
	if req.Quantity <= 0 && req.NotionalUSD <= 0 {
		s.badRequest(c, "either quantity or notional_usd is required")
		return
	}
	ctx := c.Request.Context()
	sym := strings.ToUpper(req.Symbol)
	quote, err := s.Deps.Market.Quote(ctx, sym)
	if err != nil {
		s.serverError(c, err)
		return
	}
	qty := decimal.NewFromFloat(req.Quantity)
	if qty.IsZero() {
		qty = decimal.NewFromFloat(req.NotionalUSD).Div(quote.Price).Round(4)
	}
	order := &domain.Order{
		PortfolioID: c.Param("id"),
		Symbol:      sym,
		Side:        domain.Side(req.Side),
		Type:        domain.OrderTypeMarket,
		TimeInForce: domain.TIFDay,
		Quantity:    qty,
		Reason:      req.Reason,
	}
	if err := s.Deps.Store.Orders.Create(ctx, order); err != nil {
		s.serverError(c, err)
		return
	}
	bo, err := s.Deps.Broker.SubmitOrder(ctx, order)
	if err != nil {
		order.Status = domain.OrderStatusRejected
		order.Reason = err.Error()
		_ = s.Deps.Store.Orders.UpdateStatus(ctx, order)
		s.serverError(c, err)
		return
	}
	order.BrokerID = bo.BrokerID
	order.Status = bo.Status
	order.FilledQty = bo.FilledQty
	order.FilledAvgCents = domain.NewMoneyFromDecimal(bo.FilledAvg)
	if !bo.SubmittedAt.IsZero() {
		t := bo.SubmittedAt
		order.SubmittedAt = &t
	}
	if bo.FilledAt != nil {
		order.FilledAt = bo.FilledAt
	}
	_ = s.Deps.Store.Orders.UpdateStatus(ctx, order)
	if bo.Status == domain.OrderStatusFilled || bo.Status == domain.OrderStatusPartial {
		priceCents := domain.NewMoneyFromDecimal(bo.FilledAvg)
		fillNotional := domain.NewMoneyFromDecimal(bo.FilledQty.Mul(bo.FilledAvg))
		if order.Side == domain.SideBuy {
			_ = s.Deps.Store.Portfolios.UpdateCash(ctx, order.PortfolioID, -fillNotional, 0)
		} else {
			_ = s.Deps.Store.Portfolios.UpdateCash(ctx, order.PortfolioID, fillNotional, 0)
		}
		_, _ = s.Deps.Store.Positions.Apply(ctx, order.PortfolioID, order.Symbol, order.Side, bo.FilledQty, priceCents)
	}
	c.JSON(http.StatusOK, order)
}

func (s *Server) listPoliticians(c *gin.Context) {
	ps, err := s.Deps.Store.Politicians.List(c.Request.Context())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, ps)
}

func (s *Server) upsertPolitician(c *gin.Context) {
	var p domain.Politician
	if err := c.BindJSON(&p); err != nil {
		s.badRequest(c, err.Error())
		return
	}
	if p.Name == "" {
		s.badRequest(c, "name required")
		return
	}
	if err := s.Deps.Store.Politicians.Upsert(c.Request.Context(), &p); err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (s *Server) listPoliticianTrades(c *gin.Context) {
	ts, err := s.Deps.Store.PTrades.ListRecent(c.Request.Context(), parseLimit(c, 100))
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, ts)
}

func (s *Server) listNews(c *gin.Context) {
	ns, err := s.Deps.Store.News.Recent(c.Request.Context(), parseLimit(c, 50))
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, ns)
}

func (s *Server) listSignals(c *gin.Context) {
	symbol := strings.ToUpper(c.Query("symbol"))
	ss, err := s.Deps.Store.Signals.Active(c.Request.Context(), symbol, time.Now().UTC())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, ss)
}

func (s *Server) listDecisions(c *gin.Context) {
	ds, err := s.Deps.Store.Decisions.List(c.Request.Context(), s.Deps.PortfolioID, parseLimit(c, 50))
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, ds)
}

func (s *Server) executeDecision(c *gin.Context) {
	ctx := c.Request.Context()
	ds, err := s.Deps.Store.Decisions.List(ctx, s.Deps.PortfolioID, 500)
	if err != nil {
		s.serverError(c, err)
		return
	}
	id := c.Param("id")
	for _, d := range ds {
		if d.ID == id {
			if err := s.Deps.Engine.Execute(ctx, &d); err != nil {
				if errors.Is(err, domain.ErrRiskRejected) {
					c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
					return
				}
				s.serverError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "executed"})
			return
		}
	}
	s.notFound(c, "decision not found")
}

func (s *Server) listAudit(c *gin.Context) {
	rows, err := s.Deps.Store.Audit.List(c.Request.Context(), parseLimit(c, 100))
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (s *Server) quote(c *gin.Context) {
	sym := strings.ToUpper(c.Param("symbol"))
	q, err := s.Deps.Market.Quote(c.Request.Context(), sym)
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, q)
}

func (s *Server) engineStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"enabled": s.Deps.Engine.Enabled()})
}

type toggleReq struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) engineToggle(c *gin.Context) {
	var req toggleReq
	if err := c.BindJSON(&req); err != nil {
		s.badRequest(c, err.Error())
		return
	}
	s.Deps.Engine.SetEnabled(req.Enabled)
	c.JSON(http.StatusOK, gin.H{"enabled": s.Deps.Engine.Enabled()})
}

func (s *Server) engineIngest(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	if err := s.Deps.Engine.Ingest(ctx); err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) engineDecide(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	if err := s.Deps.Engine.DecideAndTrade(ctx); err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) brokerAccount(c *gin.Context) {
	acct, err := s.Deps.Broker.Account(c.Request.Context())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, acct)
}

func (s *Server) brokerPositions(c *gin.Context) {
	pos, err := s.Deps.Broker.Positions(c.Request.Context())
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, pos)
}

// listLLMCalls returns a paginated list of persisted LLM attempts,
// filterable by operation / model / outcome / since. Use this to audit
// exactly what was sent to the model, what came back, and what it cost.
func (s *Server) listLLMCalls(c *gin.Context) {
	f := storage.ListFilter{
		Limit:     parseLimit(c, 50),
		Offset:    parseOffset(c),
		Operation: c.Query("operation"),
		Model:     c.Query("model"),
		Outcome:   c.Query("outcome"),
	}
	if q := c.Query("since"); q != "" {
		if t, err := time.Parse(time.RFC3339, q); err == nil {
			f.Since = &t
		}
	}
	if q := c.Query("until"); q != "" {
		if t, err := time.Parse(time.RFC3339, q); err == nil {
			f.Until = &t
		}
	}
	rows, err := s.Deps.Store.LLMCalls.List(c.Request.Context(), f)
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// llmUsage rolls up cost/token/latency across the requested window.
//
// Query params:
//   - since=RFC3339 (default: 30 days ago)
//   - group_by=day|hour|model|operation|outcome (default: day)
//
// Returns: {"since", "group_by", "totals": UsageTotals, "buckets": []UsageRow}.
func (s *Server) llmUsage(c *gin.Context) {
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if raw := c.Query("since"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		} else {
			s.badRequest(c, "since: "+err.Error())
			return
		}
	}
	group := c.DefaultQuery("group_by", "day")

	totals, err := s.Deps.Store.LLMCalls.Totals(c.Request.Context(), since)
	if err != nil {
		s.serverError(c, err)
		return
	}
	buckets, err := s.Deps.Store.LLMCalls.UsageBy(c.Request.Context(), group, since)
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"since":    since,
		"group_by": group,
		"totals":   totals,
		"buckets":  buckets,
	})
}

func parseOffset(c *gin.Context) int {
	v := c.Query("offset")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parseLimit(c *gin.Context, def int) int {
	v := c.Query("limit")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > 500 {
		n = 500
	}
	return n
}
