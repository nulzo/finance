import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import type { ColumnDef } from "@tanstack/react-table"
import { ArrowLeft, Banknote, Minus, Plus, ReceiptText } from "lucide-react"
import { useForm } from "react-hook-form"

import {
  DateCell,
  Decimal,
  MoneyCents,
  Symbol,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { paths } from "@/config/paths"
import {
  useDeposit,
  usePortfolio,
  usePortfolios,
  useWithdraw,
} from "@/features/portfolios/api"
import type { Portfolio, Position } from "@/types/api"
import { cents, dateTime, num } from "@/utils/format"

export function PortfoliosRoute() {
  const { data, isLoading, error } = usePortfolios()
  const navigate = useNavigate()

  const columns: ColumnDef<Portfolio>[] = [
    {
      accessorKey: "name",
      header: "Name",
      cell: ({ row }) => (
        <Link
          to={paths.portfolio.getHref(row.original.id)}
          className="font-medium hover:underline"
        >
          {row.original.name}
        </Link>
      ),
    },
    {
      accessorKey: "mode",
      header: "Mode",
      cell: ({ getValue }) => (
        <Badge variant="outline" className="uppercase">
          {String(getValue())}
        </Badge>
      ),
    },
    {
      accessorKey: "cash_cents",
      header: "Cash",
      cell: ({ getValue }) => <MoneyCents value={Number(getValue())} />,
    },
    {
      accessorKey: "reserved_cents",
      header: "Reserved",
      cell: ({ getValue }) => <MoneyCents value={Number(getValue())} />,
    },
    {
      id: "available",
      header: "Available",
      cell: ({ row }) => (
        <MoneyCents value={row.original.cash_cents - row.original.reserved_cents} />
      ),
    },
    { accessorKey: "created_at", header: "Created", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "updated_at", header: "Updated", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "id", header: "ID", cell: ({ getValue }) => <span className="font-mono text-xs">{String(getValue()).slice(0, 8)}</span> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Portfolios"
        description="Manage your trader portfolios."
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="portfolios"
          searchPlaceholder="Search portfolios…"
          onRowClick={(r) => navigate(paths.portfolio.getHref(r.id))}
        />
      )}
    </div>
  )
}

export function PortfolioDetailRoute() {
  const { id } = useParams<{ id: string }>()
  const { data, isLoading, error } = usePortfolio(id)
  const p = data?.portfolio

  const positions = data?.positions ?? []

  const posColumns: ColumnDef<Position>[] = [
    { accessorKey: "symbol", header: "Symbol", cell: ({ getValue }) => <Symbol value={String(getValue())} /> },
    { accessorKey: "quantity", header: "Qty", cell: ({ getValue }) => <Decimal value={String(getValue())} /> },
    { accessorKey: "avg_cost_cents", header: "Avg cost", cell: ({ getValue }) => <MoneyCents value={Number(getValue())} /> },
    {
      id: "cost_basis",
      header: "Cost basis",
      cell: ({ row }) => (
        <MoneyCents value={Math.round(Number(row.original.quantity) * row.original.avg_cost_cents)} />
      ),
    },
    { accessorKey: "realized_cents", header: "Realized", cell: ({ getValue }) => <MoneyCents value={Number(getValue())} /> },
    { accessorKey: "updated_at", header: "Updated", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={p?.name ?? "Portfolio"}
        description={p ? `Mode: ${p.mode} · ID ${p.id}` : ""}
        actions={
          <>
            <Button asChild variant="outline" size="sm">
              <Link to={paths.portfolios.getHref()}>
                <ArrowLeft /> All portfolios
              </Link>
            </Button>
            {p && (
              <>
                <CashDialog portfolio={p} mode="deposit" />
                <CashDialog portfolio={p} mode="withdraw" />
              </>
            )}
            <Button asChild size="sm">
              <Link to={paths.orders.getHref()}>
                <ReceiptText /> Orders
              </Link>
            </Button>
          </>
        }
      />

      {error && <QueryError error={error} />}

      {isLoading || !p ? (
        <Skeleton className="h-[180px] w-full" />
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard label="Cash" value={cents(p.cash_cents)} icon={<Banknote className="size-3.5" />} />
          <StatCard label="Reserved" value={cents(p.reserved_cents)} />
          <StatCard
            label="Available"
            value={cents(p.cash_cents - p.reserved_cents)}
            accent="positive"
          />
          <StatCard label="Positions" value={num(positions.length)} />
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Open positions</CardTitle>
          <CardDescription>
            Position snapshots for this portfolio.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable
            columns={posColumns}
            data={positions}
            exportName={`positions-${p?.id ?? "portfolio"}`}
            searchPlaceholder="Search positions…"
          />
        </CardContent>
      </Card>

      {p && (
        <Card size="sm">
          <CardHeader>
            <CardTitle>Meta</CardTitle>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-2 text-sm md:grid-cols-4">
            <Meta label="Created">{dateTime(p.created_at)}</Meta>
            <Meta label="Updated">{dateTime(p.updated_at)}</Meta>
            <Meta label="Portfolio ID"><span className="font-mono text-xs">{p.id}</span></Meta>
            <Meta label="Mode"><Badge variant="outline" className="uppercase">{p.mode}</Badge></Meta>
          </CardContent>
        </Card>
      )}
    </div>
  )
}

function Meta({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col">
      <span className="text-muted-foreground text-[11px] uppercase tracking-wide">{label}</span>
      <span>{children}</span>
    </div>
  )
}

interface CashForm {
  amount_dollars: number
}

function CashDialog({ portfolio, mode }: { portfolio: Portfolio; mode: "deposit" | "withdraw" }) {
  const [open, setOpen] = useState(false)
  const { register, handleSubmit, reset } = useForm<CashForm>({
    defaultValues: { amount_dollars: 100 },
  })
  const deposit = useDeposit(portfolio.id)
  const withdraw = useWithdraw(portfolio.id)
  const mutation = mode === "deposit" ? deposit : withdraw

  const submit = handleSubmit(async (v) => {
    const cents = Math.round(Number(v.amount_dollars) * 100)
    if (!cents || cents <= 0) return
    await mutation.mutateAsync(cents)
    reset()
    setOpen(false)
  })

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant={mode === "deposit" ? "default" : "outline"} size="sm">
          {mode === "deposit" ? <Plus /> : <Minus />}
          {mode === "deposit" ? "Deposit" : "Withdraw"}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-[380px]">
        <DialogHeader>
          <DialogTitle>
            {mode === "deposit" ? "Deposit cash" : "Withdraw cash"}
          </DialogTitle>
          <DialogDescription>
            {mode === "deposit"
              ? "Add cash to this portfolio's wallet."
              : "Remove cash from this portfolio's wallet."}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="amount">Amount (USD)</Label>
            <Input
              id="amount"
              type="number"
              step="0.01"
              min="0.01"
              {...register("amount_dollars", { valueAsNumber: true })}
            />
          </div>
          {mutation.error && (
            <div className="text-destructive text-xs">
              {(mutation.error as { message?: string }).message}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={mutation.isPending}>
              {mutation.isPending ? "Working…" : mode === "deposit" ? "Deposit" : "Withdraw"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
