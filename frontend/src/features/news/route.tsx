import type { ColumnDef } from "@tanstack/react-table"
import { ExternalLink } from "lucide-react"

import { DateCell, TruncatedCell } from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { useNews } from "@/features/news/api"
import type { NewsItem } from "@/types/api"
import { cn } from "@/lib/utils"

export function NewsRoute() {
  const { data, isLoading, error } = useNews(500)

  const columns: ColumnDef<NewsItem>[] = [
    { accessorKey: "pub_at", header: "Published", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "source", header: "Source" },
    {
      accessorKey: "title",
      header: "Headline",
      cell: ({ row }) => (
        <a
          href={row.original.url}
          target="_blank"
          rel="noreferrer"
          className="group flex items-center gap-1.5 font-medium hover:underline"
        >
          <span className="line-clamp-1">{row.original.title}</span>
          <ExternalLink className="text-muted-foreground size-3 shrink-0 opacity-0 group-hover:opacity-100" />
        </a>
      ),
    },
    {
      accessorKey: "summary",
      header: "Summary",
      cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={100} />,
    },
    {
      accessorKey: "symbols",
      header: "Symbols",
      cell: ({ getValue }) => {
        const s = String(getValue() ?? "")
        if (!s) return <span className="text-muted-foreground">-</span>
        return (
          <div className="flex flex-wrap gap-1">
            {s.split(",").slice(0, 4).map((sym) => (
              <span key={sym} className="bg-muted rounded px-1 py-0.5 font-mono text-[10px]">
                {sym.trim()}
              </span>
            ))}
          </div>
        )
      },
    },
    {
      accessorKey: "sentiment",
      header: "Sent.",
      cell: ({ getValue }) => {
        const n = Number(getValue())
        return (
          <span
            className={cn(
              "font-mono tabular-nums text-xs",
              n > 0.1 ? "text-emerald-500" : n < -0.1 ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {n.toFixed(2)}
          </span>
        )
      },
    },
    {
      accessorKey: "relevance",
      header: "Rel.",
      cell: ({ getValue }) => {
        const n = Number(getValue())
        return (
          <Badge variant="outline" className="tabular-nums">
            {n.toFixed(2)}
          </Badge>
        )
      },
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="News" description="Most recent market news ingested by the engine." />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="news"
          searchPlaceholder="Search headlines, symbols, sources…"
          initialPageSize={50}
        />
      )}
    </div>
  )
}
