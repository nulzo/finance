import { useMemo, useState } from "react"
import {
  Activity,
  ArrowUpDown,
  BarChart3,
  Briefcase,
  DollarSign,
  LineChart as LineChartIcon,
  PercentCircle,
  TrendingDown,
  TrendingUp,
  Wallet,
} from "lucide-react"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"

import { QueryError } from "@/components/errors/query-error"
import { PageHeader } from "@/components/layouts/page-header"
import { PortfolioSwitcher } from "@/components/layouts/portfolio-switcher"
import { StatCard } from "@/components/layouts/stat-card"
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
import {
  useAnalyticsSummary,
  useEquityHistory,
  usePositionsPnL,
} from "@/features/analytics/api"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"
import { cn } from "@/lib/utils"
import {
  cents,
  dateTime,
  num,
  signedCents,
  signedPercent,
} from "@/utils/format"

// The window picker maps to Go duration strings — the backend parses
// either RFC3339 timestamps or Go durations, but hours are safer
// cross-platform than "d" which Go doesn't accept.
const WINDOWS = [
  { label: "24h", duration: "24h" },
  { label: "7d", duration: "168h" },
  { label: "30d", duration: "720h" },
  { label: "90d", duration: "2160h" },
] as const
type WindowKey = (typeof WINDOWS)[number]["duration"]

export function AnalyticsRoute() {
  const { portfolio, isLoading, error } = useCurrentPortfolio()
  const [window, setWindow] = useState<WindowKey>("720h")

  const summary = useAnalyticsSummary(portfolio?.id)
  const history = useEquityHistory(portfolio?.id, window)
  const positions = usePositionsPnL(portfolio?.id)

  // Equity-curve data. Server returns taken_at ISO strings oldest-
  // first; recharts wants each row with a pre-computed label + a
  // few derived series so the chart layer stays declarative.
  const curve = useMemo(() => {
    return (history.data ?? []).map((s) => ({
      ts: s.taken_at,
      label: formatBucket(s.taken_at, window),
      equity: s.equity_cents / 100,
      cash: s.cash_cents / 100,
      positions: s.positions_mtm / 100,
      unrealized: s.unrealized_cents / 100,
      realized: s.realized_cents / 100,
      cost: s.positions_cost / 100,
    }))
  }, [history.data, window])

  // Realised + unrealised stacked for a side-by-side view of where
  // P&L actually came from. Derived from the curve so both charts
  // share the same point-in-time truth.
  const realizedVsUnrealized = useMemo(
    () => curve.map(({ label, realized, unrealized }) => ({ label, realized, unrealized })),
    [curve],
  )

  // Positions leaderboard: sort by unrealised dollars descending so
  // the highest contributors top the chart. We also separate winners
  // from losers for the sign-coloured bars.
  const sortedPositions = useMemo(() => {
    return (positions.data ?? [])
      .slice()
      .sort((a, b) => b.unrealized_cents - a.unrealized_cents)
  }, [positions.data])

  const winners = sortedPositions.filter((p) => p.unrealized_cents > 0)
  const losers = sortedPositions.filter((p) => p.unrealized_cents < 0)

  const equity = summary.data?.equity_cents ?? 0
  const unrealized = summary.data?.unrealized_cents ?? 0
  const realized = summary.data?.realized_cents ?? 0
  const dayChange = summary.data?.day_change_cents ?? 0
  const unrealizedPct =
    summary.data && summary.data.positions_cost > 0
      ? summary.data.unrealized_cents / summary.data.positions_cost
      : 0

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Analytics"
        description={
          portfolio
            ? `Live mark-to-market view for ${portfolio.name}`
            : "Select a portfolio"
        }
        actions={<PortfolioSwitcher />}
      />

      {error && <QueryError error={error} title="Failed to load portfolios" />}

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <StatCard
          label="Total equity"
          icon={<Wallet className="size-3.5" />}
          value={isLoading || summary.isLoading ? <Skeleton className="h-7 w-24" /> : cents(equity)}
          hint={
            summary.data
              ? `${cents(summary.data.cash_cents)} cash + ${cents(summary.data.positions_mtm)} mkt`
              : "-"
          }
        />
        <StatCard
          label="Unrealised P/L"
          icon={
            unrealized >= 0 ? (
              <TrendingUp className="size-3.5" />
            ) : (
              <TrendingDown className="size-3.5" />
            )
          }
          value={summary.isLoading ? <Skeleton className="h-7 w-20" /> : signedCents(unrealized)}
          hint={signedPercent(unrealizedPct) + " vs cost"}
          accent={unrealized > 0 ? "positive" : unrealized < 0 ? "negative" : "default"}
        />
        <StatCard
          label="Realised P/L (all-time)"
          icon={<DollarSign className="size-3.5" />}
          value={signedCents(realized)}
          hint={`Today ${signedCents(summary.data?.realized_today_cents)}`}
          accent={realized > 0 ? "positive" : realized < 0 ? "negative" : "default"}
        />
        <StatCard
          label="Day change"
          icon={<Activity className="size-3.5" />}
          value={
            summary.data?.day_change_available ? signedCents(dayChange) : "—"
          }
          hint={
            summary.data?.day_change_available
              ? "Since UTC midnight"
              : "Needs ≥2 snapshots today"
          }
          accent={
            summary.data?.day_change_available
              ? dayChange > 0
                ? "positive"
                : dayChange < 0
                  ? "negative"
                  : "default"
              : "default"
          }
        />
        <StatCard
          label="Open positions"
          icon={<Briefcase className="size-3.5" />}
          value={num(summary.data?.open_positions ?? 0)}
          hint={
            summary.data
              ? `${summary.data.priced_positions}/${summary.data.open_positions} priced`
              : "-"
          }
        />
        <StatCard
          label="Last 7d realised"
          icon={<PercentCircle className="size-3.5" />}
          value={signedCents(summary.data?.realized_week_cents)}
          hint={`30d ${signedCents(summary.data?.realized_month_cents)}`}
        />
      </div>

      <Card>
        <CardHeader className="flex-row items-start justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2">
              <LineChartIcon className="size-4" /> Equity curve
            </CardTitle>
            <CardDescription>
              Cash + mark-to-market positions over time. Snapshots every 5
              minutes.
            </CardDescription>
          </div>
          <div className="flex gap-1">
            {WINDOWS.map((w) => (
              <Button
                key={w.duration}
                size="xs"
                variant={window === w.duration ? "default" : "outline"}
                onClick={() => setWindow(w.duration)}
              >
                {w.label}
              </Button>
            ))}
          </div>
        </CardHeader>
        <CardContent className="h-[320px]">
          {history.isLoading ? (
            <Skeleton className="h-full w-full" />
          ) : curve.length === 0 ? (
            <EmptyChart
              title="No snapshots yet"
              hint="Snapshots are written every 5 min — check back shortly or let the engine run a bit longer."
            />
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={curve}>
                <defs>
                  <linearGradient id="equityFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#6366f1" stopOpacity={0.35} />
                    <stop offset="100%" stopColor="#6366f1" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="cashFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#10b981" stopOpacity={0.2} />
                    <stop offset="100%" stopColor="#10b981" stopOpacity={0} />
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
                  tickFormatter={(v) => `$${compact(Number(v))}`}
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
                  formatter={(value, name) => {
                    const n = Number(value) || 0
                    return [
                      `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
                      String(name),
                    ]
                  }}
                />
                <Legend wrapperStyle={{ fontSize: 12 }} />
                <Area
                  type="monotone"
                  dataKey="equity"
                  name="Equity"
                  stroke="#6366f1"
                  strokeWidth={2}
                  fill="url(#equityFill)"
                />
                <Area
                  type="monotone"
                  dataKey="cash"
                  name="Cash"
                  stroke="#10b981"
                  strokeWidth={1.5}
                  fill="url(#cashFill)"
                />
                <Area
                  type="monotone"
                  dataKey="positions"
                  name="Positions MTM"
                  stroke="#f59e0b"
                  strokeWidth={1.5}
                  fill="transparent"
                />
              </AreaChart>
            </ResponsiveContainer>
          )}
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <BarChart3 className="size-4" /> Realised vs unrealised
            </CardTitle>
            <CardDescription>
              How much of your P&amp;L is locked in vs still in flight.
            </CardDescription>
          </CardHeader>
          <CardContent className="h-[280px]">
            {history.isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : realizedVsUnrealized.length === 0 ? (
              <EmptyChart title="No history yet" hint="Let the engine accumulate snapshots." />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={realizedVsUnrealized}>
                  <defs>
                    <linearGradient id="realisedFill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#10b981" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                    <linearGradient id="unrealisedFill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#6366f1" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#6366f1" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                  <XAxis dataKey="label" fontSize={10} tickLine={false} axisLine={false} minTickGap={40} />
                  <YAxis fontSize={10} tickLine={false} axisLine={false} width={48} tickFormatter={(v) => `$${compact(Number(v))}`} />
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
                  <Area
                    type="monotone"
                    dataKey="realized"
                    name="Realised"
                    stroke="#10b981"
                    strokeWidth={1.5}
                    fill="url(#realisedFill)"
                  />
                  <Area
                    type="monotone"
                    dataKey="unrealized"
                    name="Unrealised"
                    stroke="#6366f1"
                    strokeWidth={1.5}
                    fill="url(#unrealisedFill)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ArrowUpDown className="size-4" /> Positions leaderboard
            </CardTitle>
            <CardDescription>
              Best and worst open positions by unrealised $.
            </CardDescription>
          </CardHeader>
          <CardContent className="h-[280px]">
            {positions.isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : sortedPositions.length === 0 ? (
              <EmptyChart title="No open positions" hint="Buy something to populate this chart." />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={sortedPositions.map((p) => ({
                    symbol: p.symbol,
                    unrealized: p.unrealized_cents / 100,
                  }))}
                  layout="vertical"
                  margin={{ left: 40 }}
                >
                  <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                  <XAxis
                    type="number"
                    fontSize={10}
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v) => `$${compact(Number(v))}`}
                  />
                  <YAxis type="category" dataKey="symbol" fontSize={10} width={48} tickLine={false} axisLine={false} />
                  <Tooltip
                    contentStyle={{
                      background: "var(--popover)",
                      border: "1px solid var(--border)",
                      borderRadius: 8,
                      fontSize: 12,
                    }}
                    formatter={(value) => {
                      const n = Number(value) || 0
                      return [`${n >= 0 ? "+" : ""}$${n.toFixed(2)}`, "Unrealised"]
                    }}
                  />
                  <Bar dataKey="unrealized" radius={[0, 4, 4, 0]}>
                    {sortedPositions.map((p) => (
                      <Cell
                        key={p.symbol}
                        fill={p.unrealized_cents >= 0 ? "#10b981" : "#ef4444"}
                      />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Briefcase className="size-4" /> Open positions
          </CardTitle>
          <CardDescription>
            {sortedPositions.length} held · {winners.length} winners · {losers.length} losers
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="bg-muted/50 text-muted-foreground border-y text-left text-[11px] uppercase tracking-wider">
                  <th className="px-3 py-2">Symbol</th>
                  <th className="px-3 py-2 text-right">Qty</th>
                  <th className="px-3 py-2 text-right">Avg cost</th>
                  <th className="px-3 py-2 text-right">Mark</th>
                  <th className="px-3 py-2 text-right">Market value</th>
                  <th className="px-3 py-2 text-right">Unrealised $</th>
                  <th className="px-3 py-2 text-right">%</th>
                  <th className="px-3 py-2 text-right">Realised</th>
                  <th className="px-3 py-2 text-right">Quoted</th>
                </tr>
              </thead>
              <tbody>
                {sortedPositions.length === 0 && (
                  <tr>
                    <td colSpan={9} className="text-muted-foreground px-3 py-6 text-center text-sm">
                      No open positions.
                    </td>
                  </tr>
                )}
                {sortedPositions.map((p) => (
                  <tr key={p.symbol} className="hover:bg-muted/30 border-b last:border-b-0">
                    <td className="px-3 py-2 font-medium">{p.symbol}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{num(p.quantity, 4)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{cents(p.avg_cost_cents)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{cents(p.mark_cents)}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{cents(p.market_value_cents)}</td>
                    <td
                      className={cn(
                        "px-3 py-2 text-right tabular-nums font-medium",
                        p.unrealized_cents > 0 && "text-emerald-500",
                        p.unrealized_cents < 0 && "text-destructive",
                      )}
                    >
                      {signedCents(p.unrealized_cents)}
                    </td>
                    <td
                      className={cn(
                        "px-3 py-2 text-right tabular-nums",
                        p.unrealized_pct > 0 && "text-emerald-500",
                        p.unrealized_pct < 0 && "text-destructive",
                      )}
                    >
                      {signedPercent(p.unrealized_pct)}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{cents(p.realized_cents)}</td>
                    <td className="px-3 py-2 text-right">
                      {p.priced ? (
                        <Badge variant="outline" className="text-[10px]">
                          live
                        </Badge>
                      ) : (
                        <Badge
                          variant="outline"
                          className="text-muted-foreground text-[10px]"
                          title="No live quote available; marked at cost."
                        >
                          at cost
                        </Badge>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

// compact formats a dollar magnitude for axis ticks. Plain recharts
// defaults look like "10000" which is hard to read; "$10k" fits
// better in a 48px left gutter.
function compact(v: number): string {
  const abs = Math.abs(v)
  if (abs >= 1e9) return `${(v / 1e9).toFixed(1)}b`
  if (abs >= 1e6) return `${(v / 1e6).toFixed(1)}m`
  if (abs >= 1e3) return `${(v / 1e3).toFixed(1)}k`
  return v.toFixed(0)
}

// formatBucket picks a label granularity appropriate for the window
// so a 24h chart doesn't squash timestamps and a 90d chart doesn't
// blur them all into the same day.
function formatBucket(iso: string, window: WindowKey): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  if (window === "24h") {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
  }
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

function EmptyChart({ title, hint }: { title: string; hint: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-1 text-center text-sm">
      <div className="font-medium">{title}</div>
      <div className="text-muted-foreground text-xs">{hint}</div>
    </div>
  )
}
