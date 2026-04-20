import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import { DateCell, Symbol } from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
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
import { useSocial, type SocialPlatform } from "@/features/social/api"
import { cn } from "@/lib/utils"
import type { SocialPost } from "@/types/api"
import { num } from "@/utils/format"

const sinceOptions = [
  { label: "Last 6h", value: "6h" },
  { label: "Last 24h", value: "24h" },
  { label: "Last 48h", value: "48h" },
  { label: "Last 7d", value: "168h" },
]

const platformOptions: Array<{ label: string; value: SocialPlatform }> = [
  { label: "All platforms", value: "" },
  { label: "WallStreetBets", value: "wsb" },
  { label: "Twitter", value: "twitter" },
]

function PlatformBadge({ value }: { value: string }) {
  const v = value.toLowerCase()
  return (
    <Badge
      className={cn(
        "uppercase",
        v === "wsb" && "bg-orange-500/15 text-orange-600 dark:text-orange-400",
        v === "twitter" && "bg-sky-500/15 text-sky-600 dark:text-sky-400",
        !["wsb", "twitter"].includes(v) && "bg-muted text-muted-foreground",
      )}
    >
      {value || "—"}
    </Badge>
  )
}

function SentimentCell({ value }: { value: number }) {
  if (value === 0) {
    return <span className="text-muted-foreground tabular-nums">0.00</span>
  }
  const pos = value > 0
  return (
    <span
      className={cn(
        "tabular-nums font-mono",
        pos ? "text-emerald-500" : "text-destructive",
      )}
    >
      {pos ? "+" : ""}
      {value.toFixed(2)}
    </span>
  )
}

export function SocialRoute() {
  const [symbol, setSymbol] = useState("")
  const [platform, setPlatform] = useState<SocialPlatform>("")
  const [since, setSince] = useState("48h")

  const { data, isLoading, error } = useSocial({
    symbol: symbol || undefined,
    platform,
    since,
    limit: 500,
  })
  const rows = useMemo<SocialPost[]>(() => data ?? [], [data])

  const stats = useMemo(() => {
    let totalMentions = 0
    let weightedSentiment = 0
    const symbols = new Set<string>()
    for (const r of rows) {
      symbols.add(r.symbol)
      totalMentions += r.mentions
      weightedSentiment += r.sentiment * r.mentions
    }
    const avg = totalMentions > 0 ? weightedSentiment / totalMentions : 0
    return {
      total: rows.length,
      symbols: symbols.size,
      mentions: totalMentions,
      sentiment: avg,
    }
  }, [rows])

  const topTickers = useMemo(() => {
    const freq = new Map<string, number>()
    for (const r of rows) {
      freq.set(r.symbol, (freq.get(r.symbol) ?? 0) + r.mentions)
    }
    return [...freq.entries()].sort((a, b) => b[1] - a[1]).slice(0, 6)
  }, [rows])

  const columns: ColumnDef<SocialPost>[] = [
    {
      accessorKey: "bucket_at",
      header: "Bucket",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      accessorKey: "symbol",
      header: "Symbol",
      cell: ({ getValue }) => <Symbol value={String(getValue())} />,
    },
    {
      accessorKey: "platform",
      header: "Platform",
      cell: ({ getValue }) => <PlatformBadge value={String(getValue() ?? "")} />,
    },
    {
      accessorKey: "mentions",
      header: "Mentions",
      cell: ({ getValue }) => (
        <span className="tabular-nums">{num(Number(getValue() ?? 0))}</span>
      ),
    },
    {
      accessorKey: "sentiment",
      header: "Sentiment",
      cell: ({ getValue }) => <SentimentCell value={Number(getValue() ?? 0)} />,
    },
    {
      accessorKey: "followers",
      header: "Reach",
      cell: ({ getValue }) => {
        const v = Number(getValue() ?? 0)
        if (v === 0) return <span className="text-muted-foreground">—</span>
        return <span className="tabular-nums">{num(v)}</span>
      },
    },
    { accessorKey: "source", header: "Source" },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Social buzz"
        description="WallStreetBets and Twitter mention rollups from Quiver. SocialBuzz strategy trades against the volume+sentiment combination."
      />

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Rows" value={stats.total.toString()} />
        <StatCard label="Symbols" value={stats.symbols.toString()} />
        <StatCard label="Mentions" value={num(stats.mentions)} />
        <StatCard
          label="Weighted sentiment"
          value={stats.sentiment.toFixed(2)}
          accent={
            stats.sentiment > 0.1
              ? "positive"
              : stats.sentiment < -0.1
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
              placeholder="GME"
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
              Platform
            </label>
            <Select
              value={platform || "all"}
              onValueChange={(v) =>
                setPlatform(v === "all" ? "" : (v as SocialPlatform))
              }
            >
              <SelectTrigger className="w-[170px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {platformOptions.map((o) => (
                  <SelectItem key={o.value || "all"} value={o.value || "all"}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {topTickers.length > 0 && (
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-[11px] uppercase tracking-wider">
                Top mention volume
              </label>
              <div className="flex flex-wrap gap-1">
                {topTickers.map(([sym, n]) => (
                  <button
                    key={sym}
                    onClick={() => setSymbol(sym)}
                    type="button"
                    className="bg-muted hover:bg-muted/70 text-muted-foreground inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-xs"
                    title={`Set filter to ${sym}`}
                  >
                    {sym}
                    <span className="text-foreground/70">×{num(n)}</span>
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
          exportName="social"
          searchPlaceholder="Search symbol, platform, source…"
          initialPageSize={50}
          exportOmit={["raw_hash"]}
        />
      )}
    </div>
  )
}
