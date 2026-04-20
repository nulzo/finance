import type { ReactNode } from "react"

import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import type { OrderStatus, Side, DecisionAction } from "@/types/api"
import { cents, dateTime, dollars, num, relative, truncate } from "@/utils/format"

export function MoneyCents({ value }: { value: number | null | undefined }) {
  return <span className="tabular-nums">{cents(value)}</span>
}

export function Decimal({ value, digits = 4 }: { value?: string | number | null; digits?: number }) {
  return <span className="tabular-nums">{num(value, digits)}</span>
}

export function Dollars({ value }: { value?: string | number | null }) {
  return <span className="tabular-nums">{dollars(value)}</span>
}

export function DateCell({ value }: { value?: string | null }) {
  if (!value) return <span className="text-muted-foreground">-</span>
  return (
    <span className="tabular-nums whitespace-nowrap" title={dateTime(value)}>
      {relative(value)}
    </span>
  )
}

export function Symbol({ value }: { value: string }) {
  return (
    <span className="bg-muted inline-flex rounded px-1.5 py-0.5 font-mono text-xs font-medium">
      {value}
    </span>
  )
}

export function SideBadge({ value }: { value: Side }) {
  return (
    <Badge
      variant={value === "buy" ? "default" : "destructive"}
      className={cn(
        "uppercase",
        value === "buy" && "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
      )}
    >
      {value}
    </Badge>
  )
}

export function ActionBadge({ value }: { value: DecisionAction }) {
  const map: Record<DecisionAction, string> = {
    buy: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
    sell: "bg-destructive/15 text-destructive",
    hold: "bg-muted text-muted-foreground",
  }
  return <Badge className={cn("uppercase", map[value])}>{value}</Badge>
}

export function StatusBadge({ value }: { value: OrderStatus | string }) {
  const variants: Record<string, string> = {
    filled: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
    partially_filled: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
    submitted: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
    pending: "bg-muted text-muted-foreground",
    cancelled: "bg-muted text-muted-foreground",
    rejected: "bg-destructive/15 text-destructive",
    expired: "bg-destructive/15 text-destructive",
    ok: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  }
  return <Badge className={cn("capitalize", variants[value] ?? "bg-muted text-muted-foreground")}>{value?.replace(/_/g, " ")}</Badge>
}

export function ScoreCell({ value }: { value: number }) {
  const pos = value >= 0
  return (
    <span
      className={cn(
        "font-mono tabular-nums",
        pos ? "text-emerald-500" : "text-destructive",
      )}
    >
      {pos ? "+" : ""}
      {value.toFixed(3)}
    </span>
  )
}

export function ConfidenceCell({ value }: { value: number }) {
  const pct = Math.round(value * 100)
  return (
    <div className="flex items-center gap-2">
      <div className="bg-muted relative h-1.5 w-16 overflow-hidden rounded-full">
        <div
          className={cn(
            "absolute left-0 top-0 h-full",
            pct >= 70 ? "bg-emerald-500" : pct >= 40 ? "bg-amber-500" : "bg-destructive",
          )}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="tabular-nums text-xs">{pct}%</span>
    </div>
  )
}

export function TruncatedCell({ value, max = 80 }: { value?: string; max?: number }) {
  if (!value) return <span className="text-muted-foreground">-</span>
  return (
    <span title={value} className="block max-w-[40ch] truncate">
      {truncate(value, max)}
    </span>
  )
}

export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn("font-mono text-xs", className)}>{children}</span>
}
