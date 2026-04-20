import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { Trash2 } from "lucide-react"

import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { usePatchRiskLimits, useRiskLimits } from "@/features/risk/api"
import type { RiskLimits, RiskLimitsPatch } from "@/types/api"

interface FormState {
  max_order_usd: string
  max_position_usd: string
  max_daily_loss_usd: string
  max_daily_orders: string
  max_symbol_exposure: string
  max_concurrent_positions: string
  require_approval: boolean
  blacklist: string[]
}

// Converts a server RiskLimits blob into the local form's string-first
// shape. Decimals already arrive as strings; integers get stringified
// so inputs don't flip between controlled/uncontrolled.
function toForm(l: RiskLimits): FormState {
  return {
    max_order_usd: l.max_order_usd,
    max_position_usd: l.max_position_usd,
    max_daily_loss_usd: l.max_daily_loss_usd,
    max_daily_orders: String(l.max_daily_orders),
    max_symbol_exposure: l.max_symbol_exposure,
    max_concurrent_positions: String(l.max_concurrent_positions),
    require_approval: l.require_approval,
    blacklist: [...l.blacklist],
  }
}

// Diffs the form against the last known server snapshot so we only
// PATCH fields the operator actually changed. Sending zeros for
// untouched fields would look like "set to zero" on the server.
function diffPatch(current: FormState, server: RiskLimits): RiskLimitsPatch {
  const p: RiskLimitsPatch = {}
  if (current.max_order_usd !== server.max_order_usd) p.max_order_usd = current.max_order_usd
  if (current.max_position_usd !== server.max_position_usd) p.max_position_usd = current.max_position_usd
  if (current.max_daily_loss_usd !== server.max_daily_loss_usd) p.max_daily_loss_usd = current.max_daily_loss_usd
  if (current.max_daily_orders !== String(server.max_daily_orders)) {
    p.max_daily_orders = Number(current.max_daily_orders)
  }
  if (current.max_symbol_exposure !== server.max_symbol_exposure) {
    p.max_symbol_exposure = current.max_symbol_exposure
  }
  if (current.max_concurrent_positions !== String(server.max_concurrent_positions)) {
    p.max_concurrent_positions = Number(current.max_concurrent_positions)
  }
  if (current.require_approval !== server.require_approval) {
    p.require_approval = current.require_approval
  }
  const blA = [...current.blacklist].sort().join(",")
  const blB = [...server.blacklist].sort().join(",")
  if (blA !== blB) p.blacklist = current.blacklist
  return p
}

function validate(f: FormState): string | null {
  for (const [k, v] of Object.entries({
    max_order_usd: f.max_order_usd,
    max_position_usd: f.max_position_usd,
    max_daily_loss_usd: f.max_daily_loss_usd,
    max_symbol_exposure: f.max_symbol_exposure,
  })) {
    if (v !== "" && Number.isNaN(Number(v))) return `${k} must be a number`
    if (v !== "" && Number(v) < 0) return `${k} must be non-negative`
  }
  if (Number.isNaN(Number(f.max_daily_orders)) || Number(f.max_daily_orders) < 0) {
    return "max_daily_orders must be non-negative integer"
  }
  if (
    Number.isNaN(Number(f.max_concurrent_positions)) ||
    Number(f.max_concurrent_positions) < 0
  ) {
    return "max_concurrent_positions must be non-negative integer"
  }
  const sx = Number(f.max_symbol_exposure)
  if (!Number.isNaN(sx) && sx > 1) {
    return "max_symbol_exposure must be in [0, 1]"
  }
  return null
}

export function RiskRoute() {
  const { data, isLoading, error, refetch } = useRiskLimits()
  const patch = usePatchRiskLimits()
  const [form, setForm] = useState<FormState | null>(null)
  const [newSymbol, setNewSymbol] = useState("")

  useEffect(() => {
    // Initialise the edit form the first time the server sends limits.
    // After that we only accept updates through onSave → server → query
    // cache, so the form never fights a background refetch.
    if (data && !form) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(toForm(data))
    }
  }, [data, form])

  const hasChanges = useMemo(() => {
    if (!form || !data) return false
    return Object.keys(diffPatch(form, data)).length > 0
  }, [form, data])

  function set<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev))
  }

  function addBlacklist() {
    if (!form) return
    const sym = newSymbol.trim().toUpperCase()
    if (!sym || form.blacklist.includes(sym)) return
    set("blacklist", [...form.blacklist, sym])
    setNewSymbol("")
  }

  function removeBlacklist(sym: string) {
    if (!form) return
    set(
      "blacklist",
      form.blacklist.filter((s) => s !== sym),
    )
  }

  async function onSave() {
    if (!form || !data) return
    const err = validate(form)
    if (err) {
      toast.error(err)
      return
    }
    const body = diffPatch(form, data)
    if (Object.keys(body).length === 0) {
      toast.info("Nothing changed")
      return
    }
    try {
      const updated = await patch.mutateAsync(body)
      setForm(toForm(updated))
      toast.success("Risk limits updated")
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed")
    }
  }

  function onReset() {
    if (data) setForm(toForm(data))
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Risk limits"
        description="Live controls for the risk engine. Changes apply immediately but are in-memory only — restarting the daemon reloads defaults from env."
      />

      {error && <QueryError error={error} />}
      {isLoading || !form || !data ? (
        <Skeleton className="h-[480px] w-full" />
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Ceilings</CardTitle>
              <CardDescription>
                Monetary ceilings are USD; <code>max_symbol_exposure</code> is a
                fraction of equity in [0, 1].
              </CardDescription>
            </CardHeader>
            <CardContent className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <LabeledInput
                label="Max order USD"
                hint="Per-order cap (approved.Notional clamp)"
                value={form.max_order_usd}
                onChange={(v) => set("max_order_usd", v)}
              />
              <LabeledInput
                label="Max position USD"
                hint="Cost-basis ceiling per symbol"
                value={form.max_position_usd}
                onChange={(v) => set("max_position_usd", v)}
              />
              <LabeledInput
                label="Max daily loss USD"
                hint="Blocks buys when realised P&L ≤ –N"
                value={form.max_daily_loss_usd}
                onChange={(v) => set("max_daily_loss_usd", v)}
              />
              <LabeledInput
                label="Max daily orders"
                hint="Integer; 0 = unlimited"
                value={form.max_daily_orders}
                onChange={(v) => set("max_daily_orders", v)}
              />
              <LabeledInput
                label="Max symbol exposure"
                hint="Fraction of equity, e.g. 0.15"
                value={form.max_symbol_exposure}
                onChange={(v) => set("max_symbol_exposure", v)}
              />
              <LabeledInput
                label="Max concurrent positions"
                hint="Hard cap on distinct open symbols"
                value={form.max_concurrent_positions}
                onChange={(v) => set("max_concurrent_positions", v)}
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Approval & blacklist</CardTitle>
              <CardDescription>
                Runtime safety switches. Blacklisted symbols are refused by the
                risk engine with a <code>blacklisted</code> rejection row.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <div className="flex items-center gap-3">
                <Switch
                  id="req-approval"
                  checked={form.require_approval}
                  onCheckedChange={(v) => set("require_approval", !!v)}
                />
                <Label htmlFor="req-approval">Require manual approval for every trade</Label>
              </div>

              <div className="flex flex-col gap-2">
                <Label>Blacklist</Label>
                <div className="flex flex-wrap gap-2">
                  {form.blacklist.length === 0 ? (
                    <span className="text-muted-foreground text-sm">No symbols blacklisted.</span>
                  ) : (
                    form.blacklist.map((sym) => (
                      <Badge key={sym} variant="secondary" className="flex items-center gap-1 font-mono">
                        {sym}
                        <button
                          onClick={() => removeBlacklist(sym)}
                          className="hover:text-destructive"
                          type="button"
                          aria-label={`Remove ${sym}`}
                        >
                          <Trash2 className="size-3" />
                        </button>
                      </Badge>
                    ))
                  )}
                </div>
                <div className="flex items-end gap-2">
                  <div className="flex flex-col gap-1">
                    <Label htmlFor="new-sym" className="text-muted-foreground text-[11px] uppercase tracking-wider">
                      Add symbol
                    </Label>
                    <Input
                      id="new-sym"
                      className="w-[160px] font-mono"
                      value={newSymbol}
                      onChange={(e) => setNewSymbol(e.target.value)}
                      placeholder="e.g. TSLA"
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault()
                          addBlacklist()
                        }
                      }}
                    />
                  </div>
                  <Button variant="outline" size="sm" onClick={addBlacklist} disabled={!newSymbol.trim()}>
                    Add
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>

          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" onClick={() => refetch()} disabled={patch.isPending}>
              Refresh
            </Button>
            <Button variant="outline" onClick={onReset} disabled={!hasChanges || patch.isPending}>
              Reset
            </Button>
            <Button onClick={onSave} disabled={!hasChanges || patch.isPending}>
              {patch.isPending ? "Saving…" : "Save changes"}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}

function LabeledInput({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint?: string
  value: string
  onChange: (v: string) => void
}) {
  return (
    <div className="flex flex-col gap-1">
      <Label className="text-xs uppercase tracking-wider text-muted-foreground">{label}</Label>
      <Input value={value} onChange={(e) => onChange(e.target.value)} className="font-mono" />
      {hint && <span className="text-muted-foreground text-[11px]">{hint}</span>}
    </div>
  )
}
