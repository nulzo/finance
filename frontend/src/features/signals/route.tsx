import { useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import {
  ConfidenceCell,
  DateCell,
  ScoreCell,
  SideBadge,
  Symbol,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { useSignals } from "@/features/signals/api"
import type { Side, Signal } from "@/types/api"

export function SignalsRoute() {
  const [symbol, setSymbol] = useState("")
  const { data, isLoading, error } = useSignals(symbol.trim().toUpperCase() || undefined)

  const columns: ColumnDef<Signal>[] = [
    { accessorKey: "created_at", header: "Created", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    {
      accessorKey: "kind",
      header: "Kind",
      cell: ({ getValue }) => (
        <Badge variant="outline" className="capitalize">
          {String(getValue())}
        </Badge>
      ),
    },
    { accessorKey: "symbol", header: "Symbol", cell: ({ getValue }) => <Symbol value={String(getValue())} /> },
    { accessorKey: "side", header: "Side", cell: ({ getValue }) => <SideBadge value={getValue() as Side} /> },
    { accessorKey: "score", header: "Score", cell: ({ getValue }) => <ScoreCell value={Number(getValue())} /> },
    { accessorKey: "confidence", header: "Confidence", cell: ({ getValue }) => <ConfidenceCell value={Number(getValue())} /> },
    { accessorKey: "reason", header: "Reason", cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={80} /> },
    { accessorKey: "ref_id", header: "Ref", cell: ({ getValue }) => <span className="font-mono text-xs">{String(getValue() ?? "").slice(0, 12)}</span> },
    { accessorKey: "expires_at", header: "Expires", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Signals"
        description="Active trading signals assembled from news, momentum, politicians and manual entries."
        actions={
          <Input
            value={symbol}
            onChange={(e) => setSymbol(e.target.value)}
            placeholder="Filter by symbol…"
            className="max-w-[200px]"
          />
        }
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="signals"
          searchPlaceholder="Search signals…"
          initialPageSize={50}
        />
      )}
    </div>
  )
}
