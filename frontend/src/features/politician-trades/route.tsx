import type { ColumnDef } from "@tanstack/react-table"

import {
  DateCell,
  SideBadge,
  Symbol,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { usePoliticianTrades } from "@/features/politician-trades/api"
import type { PoliticianTrade, Side } from "@/types/api"
import { num } from "@/utils/format"

export function PoliticianTradesRoute() {
  const { data, isLoading, error } = usePoliticianTrades(1000)

  const columns: ColumnDef<PoliticianTrade>[] = [
    { accessorKey: "traded_at", header: "Traded", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "disclosed_at", header: "Disclosed", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "politician_name", header: "Name" },
    {
      accessorKey: "chamber",
      header: "Chamber",
      cell: ({ getValue }) => (
        <Badge variant="outline" className="capitalize">
          {String(getValue() || "—")}
        </Badge>
      ),
    },
    { accessorKey: "symbol", header: "Symbol", cell: ({ getValue }) => <Symbol value={String(getValue() ?? "—")} /> },
    { accessorKey: "asset_name", header: "Asset", cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={40} /> },
    { accessorKey: "side", header: "Side", cell: ({ getValue }) => <SideBadge value={getValue() as Side} /> },
    {
      id: "amount",
      header: "Amount USD",
      cell: ({ row }) => (
        <span className="tabular-nums">
          ${num(row.original.amount_min_usd)}–${num(row.original.amount_max_usd)}
        </span>
      ),
    },
    { accessorKey: "source", header: "Source" },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Politician trades"
        description="Recently disclosed congressional trades."
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="politician-trades"
          searchPlaceholder="Search politicians, symbols, assets…"
          initialPageSize={50}
          exportOmit={["raw_hash"]}
        />
      )}
    </div>
  )
}
