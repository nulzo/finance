import { useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import { ReceiptText } from "lucide-react"
import { useForm } from "react-hook-form"
import { toast } from "sonner"

import {
  DateCell,
  Decimal,
  MoneyCents,
  StatusBadge,
  SideBadge,
  Symbol,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { PortfolioSwitcher } from "@/components/layouts/portfolio-switcher"
import { QueryError } from "@/components/errors/query-error"
import { Button } from "@/components/ui/button"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useCreateOrder, useOrders } from "@/features/orders/api"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"
import type { Order, Side } from "@/types/api"

export function OrdersRoute() {
  const { portfolio, portfolioId } = useCurrentPortfolio()
  const { data, isLoading, error } = useOrders(portfolioId, 500)

  const columns: ColumnDef<Order>[] = [
    { accessorKey: "created_at", header: "Placed", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "symbol", header: "Symbol", cell: ({ getValue }) => <Symbol value={String(getValue())} /> },
    { accessorKey: "side", header: "Side", cell: ({ getValue }) => <SideBadge value={getValue() as Side} /> },
    { accessorKey: "type", header: "Type" },
    { accessorKey: "time_in_force", header: "TIF" },
    { accessorKey: "quantity", header: "Qty", cell: ({ getValue }) => <Decimal value={String(getValue())} digits={4} /> },
    { accessorKey: "filled_qty", header: "Filled", cell: ({ getValue }) => <Decimal value={String(getValue())} digits={4} /> },
    { accessorKey: "filled_avg_cents", header: "Avg fill", cell: ({ getValue }) => <MoneyCents value={Number(getValue())} /> },
    { accessorKey: "status", header: "Status", cell: ({ getValue }) => <StatusBadge value={String(getValue())} /> },
    { accessorKey: "broker_id", header: "Broker ID", cell: ({ getValue }) => <span className="font-mono text-xs">{String(getValue() ?? "")}</span> },
    { accessorKey: "reason", header: "Reason", cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={50} /> },
    { accessorKey: "filled_at", header: "Filled at", cell: ({ getValue }) => <DateCell value={getValue() as string | null} /> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Orders"
        description={
          portfolio
            ? `Order history for ${portfolio.name}.`
            : "Select a portfolio to see orders."
        }
        actions={
          <>
            <PortfolioSwitcher />
            {portfolio && <CreateOrderDialog portfolioId={portfolio.id} />}
          </>
        }
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="orders"
          searchPlaceholder="Search by symbol, status, reason…"
          exportOmit={["broker_id", "decision_id"]}
          initialPageSize={50}
        />
      )}
    </div>
  )
}

interface OrderForm {
  symbol: string
  side: Side
  quantity?: number
  notional_usd?: number
  reason?: string
}

function CreateOrderDialog({ portfolioId }: { portfolioId: string }) {
  const [open, setOpen] = useState(false)
  const create = useCreateOrder()
  const form = useForm<OrderForm>({
    defaultValues: { side: "buy", notional_usd: 100 },
  })

  const submit = form.handleSubmit(async (v) => {
    try {
      await create.mutateAsync({
        portfolioId,
        symbol: v.symbol.toUpperCase().trim(),
        side: v.side,
        quantity: v.quantity || undefined,
        notional_usd: v.notional_usd || undefined,
        reason: v.reason || "manual",
      })
      toast.success(`Order submitted for ${v.symbol.toUpperCase()}`)
      form.reset()
      setOpen(false)
    } catch (e) {
      toast.error((e as { message?: string }).message ?? "Order failed")
    }
  })

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <ReceiptText /> New order
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle>Create order</DialogTitle>
          <DialogDescription>
            Market order submitted to the configured broker.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <Label htmlFor="symbol">Symbol</Label>
              <Input id="symbol" placeholder="AAPL" {...form.register("symbol", { required: true })} />
            </div>
            <div className="flex flex-col gap-1">
              <Label>Side</Label>
              <Select
                value={form.watch("side")}
                onValueChange={(v) => form.setValue("side", v as Side)}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="buy">Buy</SelectItem>
                  <SelectItem value="sell">Sell</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <Label htmlFor="qty">Quantity</Label>
              <Input
                id="qty"
                type="number"
                step="0.0001"
                placeholder="0"
                {...form.register("quantity", { valueAsNumber: true })}
              />
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="notional">Notional USD</Label>
              <Input
                id="notional"
                type="number"
                step="1"
                placeholder="100"
                {...form.register("notional_usd", { valueAsNumber: true })}
              />
            </div>
          </div>
          <div className="text-muted-foreground text-[11px]">
            Either quantity or notional USD is required. Quantity wins if both are set.
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="reason">Reason</Label>
            <Input id="reason" placeholder="manual" {...form.register("reason")} />
          </div>
          {create.error && (
            <div className="text-destructive text-xs">
              {(create.error as { message?: string }).message}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={create.isPending}>
              {create.isPending ? "Submitting…" : "Submit order"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
