import { Link } from "react-router-dom"
import {
  Activity,
  ArrowRight,
  BarChart3,
  DollarSign,
  Flame,
  Gauge,
  Newspaper,
  ReceiptText,
  ShieldAlert,
  Sparkles,
  TrendingDown,
  TrendingUp,
  Users,
  Wallet,
  XCircle,
} from "lucide-react"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  Legend,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"

import { PageHeader } from "@/components/layouts/page-header"
import { PortfolioSwitcher } from "@/components/layouts/portfolio-switcher"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { paths } from "@/config/paths"
import { useAnalyticsSummary, useEquityHistory } from "@/features/analytics/api"
import { useCooldowns } from "@/features/cooldowns/api"
import { useDecisions } from "@/features/decisions/api"
import { useEngineStatus, useHealthz, useVersion } from "@/features/engine/api"
import { useLLMUsage } from "@/features/llm/api"
import { useNews } from "@/features/news/api"
import { useOrders } from "@/features/orders/api"
import { usePoliticianTrades } from "@/features/politician-trades/api"
import { usePnLSeries } from "@/features/pnl/api"
import { usePositions } from "@/features/positions/api"
import { useSignals } from "@/features/signals/api"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"
import { cents, dateTime, num, relative, signedCents, signedPercent } from "@/utils/format"

// Computed once per mount so react-query sees a stable key
// (Date.now() on every render would produce a new ISO string → infinite refetch loop).
const usageSince14d = new Date(Date.now() - 14 * 86400 * 1000).toISOString()

export function OverviewRoute() {
  const { portfolio, isLoading, error } = useCurrentPortfolio()
  const positions = usePositions(portfolio?.id)
  const orders = useOrders(portfolio?.id, 50)
  const news = useNews(10)
  const signals = useSignals()
  const decisions = useDecisions(50)
  const pTrades = usePoliticianTrades(50)
  const cooldowns = useCooldowns(portfolio?.id)
  const pnl = usePnLSeries(portfolio?.id, "720h")
  const engine = useEngineStatus()
  const usage = useLLMUsage(usageSince14d, "day")
  const version = useVersion()
  const health = useHealthz()
  const summary = useAnalyticsSummary(portfolio?.id)
  const equityHistory = useEquityHistory(portfolio?.id, "720h")

  const totalPositionValueCents = (positions.data ?? []).reduce((acc, p) => {
    const q = Number(p.quantity) || 0
    return acc + Math.round(q * p.avg_cost_cents)
  }, 0)

  const realizedCents = (positions.data ?? []).reduce(
    (acc, p) => acc + (p.realized_cents ?? 0),
    0,
  )

  const rejectedOrders = (orders.data ?? [])
    .filter((o) => o.status === "rejected")
    .slice(0, 8)

  const recentBuckets = (usage.data?.buckets ?? [])
    .slice()
    .reverse()
    .map((b) => ({
      day: b.bucket,
      calls: b.calls,
      cost: Number(b.total_cost_usd) || 0,
    }))

  const actionCounts = (decisions.data ?? []).reduce(
    (acc, d) => {
      acc[d.action] = (acc[d.action] ?? 0) + 1
      return acc
    },
    {} as Record<string, number>,
  )

  const pie = [
    { name: "Buy", value: actionCounts.buy ?? 0, fill: "#10b981" },
    { name: "Sell", value: actionCounts.sell ?? 0, fill: "#ef4444" },
    { name: "Hold", value: actionCounts.hold ?? 0, fill: "#6b7280" },
  ]

  const last30dOrderTrend = buildDailySeries(orders.data ?? [], (o) => o.created_at)

  // Cumulative realised P&L series — chart shape tracks whether we're
  // compounding or bleeding. Server already zero-fills day buckets.
  const pnlSeries = (pnl.data ?? []).reduce<Array<{ day: string; daily: number; cumulative: number }>>(
    (acc, row) => {
      const prev = acc.length ? acc[acc.length - 1].cumulative : 0
      const daily = (row.realized_cents ?? 0) / 100
      acc.push({
        day: row.day.slice(5, 10),
        daily,
        cumulative: Number((prev + daily).toFixed(2)),
      })
      return acc
    },
    [],
  )
  const pnl30d = pnlSeries.length ? pnlSeries[pnlSeries.length - 1].cumulative : 0

  // Equity curve data — same pipeline as /analytics but we only use
  // the single "equity" series here to keep the overview tile tidy.
  const equityCurve = (equityHistory.data ?? []).map((s) => ({
    ts: s.taken_at,
    label: new Date(s.taken_at).toLocaleDateString(undefined, { month: "short", day: "numeric" }),
    equity: s.equity_cents / 100,
    unrealized: s.unrealized_cents / 100,
  }))
  const equityValue = summary.data?.equity_cents ?? 0
  const unrealizedValue = summary.data?.unrealized_cents ?? 0
  const dayChangeValue = summary.data?.day_change_cents ?? 0
  const unrealizedPct =
    summary.data && summary.data.positions_cost > 0
      ? summary.data.unrealized_cents / summary.data.positions_cost
      : 0

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Overview"
        description={
          portfolio
            ? `Portfolio: ${portfolio.name} (${portfolio.mode})`
            : "Select a portfolio"
        }
        actions={
          <>
            <PortfolioSwitcher />
            <Button asChild size="sm">
              <Link to={paths.orders.getHref()}>
                <ReceiptText /> New order
              </Link>
            </Button>
          </>
        }
      />

      {error && <QueryError error={error} title="Failed to load portfolios" />}

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <StatCard
          label="Total equity"
          icon={<Wallet className="size-3.5" />}
          value={
            isLoading || summary.isLoading ? (
              <Skeleton className="h-7 w-24" />
            ) : (
              cents(equityValue)
            )
          }
          hint={
            summary.data
              ? `${cents(summary.data.cash_cents)} cash + ${cents(summary.data.positions_mtm)} mkt`
              : portfolio
                ? `Reserved ${cents(portfolio.reserved_cents)}`
                : "-"
          }
        />
        <StatCard
          label="Unrealised P/L"
          icon={
            unrealizedValue >= 0 ? (
              <TrendingUp className="size-3.5" />
            ) : (
              <TrendingDown className="size-3.5" />
            )
          }
          value={
            summary.isLoading ? <Skeleton className="h-7 w-20" /> : signedCents(unrealizedValue)
          }
          hint={`${signedPercent(unrealizedPct)} vs cost`}
          accent={
            unrealizedValue > 0 ? "positive" : unrealizedValue < 0 ? "negative" : "default"
          }
        />
        <StatCard
          label="Day change"
          icon={<Activity className="size-3.5" />}
          value={summary.data?.day_change_available ? signedCents(dayChangeValue) : "—"}
          hint={
            summary.data?.day_change_available ? "Since UTC midnight" : "Collecting snapshots…"
          }
          accent={
            summary.data?.day_change_available
              ? dayChangeValue > 0
                ? "positive"
                : dayChangeValue < 0
                  ? "negative"
                  : "default"
              : "default"
          }
        />
        <StatCard
          label="Realised P/L"
          icon={<DollarSign className="size-3.5" />}
          value={signedCents(summary.data?.realized_cents ?? realizedCents)}
          hint={`Today ${signedCents(summary.data?.realized_today_cents)}`}
          accent={realizedCents > 0 ? "positive" : realizedCents < 0 ? "negative" : "default"}
        />
        <StatCard
          label="Open positions"
          icon={<TrendingUp className="size-3.5" />}
          value={positions.data?.length ?? "–"}
          hint={
            summary.data
              ? `${summary.data.priced_positions}/${summary.data.open_positions} quoted`
              : cents(totalPositionValueCents) + " cost basis"
          }
        />
        <StatCard
          label="LLM spend (14d)"
          icon={<BarChart3 className="size-3.5" />}
          value={`$${Number(usage.data?.totals.total_cost_usd ?? 0).toFixed(4)}`}
          hint={`${num(usage.data?.totals.calls ?? 0)} calls · ${num(signals.data?.length ?? 0)} signals`}
        />
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Activity className="size-4" /> Orders — last 30 days
            </CardTitle>
            <CardDescription>Daily order count</CardDescription>
          </CardHeader>
          <CardContent className="h-[260px]">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={last30dOrderTrend}>
                <defs>
                  <linearGradient id="ord" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="currentColor" stopOpacity={0.3} />
                    <stop offset="100%" stopColor="currentColor" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                <XAxis dataKey="day" fontSize={10} tickLine={false} axisLine={false} />
                <YAxis fontSize={10} tickLine={false} axisLine={false} width={24} />
                <Tooltip
                  contentStyle={{
                    background: "var(--popover)",
                    border: "1px solid var(--border)",
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                />
                <Area
                  type="monotone"
                  dataKey="count"
                  className="text-primary"
                  stroke="currentColor"
                  strokeWidth={2}
                  fill="url(#ord)"
                />
              </AreaChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Sparkles className="size-4" /> Decisions mix
            </CardTitle>
            <CardDescription>Last 50 decisions</CardDescription>
          </CardHeader>
          <CardContent className="h-[260px]">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={pie}
                  dataKey="value"
                  cx="50%"
                  cy="50%"
                  innerRadius={55}
                  outerRadius={90}
                  paddingAngle={2}
                >
                  {pie.map((e) => (
                    <Cell key={e.name} fill={e.fill} />
                  ))}
                </Pie>
                <Legend
                  iconType="circle"
                  verticalAlign="bottom"
                  wrapperStyle={{ fontSize: 12 }}
                />
                <Tooltip
                  contentStyle={{
                    background: "var(--popover)",
                    border: "1px solid var(--border)",
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                />
              </PieChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <BarChart3 className="size-4" /> LLM spend — last 14 days
            </CardTitle>
            <CardDescription>
              Daily cost (USD) and call count
            </CardDescription>
          </CardHeader>
          <CardContent className="h-[260px]">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={recentBuckets}>
                <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                <XAxis dataKey="day" fontSize={10} tickLine={false} axisLine={false} />
                <YAxis
                  yAxisId="l"
                  orientation="left"
                  fontSize={10}
                  tickLine={false}
                  axisLine={false}
                  width={32}
                />
                <YAxis
                  yAxisId="r"
                  orientation="right"
                  fontSize={10}
                  tickLine={false}
                  axisLine={false}
                  width={32}
                />
                <Tooltip
                  contentStyle={{
                    background: "var(--popover)",
                    border: "1px solid var(--border)",
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                />
                <Legend wrapperStyle={{ fontSize: 12 }} />
                <Bar yAxisId="l" dataKey="cost" name="Cost USD" fill="#6366f1" radius={[4, 4, 0, 0]} />
                <Bar yAxisId="r" dataKey="calls" name="Calls" fill="#06b6d4" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Gauge className="size-4" /> System
            </CardTitle>
            <CardDescription>Engine &amp; service health</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3 text-sm">
            <Row label="Engine">
              <Badge
                className={
                  engine.data?.enabled
                    ? "bg-emerald-500/15 text-emerald-600"
                    : "bg-muted text-muted-foreground"
                }
              >
                {engine.data?.enabled ? "Running" : "Paused"}
              </Badge>
            </Row>
            <Row label="API">
              <Badge
                className={
                  health.data?.status === "ok" || health.data?.status === "alive"
                    ? "bg-emerald-500/15 text-emerald-600"
                    : "bg-destructive/15 text-destructive"
                }
              >
                {health.data?.status ?? "unknown"}
              </Badge>
            </Row>
            <Row label="Version">
              <span className="font-mono text-xs">
                {version.data?.version ?? "…"} · {version.data?.commit?.slice(0, 7)}
              </span>
            </Row>
            <Row label="LLM errors (14d)">
              <span className="tabular-nums">
                {num(usage.data?.totals.error_calls ?? 0)}
              </span>
            </Row>
            <Row label="Avg latency">
              <span className="tabular-nums">
                {num(usage.data?.totals.avg_latency_ms ?? 0, 0)} ms
              </span>
            </Row>
            <Button asChild variant="outline" size="sm" className="mt-auto">
              <Link to={paths.engine.getHref()}>
                Manage engine <ArrowRight className="ml-auto" />
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3">
        <Card>
          <CardHeader className="flex flex-row items-start justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <TrendingUp className="size-4" /> Equity curve — last 30 days
              </CardTitle>
              <CardDescription>
                Cash + mark-to-market positions. Includes unrealised gains/losses.
              </CardDescription>
            </div>
            <div className="text-right">
              <div className="text-muted-foreground text-[10px] uppercase tracking-wider">
                Current
              </div>
              <div className="text-lg font-semibold tabular-nums">
                {cents(equityValue)}
              </div>
              <div
                className={
                  unrealizedValue > 0
                    ? "text-emerald-500 text-xs tabular-nums"
                    : unrealizedValue < 0
                      ? "text-destructive text-xs tabular-nums"
                      : "text-muted-foreground text-xs tabular-nums"
                }
              >
                {signedCents(unrealizedValue)} ({signedPercent(unrealizedPct)}) unrealised
              </div>
            </div>
          </CardHeader>
          <CardContent className="h-[260px]">
            {equityHistory.isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : equityCurve.length === 0 ? (
              <div className="flex h-full items-center justify-center text-center text-sm">
                <div>
                  <div className="font-medium">No snapshots yet</div>
                  <div className="text-muted-foreground text-xs">
                    The engine writes a valuation every 5 minutes — check back shortly.
                  </div>
                </div>
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={equityCurve}>
                  <defs>
                    <linearGradient id="equityOverview" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#6366f1" stopOpacity={0.35} />
                      <stop offset="100%" stopColor="#6366f1" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                  <XAxis
                    dataKey="label"
                    fontSize={10}
                    tickLine={false}
                    axisLine={false}
                    minTickGap={40}
                  />
                  <YAxis
                    fontSize={10}
                    tickLine={false}
                    axisLine={false}
                    width={52}
                    tickFormatter={(v) => {
                      const n = Number(v) || 0
                      const abs = Math.abs(n)
                      if (abs >= 1e6) return `$${(n / 1e6).toFixed(1)}m`
                      if (abs >= 1e3) return `$${(n / 1e3).toFixed(1)}k`
                      return `$${n.toFixed(0)}`
                    }}
                  />
                  <Tooltip
                    contentStyle={{
                      background: "var(--popover)",
                      border: "1px solid var(--border)",
                      borderRadius: 8,
                      fontSize: 12,
                    }}
                    labelFormatter={(_, payload) =>
                      payload?.[0]?.payload?.ts
                        ? dateTime(payload[0].payload.ts as string)
                        : ""
                    }
                    formatter={(value, name) => [
                      `$${(Number(value) || 0).toFixed(2)}`,
                      String(name),
                    ]}
                  />
                  <Area
                    type="monotone"
                    dataKey="equity"
                    name="Equity"
                    stroke="#6366f1"
                    strokeWidth={2}
                    fill="url(#equityOverview)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <Card className="lg:col-span-3">
          <CardHeader className="flex flex-row items-start justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <DollarSign className="size-4" /> Realised P&amp;L — last 30 days
              </CardTitle>
              <CardDescription>
                Daily realised (bars) and cumulative (line). Resets each UTC midnight.
              </CardDescription>
            </div>
            <div className="text-right">
              <div className="text-muted-foreground text-[10px] uppercase tracking-wider">
                30d cumulative
              </div>
              <div
                className={
                  pnl30d > 0
                    ? "text-emerald-500 text-lg font-semibold tabular-nums"
                    : pnl30d < 0
                      ? "text-destructive text-lg font-semibold tabular-nums"
                      : "text-muted-foreground text-lg font-semibold tabular-nums"
                }
              >
                {pnl30d >= 0 ? "+" : ""}${pnl30d.toFixed(2)}
              </div>
            </div>
          </CardHeader>
          <CardContent className="h-[280px]">
            {pnl.isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : pnlSeries.length === 0 ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                No realised P&amp;L yet — trade some positions to close to see this fill in.
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <ComposedChart data={pnlSeries}>
                  <defs>
                    <linearGradient id="pnlCumulative" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#6366f1" stopOpacity={0.4} />
                      <stop offset="100%" stopColor="#6366f1" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                  <XAxis dataKey="day" fontSize={10} tickLine={false} axisLine={false} />
                  <YAxis
                    yAxisId="l"
                    orientation="left"
                    fontSize={10}
                    tickLine={false}
                    axisLine={false}
                    width={40}
                    tickFormatter={(v) => `$${v}`}
                  />
                  <YAxis
                    yAxisId="r"
                    orientation="right"
                    fontSize={10}
                    tickLine={false}
                    axisLine={false}
                    width={48}
                    tickFormatter={(v) => `$${v}`}
                  />
                  <Tooltip
                    contentStyle={{
                      background: "var(--popover)",
                      border: "1px solid var(--border)",
                      borderRadius: 8,
                      fontSize: 12,
                    }}
                    formatter={(value, name) => {
                      const n = Number(value) || 0
                      return [`${n >= 0 ? "+" : ""}$${n.toFixed(2)}`, String(name)]
                    }}
                  />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  <Bar
                    yAxisId="l"
                    dataKey="daily"
                    name="Daily"
                    radius={[2, 2, 0, 0]}
                  >
                    {pnlSeries.map((d, i) => (
                      <Cell
                        key={i}
                        fill={d.daily >= 0 ? "#10b981" : "#ef4444"}
                      />
                    ))}
                  </Bar>
                  <Area
                    yAxisId="r"
                    type="monotone"
                    dataKey="cumulative"
                    name="Cumulative"
                    stroke="#6366f1"
                    strokeWidth={2}
                    fill="url(#pnlCumulative)"
                  />
                </ComposedChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex-row items-start justify-between space-y-0">
            <div>
              <CardTitle className="flex items-center gap-2">
                <XCircle className="size-4 text-destructive" /> Recent rejections
              </CardTitle>
              <CardDescription>
                Orders the risk engine or broker refused, newest first.
              </CardDescription>
            </div>
            <Button asChild variant="ghost" size="sm">
              <Link to={paths.orders.getHref()}>
                All orders <ArrowRight className="ml-1 size-3.5" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-1.5">
            {rejectedOrders.map((o) => (
              <div
                key={o.id}
                className="hover:bg-muted flex items-center justify-between gap-3 rounded-md p-2 text-sm"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="bg-muted rounded px-1.5 py-0.5 font-mono text-xs">
                      {o.symbol}
                    </span>
                    <Badge
                      variant="outline"
                      className={
                        o.side === "buy"
                          ? "border-emerald-500/30 text-emerald-600"
                          : "border-destructive/30 text-destructive"
                      }
                    >
                      {o.side}
                    </Badge>
                    <span className="text-muted-foreground text-[10px]">
                      {relative(o.created_at)}
                    </span>
                  </div>
                  <div
                    className="text-muted-foreground mt-0.5 line-clamp-1 text-xs"
                    title={o.reason}
                  >
                    {o.reason || "rejected"}
                  </div>
                </div>
              </div>
            ))}
            {!rejectedOrders.length && (
              <div className="text-muted-foreground py-6 text-center text-sm">
                No rejections in the last 50 orders. Nice.
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex-row items-start justify-between space-y-0">
            <div>
              <CardTitle className="flex items-center gap-2">
                <ShieldAlert className="size-4" /> Active cooldowns
              </CardTitle>
              <CardDescription>
                Symbols the engine is steering around until the window expires.
              </CardDescription>
            </div>
            <Button asChild variant="ghost" size="sm">
              <Link to={paths.cooldowns.getHref()}>
                Manage <ArrowRight className="ml-1 size-3.5" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-1.5">
            {(cooldowns.data ?? []).slice(0, 8).map((c) => (
              <div
                key={`${c.symbol}-${c.until_ts}`}
                className="hover:bg-muted flex items-center justify-between gap-3 rounded-md p-2 text-sm"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="bg-muted rounded px-1.5 py-0.5 font-mono text-xs">
                      {c.symbol}
                    </span>
                    <span className="text-muted-foreground text-[10px]">
                      expires {relative(c.until_ts)}
                    </span>
                  </div>
                  <div
                    className="text-muted-foreground mt-0.5 line-clamp-1 text-xs"
                    title={c.reason}
                  >
                    {c.reason || "cooldown active"}
                  </div>
                </div>
              </div>
            ))}
            {!cooldowns.data?.length && (
              <div className="text-muted-foreground py-6 text-center text-sm">
                No active cooldowns.
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Newspaper className="size-4" /> Recent news
            </CardTitle>
            <CardDescription>Latest 10 headlines</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {(news.data ?? []).slice(0, 10).map((n) => (
              <a
                key={n.id}
                href={n.url}
                target="_blank"
                rel="noreferrer"
                className="hover:bg-muted flex flex-col rounded-md p-2 text-sm"
              >
                <div className="line-clamp-2 font-medium">{n.title}</div>
                <div className="text-muted-foreground mt-1 flex items-center gap-2 text-xs">
                  <span>{n.source}</span>
                  <span>·</span>
                  <span>{relative(n.pub_at)}</span>
                  {n.symbols && (
                    <>
                      <span>·</span>
                      <span className="font-mono">{n.symbols}</span>
                    </>
                  )}
                </div>
              </a>
            ))}
            {!news.data?.length && (
              <div className="text-muted-foreground py-6 text-center text-sm">
                No news yet.
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Flame className="size-4" /> Top signals
            </CardTitle>
            <CardDescription>Highest confidence, active</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {(signals.data ?? [])
              .slice()
              .sort((a, b) => b.confidence - a.confidence)
              .slice(0, 8)
              .map((s) => (
                <div
                  key={s.id}
                  className="hover:bg-muted flex items-center justify-between rounded-md p-2 text-sm"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="bg-muted rounded px-1.5 py-0.5 font-mono text-xs">
                        {s.symbol}
                      </span>
                      <Badge variant="outline" className="capitalize">
                        {s.kind}
                      </Badge>
                      <span
                        className={
                          s.side === "buy"
                            ? "text-xs font-medium text-emerald-500"
                            : "text-destructive text-xs font-medium"
                        }
                      >
                        {s.side}
                      </span>
                    </div>
                    <div className="text-muted-foreground mt-0.5 line-clamp-1 text-xs">
                      {s.reason || s.ref_id}
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="text-xs font-medium">
                      {Math.round(s.confidence * 100)}%
                    </div>
                    <div className="text-muted-foreground text-[10px]">
                      {relative(s.expires_at)}
                    </div>
                  </div>
                </div>
              ))}
            {!signals.data?.length && (
              <div className="text-muted-foreground py-6 text-center text-sm">
                No active signals.
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Users className="size-4" /> Recent politician trades
            </CardTitle>
            <CardDescription>Most recently disclosed</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {(pTrades.data ?? []).slice(0, 8).map((t) => (
              <div
                key={t.id}
                className="hover:bg-muted flex items-center justify-between rounded-md p-2 text-sm"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="bg-muted rounded px-1.5 py-0.5 font-mono text-xs">
                      {t.symbol || "—"}
                    </span>
                    <span className="truncate font-medium">{t.politician_name}</span>
                  </div>
                  <div className="text-muted-foreground mt-0.5 text-xs">
                    {t.chamber} · {dateTime(t.traded_at)}
                  </div>
                </div>
                <div className="text-right">
                  <Badge
                    className={
                      t.side === "buy"
                        ? "bg-emerald-500/15 text-emerald-600"
                        : "bg-destructive/15 text-destructive"
                    }
                  >
                    {t.side}
                  </Badge>
                  <div className="text-muted-foreground mt-0.5 text-[10px]">
                    ${num(t.amount_min_usd)}–${num(t.amount_max_usd)}
                  </div>
                </div>
              </div>
            ))}
            {!pTrades.data?.length && (
              <div className="text-muted-foreground py-6 text-center text-sm">
                No trades yet.
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-muted-foreground text-xs uppercase tracking-wide">
        {label}
      </span>
      {children}
    </div>
  )
}

/** Build a last-30-days series counting items by UTC day. */
function buildDailySeries<T>(
  rows: T[],
  getDate: (row: T) => string | undefined,
): Array<{ day: string; count: number }> {
  const buckets = new Map<string, number>()
  const now = new Date()
  for (let i = 29; i >= 0; i--) {
    const d = new Date(now)
    d.setUTCDate(now.getUTCDate() - i)
    buckets.set(d.toISOString().slice(0, 10), 0)
  }
  for (const row of rows) {
    const raw = getDate(row)
    if (!raw) continue
    const day = raw.slice(0, 10)
    if (buckets.has(day)) buckets.set(day, (buckets.get(day) ?? 0) + 1)
  }
  return [...buckets.entries()].map(([day, count]) => ({ day: day.slice(5), count }))
}
