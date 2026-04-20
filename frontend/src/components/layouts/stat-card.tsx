import type { ReactNode } from "react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"

interface Props {
  label: string
  value: ReactNode
  hint?: ReactNode
  icon?: ReactNode
  accent?: "default" | "positive" | "negative"
  className?: string
}

export function StatCard({ label, value, hint, icon, accent = "default", className }: Props) {
  return (
    <Card className={cn("gap-2", className)} size="sm">
      <CardHeader className="flex-row items-start justify-between gap-2">
        <CardDescription className="text-[11px] font-medium uppercase tracking-wider">
          {label}
        </CardDescription>
        {icon && (
          <div className="text-muted-foreground flex size-6 items-center justify-center rounded-md bg-muted">
            {icon}
          </div>
        )}
      </CardHeader>
      <CardContent className="pb-1">
        <CardTitle
          className={cn(
            "text-2xl font-semibold tabular-nums",
            accent === "positive" && "text-emerald-500",
            accent === "negative" && "text-destructive",
          )}
        >
          {value}
        </CardTitle>
        {hint && (
          <div className="text-muted-foreground mt-1 text-xs">{hint}</div>
        )}
      </CardContent>
    </Card>
  )
}
