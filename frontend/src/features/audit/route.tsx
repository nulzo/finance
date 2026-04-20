import { useMemo } from "react"
import type { ColumnDef } from "@tanstack/react-table"

import { DateCell, TruncatedCell } from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { useAudit } from "@/features/audit/api"
import type { AuditRow } from "@/types/api"

export function AuditRoute() {
  const { data, isLoading, error } = useAudit(500)

  const rows = useMemo(() => data ?? [], [data])

  // We accept a permissive AuditRow shape so we render opportunistically
  // — the backend returns whatever columns the audit table has.
  const keys = useMemo(() => {
    const set = new Set<string>()
    rows.forEach((r) => Object.keys(r).forEach((k) => set.add(k)))
    return [...set]
  }, [rows])

  const prefer = ["created_at", "entity", "entity_id", "action", "detail", "id"]
  const ordered = [
    ...prefer.filter((k) => keys.includes(k)),
    ...keys.filter((k) => !prefer.includes(k)),
  ]

  const columns: ColumnDef<AuditRow>[] = ordered.map((k) => ({
    accessorKey: k,
    header: k.replace(/_/g, " "),
    cell: ({ getValue }) => {
      const v = getValue()
      if (v == null || v === "") return <span className="text-muted-foreground">-</span>
      if (k === "created_at" || k.endsWith("_at")) return <DateCell value={String(v)} />
      if (k === "id" || k.endsWith("_id"))
        return <span className="font-mono text-xs">{String(v).slice(0, 12)}</span>
      if (k === "action")
        return <Badge variant="outline" className="uppercase">{String(v)}</Badge>
      if (typeof v === "object") return <TruncatedCell value={JSON.stringify(v)} max={60} />
      return <TruncatedCell value={String(v)} max={80} />
    },
  }))

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Audit log"
        description="Every state change recorded by the backend."
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={rows}
          exportName="audit"
          searchPlaceholder="Search audit log…"
          initialPageSize={50}
        />
      )}
    </div>
  )
}
