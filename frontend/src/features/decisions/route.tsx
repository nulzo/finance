import type { ColumnDef } from "@tanstack/react-table"
import { Play } from "lucide-react"
import { toast } from "sonner"

import {
  ActionBadge,
  ConfidenceCell,
  DateCell,
  Dollars,
  ScoreCell,
  Symbol,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useDecisions, useExecuteDecision } from "@/features/decisions/api"
import type { Decision, DecisionAction } from "@/types/api"

export function DecisionsRoute() {
  const { data, isLoading, error } = useDecisions(500)
  const execute = useExecuteDecision()

  const run = async (id: string) => {
    try {
      await execute.mutateAsync(id)
      toast.success("Decision executed")
    } catch (e) {
      toast.error((e as { message?: string }).message ?? "Execution failed")
    }
  }

  const columns: ColumnDef<Decision>[] = [
    { accessorKey: "created_at", header: "Decided", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "symbol", header: "Symbol", cell: ({ getValue }) => <Symbol value={String(getValue())} /> },
    {
      accessorKey: "action",
      header: "Action",
      cell: ({ getValue }) => <ActionBadge value={getValue() as DecisionAction} />,
    },
    { accessorKey: "score", header: "Score", cell: ({ getValue }) => <ScoreCell value={Number(getValue())} /> },
    { accessorKey: "confidence", header: "Confidence", cell: ({ getValue }) => <ConfidenceCell value={Number(getValue())} /> },
    { accessorKey: "target_usd", header: "Target USD", cell: ({ getValue }) => <Dollars value={String(getValue())} /> },
    { accessorKey: "model_used", header: "Model", cell: ({ getValue }) => <span className="font-mono text-xs">{String(getValue())}</span> },
    { accessorKey: "reasoning", header: "Reasoning", cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={80} /> },
    {
      accessorKey: "executed_id",
      header: "Executed",
      cell: ({ getValue }) => {
        const v = getValue() as string | null | undefined
        return v ? (
          <span className="font-mono text-xs text-emerald-500">{v.slice(0, 8)}</span>
        ) : (
          <span className="text-muted-foreground">-</span>
        )
      },
    },
    {
      id: "actions",
      header: "",
      enableSorting: false,
      cell: ({ row }) => {
        const canExec = row.original.action !== "hold" && !row.original.executed_id
        if (!canExec) return null
        return (
          <Button
            size="sm"
            variant="outline"
            onClick={(e) => {
              e.stopPropagation()
              run(row.original.id)
            }}
            disabled={execute.isPending}
          >
            <Play /> Execute
          </Button>
        )
      },
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Decisions"
        description="LLM-assisted trade decisions and their reasoning."
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="decisions"
          searchPlaceholder="Search decisions…"
          initialPageSize={50}
          exportOmit={["signal_ids"]}
        />
      )}
    </div>
  )
}
