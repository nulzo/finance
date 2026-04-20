import type { ColumnDef } from "@tanstack/react-table"
import { Trash2 } from "lucide-react"
import { toast } from "sonner"

import { DateCell, Symbol, TruncatedCell } from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { PortfolioSwitcher } from "@/components/layouts/portfolio-switcher"
import { QueryError } from "@/components/errors/query-error"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useClearCooldown, useCooldowns } from "@/features/cooldowns/api"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"
import type { Cooldown } from "@/types/api"
import { relative } from "@/utils/format"

export function CooldownsRoute() {
  const { portfolio, portfolioId } = useCurrentPortfolio()
  const { data, isLoading, error } = useCooldowns(portfolioId)
  const clear = useClearCooldown(portfolioId)

  async function handleClear(sym: string) {
    try {
      await clear.mutateAsync(sym)
      toast.success(`Cooldown cleared for ${sym}`)
    } catch (e) {
      toast.error(
        `Failed to clear cooldown for ${sym}: ${
          e instanceof Error ? e.message : "unknown error"
        }`,
      )
    }
  }

  const columns: ColumnDef<Cooldown>[] = [
    {
      accessorKey: "symbol",
      header: "Symbol",
      cell: ({ getValue }) => <Symbol value={String(getValue())} />,
    },
    {
      accessorKey: "reason",
      header: "Reason",
      cell: ({ getValue }) => (
        <TruncatedCell value={String(getValue() ?? "")} max={80} />
      ),
    },
    {
      accessorKey: "until_ts",
      header: "Until",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      id: "expires",
      header: "Expires",
      cell: ({ row }) => (
        <span className="text-muted-foreground text-xs">
          {relative(row.original.until_ts)}
        </span>
      ),
    },
    {
      accessorKey: "updated_at",
      header: "Set at",
      cell: ({ getValue }) => <DateCell value={String(getValue())} />,
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <Button
          size="sm"
          variant="ghost"
          className="h-7 px-2 text-muted-foreground hover:text-destructive"
          onClick={() => handleClear(row.original.symbol)}
          disabled={
            clear.isPending && clear.variables === row.original.symbol
          }
          title="Clear cooldown"
        >
          <Trash2 className="size-3.5" />
          <span className="sr-only">Clear cooldown</span>
        </Button>
      ),
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Cooldowns"
        description={
          portfolio
            ? `Symbols the engine is temporarily steering around for ${portfolio.name}. Set by the risk engine and broker rejections; exit-policy sells bypass. Use the trash icon to release a symbol early.`
            : "Select a portfolio to see cooldowns."
        }
        actions={<PortfolioSwitcher />}
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[240px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="cooldowns"
          searchPlaceholder="Search cooldowns by symbol, reason…"
        />
      )}
    </div>
  )
}
