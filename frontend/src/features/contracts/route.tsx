import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import {
  DateCell,
  Dollars,
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
import { useContracts } from "@/features/contracts/api"
import type { GovContract } from "@/types/api"
import { num } from "@/utils/format"

const sinceOptions = [
  { label: "Last 30d", value: "30d" },
  { label: "Last 90d", value: "90d" },
  { label: "Last 180d", value: "180d" },
  { label: "Last 365d", value: "365d" },
]

export function ContractsRoute() {
  const [symbol, setSymbol] = useState("")
  const [since, setSince] = useState("90d")

  const { data, isLoading, error } = useContracts({
    symbol: symbol || undefined,
    since,
    limit: 500,
  })
  const rows = useMemo<GovContract[]>(() => data ?? [], [data])

  const stats = useMemo(() => {
    let totalUSD = 0
    const symbols = new Set<string>()
    const agencies = new Set<string>()
    for (const r of rows) {
      symbols.add(r.symbol)
      agencies.add(r.agency)
      totalUSD += r.amount_usd
    }
    return {
      total: rows.length,
      symbols: symbols.size,
      agencies: agencies.size,
      totalUSD,
    }
  }, [rows])

  const topTickers = useMemo(() => {
    const freq = new Map<string, number>()
    for (const r of rows) {
      freq.set(r.symbol, (freq.get(r.symbol) ?? 0) + r.amount_usd)
    }
    return [...freq.entries()].sort((a, b) => b[1] - a[1]).slice(0, 5)
  }, [rows])

  const columns: ColumnDef<GovContract>[] = [
    {
      accessorKey: "awarded_at",
      header: "Awarded",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      accessorKey: "symbol",
      header: "Symbol",
      cell: ({ getValue }) => <Symbol value={String(getValue())} />,
    },
    {
      accessorKey: "agency",
      header: "Agency",
      cell: ({ getValue }) => (
        <TruncatedCell value={String(getValue() ?? "")} max={40} />
      ),
    },
    {
      accessorKey: "description",
      header: "Description",
      cell: ({ getValue }) => (
        <TruncatedCell value={String(getValue() ?? "")} max={80} />
      ),
    },
    {
      accessorKey: "amount_usd",
      header: "Amount",
      cell: ({ getValue }) => <Dollars value={Number(getValue() ?? 0)} />,
    },
    { accessorKey: "source", header: "Source" },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Government contracts"
        description="Federal contract awards tied to public tickers. Large new awards feed the LLM as bullish fundamental context."
      />

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Awards" value={stats.total.toString()} />
        <StatCard label="Symbols" value={stats.symbols.toString()} />
        <StatCard label="Agencies" value={stats.agencies.toString()} />
        <StatCard label="Total awarded" value={`$${num(stats.totalUSD)}`} />
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
              placeholder="LMT"
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
          {topTickers.length > 0 && (
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
                Top $-awarded
              </label>
              <div className="flex flex-wrap gap-1">
                {topTickers.map(([sym, usd]) => (
                  <button
                    key={sym}
                    onClick={() => setSymbol(sym)}
                    type="button"
                    className="bg-muted hover:bg-muted/70 text-muted-foreground inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-xs"
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
          exportName="gov-contracts"
          searchPlaceholder="Search agency, description, symbol…"
          initialPageSize={50}
          exportOmit={["raw_hash"]}
        />
      )}
    </div>
  )
}
