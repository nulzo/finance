import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import {
  DateCell,
  Dollars,
  Mono,
  SideBadge,
  Symbol,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { PortfolioSwitcher } from "@/components/layouts/portfolio-switcher"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent } from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useRejections } from "@/features/rejections/api"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"
import { cn } from "@/lib/utils"
import type { Rejection, RejectionSource, Side } from "@/types/api"

// Badge palette per source — keeps the table legible at a glance and
// matches the colour conventions used on Cooldowns / Orders.
const sourceColor: Record<RejectionSource, string> = {
  risk: "bg-destructive/15 text-destructive",
  broker: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  engine: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
}

function SourceBadge({ value }: { value: RejectionSource }) {
  return (
    <Badge className={cn("uppercase", sourceColor[value] ?? "bg-muted")}>
      {value}
    </Badge>
  )
}

const sinceOptions = [
  { label: "Last 1h", value: "1h" },
  { label: "Last 6h", value: "6h" },
  { label: "Last 24h", value: "24h" },
  { label: "Last 7d", value: "168h" },
]

const sourceOptions: Array<{ label: string; value: "" | RejectionSource }> = [
  { label: "All sources", value: "" },
  { label: "Risk engine", value: "risk" },
  { label: "Broker", value: "broker" },
  { label: "Engine short-circuits", value: "engine" },
]

export function RejectionsRoute() {
  const { portfolio, portfolioId } = useCurrentPortfolio()
  const [since, setSince] = useState("24h")
  const [source, setSource] = useState<"" | RejectionSource>("")

  const { data, isLoading, error } = useRejections(portfolioId, { since, source })
  // Memoise so downstream useMemo deps stay referentially stable across
  // renders where the server hasn't returned new data — otherwise we
  // re-aggregate on every render, which the lint rule correctly flags.
  const rows = useMemo(() => data ?? [], [data])

  const counts = useMemo(() => {
    const c = { risk: 0, broker: 0, engine: 0 }
    for (const r of rows) {
      if (r.source in c) c[r.source]++
    }
    return c
  }, [rows])

  const topSymbols = useMemo(() => {
    const freq = new Map<string, number>()
    for (const r of rows) freq.set(r.symbol, (freq.get(r.symbol) ?? 0) + 1)
    return [...freq.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, 5)
  }, [rows])

  const columns: ColumnDef<Rejection>[] = [
    {
      accessorKey: "created_at",
      header: "When",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      accessorKey: "symbol",
      header: "Symbol",
      cell: ({ getValue }) => <Symbol value={String(getValue())} />,
    },
    {
      accessorKey: "side",
      header: "Side",
      cell: ({ getValue }) => {
        const v = String(getValue() ?? "")
        if (!v) return <span className="text-muted-foreground">-</span>
        return <SideBadge value={v as Side} />
      },
    },
    {
      accessorKey: "source",
      header: "Source",
      cell: ({ getValue }) => (
        <SourceBadge value={getValue() as RejectionSource} />
      ),
    },
    {
      accessorKey: "target_usd",
      header: "Target $",
      cell: ({ getValue }) => <Dollars value={String(getValue() ?? "0")} />,
    },
    {
      accessorKey: "reason",
      header: "Reason",
      cell: ({ getValue }) => (
        <TruncatedCell value={String(getValue() ?? "")} max={140} />
      ),
    },
    {
      accessorKey: "decision_id",
      header: "Decision",
      cell: ({ getValue }) => {
        const v = getValue() as string | null | undefined
        if (!v) return <span className="text-muted-foreground">-</span>
        return <Mono>{v.slice(0, 8)}</Mono>
      },
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Rejections"
        description={
          portfolio
            ? `Trade attempts the engine, risk controls, or broker refused for ${portfolio.name}. Use this to audit why the engine didn't trade a signal.`
            : "Select a portfolio to see rejections."
        }
        actions={<PortfolioSwitcher />}
      />

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Total" value={rows.length.toString()} />
        <StatCard
          label="Risk"
          value={counts.risk.toString()}
          accent={counts.risk > 0 ? "negative" : "default"}
        />
        <StatCard
          label="Broker"
          value={counts.broker.toString()}
          accent={counts.broker > 0 ? "negative" : "default"}
        />
        <StatCard
          label="Engine"
          value={counts.engine.toString()}
          accent={counts.engine > 0 ? "negative" : "default"}
        />
      </div>

      <Card>
        <CardContent className="flex flex-wrap items-end gap-3 py-4">
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
              Window
            </label>
            <Select value={since} onValueChange={setSince}>
              <SelectTrigger className="w-[160px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {sinceOptions.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
              Source
            </label>
            <Select
              value={source || "all"}
              onValueChange={(v) =>
                setSource(v === "all" ? "" : (v as RejectionSource))
              }
            >
              <SelectTrigger className="w-[180px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {sourceOptions.map((o) => (
                  <SelectItem key={o.value || "all"} value={o.value || "all"}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {topSymbols.length > 0 && (
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
                Top symbols
              </label>
              <div className="flex flex-wrap gap-1">
                {topSymbols.map(([sym, n]) => (
                  <span
                    key={sym}
                    className="bg-muted text-muted-foreground inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-xs"
                  >
                    {sym}
                    <span className="text-foreground/70">×{n}</span>
                  </span>
                ))}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[320px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={rows}
          exportName="rejections"
          searchPlaceholder="Search rejections by symbol, reason, source…"
        />
      )}
    </div>
  )
}
