import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import {
  DateCell,
  Dollars,
  SideBadge,
  Symbol,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useInsiders } from "@/features/insiders/api"
import type { InsiderTrade, Side } from "@/types/api"
import { num } from "@/utils/format"

// Windows that map cleanly onto the backend default (30d). We pick
// a short end (7d) for active desks and a quarter for context.
const sinceOptions = [
  { label: "Last 7d", value: "7d" },
  { label: "Last 30d", value: "30d" },
  { label: "Last 90d", value: "90d" },
]

const sideOptions: Array<{ label: string; value: "" | Side }> = [
  { label: "Buys + sells", value: "" },
  { label: "Buys only", value: "buy" },
  { label: "Sells only", value: "sell" },
]

export function InsidersRoute() {
  const [symbol, setSymbol] = useState("")
  const [side, setSide] = useState<"" | Side>("")
  const [since, setSince] = useState("30d")

  const { data, isLoading, error } = useInsiders({
    symbol: symbol || undefined,
    side,
    since,
    limit: 500,
  })
  const rows = useMemo<InsiderTrade[]>(() => data ?? [], [data])

  // Aggregate buy vs sell dollar volume — a first-order signal the
  // InsiderFollow strategy also uses for scoring.
  const stats = useMemo(() => {
    let buyUSD = 0
    let sellUSD = 0
    const symbols = new Set<string>()
    for (const r of rows) {
      symbols.add(r.symbol)
      if (r.side === "buy") buyUSD += r.value_usd
      else if (r.side === "sell") sellUSD += r.value_usd
    }
    return {
      total: rows.length,
      symbols: symbols.size,
      buyUSD,
      sellUSD,
      netUSD: buyUSD - sellUSD,
    }
  }, [rows])

  const topBuyers = useMemo(() => {
    const freq = new Map<string, number>()
    for (const r of rows) {
      if (r.side !== "buy") continue
      freq.set(r.symbol, (freq.get(r.symbol) ?? 0) + r.value_usd)
    }
    return [...freq.entries()].sort((a, b) => b[1] - a[1]).slice(0, 5)
  }, [rows])

  const columns: ColumnDef<InsiderTrade>[] = [
    {
      accessorKey: "filed_at",
      header: "Filed",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      accessorKey: "transacted_at",
      header: "Traded",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      accessorKey: "symbol",
      header: "Symbol",
      cell: ({ getValue }) => <Symbol value={String(getValue())} />,
    },
    {
      accessorKey: "insider_name",
      header: "Insider",
      cell: ({ getValue }) => (
        <TruncatedCell value={String(getValue() ?? "")} max={32} />
      ),
    },
    {
      accessorKey: "insider_title",
      header: "Title",
      cell: ({ getValue }) => (
        <TruncatedCell value={String(getValue() ?? "")} max={32} />
      ),
    },
    {
      accessorKey: "side",
      header: "Side",
      cell: ({ getValue }) => <SideBadge value={getValue() as Side} />,
    },
    {
      accessorKey: "shares",
      header: "Shares",
      cell: ({ getValue }) => (
        <span className="tabular-nums">{num(Number(getValue() ?? 0))}</span>
      ),
    },
    {
      accessorKey: "value_usd",
      header: "Value",
      cell: ({ getValue }) => <Dollars value={Number(getValue() ?? 0)} />,
    },
    { accessorKey: "source", header: "Source" },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Insider trades"
        description="SEC Form 4 filings pulled from Quiver. Cluster buys from executive officers are a positive signal for the InsiderFollow strategy."
      />

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Filings" value={stats.total.toString()} />
        <StatCard label="Symbols" value={stats.symbols.toString()} />
        <StatCard
          label="Buy USD"
          value={`$${num(stats.buyUSD)}`}
          accent={stats.buyUSD > 0 ? "positive" : "default"}
        />
        <StatCard
          label="Net USD (buy-sell)"
          value={`$${num(stats.netUSD)}`}
          accent={
            stats.netUSD > 0
              ? "positive"
              : stats.netUSD < 0
                ? "negative"
                : "default"
          }
        />
      </div>

      <Card>
        <CardContent className="flex flex-wrap items-end gap-3 py-4">
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
              Symbol
            </label>
            <Input
              value={symbol}
              onChange={(e) => setSymbol(e.target.value.toUpperCase())}
              placeholder="AAPL"
              className="w-[140px] uppercase"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
              Window
            </label>
            <Select value={since} onValueChange={setSince}>
              <SelectTrigger className="w-[140px]">
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
              Side
            </label>
            <Select
              value={side || "all"}
              onValueChange={(v) => setSide(v === "all" ? "" : (v as Side))}
            >
              <SelectTrigger className="w-[160px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {sideOptions.map((o) => (
                  <SelectItem key={o.value || "all"} value={o.value || "all"}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {topBuyers.length > 0 && (
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
                Top buy-$ tickers
              </label>
              <div className="flex flex-wrap gap-1">
                {topBuyers.map(([sym, usd]) => (
                  <button
                    key={sym}
                    onClick={() => setSymbol(sym)}
                    className="bg-muted hover:bg-muted/70 text-muted-foreground inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-xs"
                    type="button"
                    title={`Set filter to ${sym}`}
                  >
                    {sym}
                    <span className="text-foreground/70">${num(usd)}</span>
                  </button>
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
          exportName="insiders"
          searchPlaceholder="Search insider name, title, symbol…"
          initialPageSize={50}
          exportOmit={["raw_hash"]}
        />
      )}
    </div>
  )
}
