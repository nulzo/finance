import { useState, type ReactNode } from "react"
import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table"
import type {
  ColumnDef,
  ColumnFiltersState,
  SortingState,
  VisibilityState,
} from "@tanstack/react-table"
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Columns3,
  Download,
  FileJson,
  Search,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"
import { downloadCSV, downloadJSON, toCSV } from "@/utils/csv"

interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[]
  data: TData[]
  /** Prefix for CSV/JSON file names, e.g. "orders". */
  exportName?: string
  /** Global free-text search placeholder. */
  searchPlaceholder?: string
  /** Columns to exclude from the export. */
  exportOmit?: string[]
  /** Rendered to the right of the toolbar (e.g. refresh, create button). */
  toolbarRight?: ReactNode
  /** Initial page size. */
  initialPageSize?: number
  /** Whether to show the toolbar (search + column toggle + download). */
  toolbar?: boolean
  /** Message shown when the data set is empty. */
  emptyLabel?: string
  /** Triggered when the user clicks a row (ignored for cells with role=button). */
  onRowClick?: (row: TData) => void
}

/** A tanstack-table wrapper with: global search, column visibility,
 *  sortable headers, pagination, row actions and CSV/JSON export.
 *  Intentionally small — feature tables compose on top of it. */
export function DataTable<TData, TValue>({
  columns,
  data,
  exportName = "export",
  searchPlaceholder = "Search…",
  exportOmit,
  toolbarRight,
  initialPageSize = 25,
  toolbar = true,
  emptyLabel = "No results.",
  onRowClick,
}: DataTableProps<TData, TValue>) {
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  const [globalFilter, setGlobalFilter] = useState("")

  const table = useReactTable({
    data,
    columns,
    state: { sorting, columnFilters, columnVisibility, globalFilter },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onColumnVisibilityChange: setColumnVisibility,
    onGlobalFilterChange: setGlobalFilter,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize: initialPageSize } },
    globalFilterFn: "includesString",
  })

  const exportData = () => {
    const omit = new Set(exportOmit ?? [])
    const rows = table
      .getFilteredRowModel()
      .rows.map((r) => r.original as Record<string, unknown>)
    const cols = Object.keys(rows[0] ?? {}).filter((k) => !omit.has(k))
    const csv = toCSV(rows, cols)
    downloadCSV(`${exportName}-${new Date().toISOString().slice(0, 10)}.csv`, csv)
  }

  const exportJson = () => {
    const rows = table.getFilteredRowModel().rows.map((r) => r.original)
    downloadJSON(`${exportName}-${new Date().toISOString().slice(0, 10)}.json`, rows)
  }

  return (
    <div className="flex w-full flex-col gap-3">
      {toolbar && (
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-[240px] flex-1">
            <Search className="text-muted-foreground absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
            <Input
              value={globalFilter}
              onChange={(e) => setGlobalFilter(e.target.value)}
              placeholder={searchPlaceholder}
              className="pl-7"
            />
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Columns3 /> Columns
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>Toggle columns</DropdownMenuLabel>
              <DropdownMenuSeparator />
              {table
                .getAllLeafColumns()
                .filter((c) => c.getCanHide())
                .map((c) => (
                  <DropdownMenuCheckboxItem
                    key={c.id}
                    checked={c.getIsVisible()}
                    onCheckedChange={(v) => c.toggleVisibility(!!v)}
                    className="capitalize"
                  >
                    {c.id.replace(/_/g, " ")}
                  </DropdownMenuCheckboxItem>
                ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Download /> Export
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuCheckboxItem
                onSelect={(e) => {
                  e.preventDefault()
                  exportData()
                }}
                checked={false}
              >
                <Download className="mr-2 size-3.5" />
                CSV
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                onSelect={(e) => {
                  e.preventDefault()
                  exportJson()
                }}
                checked={false}
              >
                <FileJson className="mr-2 size-3.5" />
                JSON
              </DropdownMenuCheckboxItem>
            </DropdownMenuContent>
          </DropdownMenu>
          {toolbarRight}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((g) => (
              <TableRow key={g.id} className="bg-muted/40">
                {g.headers.map((h) => {
                  const canSort = h.column.getCanSort()
                  const sort = h.column.getIsSorted()
                  return (
                    <TableHead key={h.id} className="text-xs font-medium">
                      {h.isPlaceholder ? null : (
                        <button
                          type="button"
                          onClick={() => canSort && h.column.toggleSorting()}
                          className={cn(
                            "inline-flex items-center gap-1",
                            canSort && "hover:text-foreground",
                          )}
                          disabled={!canSort}
                        >
                          {flexRender(
                            h.column.columnDef.header,
                            h.getContext(),
                          )}
                          {canSort &&
                            (sort === "asc" ? (
                              <ArrowUp className="size-3" />
                            ) : sort === "desc" ? (
                              <ArrowDown className="size-3" />
                            ) : (
                              <ArrowUpDown className="text-muted-foreground size-3" />
                            ))}
                        </button>
                      )}
                    </TableHead>
                  )
                })}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.id}
                  data-state={row.getIsSelected() && "selected"}
                  onClick={() => onRowClick?.(row.original as TData)}
                  className={cn(onRowClick && "cursor-pointer")}
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id} className="text-sm">
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className="text-muted-foreground h-24 text-center"
                >
                  {emptyLabel}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-muted-foreground text-xs">
          {table.getFilteredRowModel().rows.length} row
          {table.getFilteredRowModel().rows.length === 1 ? "" : "s"}
        </div>
        <div className="flex items-center gap-2">
          <div className="text-muted-foreground text-xs">Rows / page</div>
          <Select
            value={String(table.getState().pagination.pageSize)}
            onValueChange={(v) => table.setPageSize(Number(v))}
          >
            <SelectTrigger size="sm" className="w-[80px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {[10, 25, 50, 100, 250].map((n) => (
                <SelectItem key={n} value={String(n)}>
                  {n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="text-muted-foreground text-xs">
            Page {table.getState().pagination.pageIndex + 1} of{" "}
            {table.getPageCount() || 1}
          </div>
          <Button
            variant="outline"
            size="icon-sm"
            onClick={() => table.setPageIndex(0)}
            disabled={!table.getCanPreviousPage()}
          >
            <ChevronsLeft />
          </Button>
          <Button
            variant="outline"
            size="icon-sm"
            onClick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
          >
            <ChevronLeft />
          </Button>
          <Button
            variant="outline"
            size="icon-sm"
            onClick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
          >
            <ChevronRight />
          </Button>
          <Button
            variant="outline"
            size="icon-sm"
            onClick={() => table.setPageIndex(table.getPageCount() - 1)}
            disabled={!table.getCanNextPage()}
          >
            <ChevronsRight />
          </Button>
        </div>
      </div>
    </div>
  )
}
