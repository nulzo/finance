import type { ColumnDef } from "@tanstack/react-table"

import {
  DateCell,
  Decimal,
  MoneyCents,
  Symbol,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { PortfolioSwitcher } from "@/components/layouts/portfolio-switcher"
import { QueryError } from "@/components/errors/query-error"
import { Skeleton } from "@/components/ui/skeleton"
import { usePositions } from "@/features/positions/api"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"
import type { Position } from "@/types/api"

export function PositionsRoute() {
  const { portfolio, portfolioId } = useCurrentPortfolio()
  const { data, isLoading, error } = usePositions(portfolioId)

  const columns: ColumnDef<Position>[] = [
    { accessorKey: "symbol", header: "Symbol", cell: ({ getValue }) => <Symbol value={String(getValue())} /> },
    { accessorKey: "quantity", header: "Qty", cell: ({ getValue }) => <Decimal value={String(getValue())} digits={4} /> },
    { accessorKey: "avg_cost_cents", header: "Avg cost", cell: ({ getValue }) => <MoneyCents value={Number(getValue())} /> },
    {
      id: "cost_basis",
      header: "Cost basis",
      cell: ({ row }) => (
        <MoneyCents value={Math.round(Number(row.original.quantity) * row.original.avg_cost_cents)} />
      ),
    },
    { accessorKey: "realized_cents", header: "Realized P/L", cell: ({ getValue }) => <MoneyCents value={Number(getValue())} /> },
    { accessorKey: "updated_at", header: "Updated", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "id", header: "ID", cell: ({ getValue }) => <span className="font-mono text-xs">{String(getValue()).slice(0, 8)}</span> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Positions"
        description={
          portfolio
            ? `Open positions for ${portfolio.name}.`
            : "Select a portfolio to see positions."
        }
        actions={<PortfolioSwitcher />}
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="positions"
          searchPlaceholder="Search positions…"
        />
      )}
    </div>
  )
}
