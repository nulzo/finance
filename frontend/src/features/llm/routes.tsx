import { useMemo, useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"

import {
  DateCell,
  StatusBadge,
  TruncatedCell,
} from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useLLMCalls, useLLMUsage } from "@/features/llm/api"
import type { LLMCall } from "@/types/api"
import { cn } from "@/lib/utils"
import { num } from "@/utils/format"

export function LLMCallsRoute() {
  const [operation, setOperation] = useState("")
  const [model, setModel] = useState("")
  const [outcome, setOutcome] = useState("")
  const { data, isLoading, error } = useLLMCalls({
    limit: 500,
    operation: operation || undefined,
    model: model || undefined,
    outcome: outcome || undefined,
  })

  const columns: ColumnDef<LLMCall>[] = [
    { accessorKey: "created_at", header: "When", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
    { accessorKey: "operation", header: "Operation" },
    { accessorKey: "model_used", header: "Model", cell: ({ getValue }) => <span className="font-mono text-xs">{String(getValue() ?? "")}</span> },
    { accessorKey: "attempt_index", header: "Attempt" },
    { accessorKey: "outcome", header: "Outcome", cell: ({ getValue }) => <StatusBadge value={String(getValue())} /> },
    { accessorKey: "total_tokens", header: "Tokens", cell: ({ row }) => (
      <span className="tabular-nums">
        {num(row.original.prompt_tokens)} · {num(row.original.completion_tokens)}
      </span>
    ) },
    {
      accessorKey: "total_cost_usd",
      header: "Cost USD",
      cell: ({ getValue }) => (
        <span className="tabular-nums">${Number(getValue()).toFixed(6)}</span>
      ),
    },
    { accessorKey: "latency_ms", header: "Latency", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()))} ms</span> },
    { accessorKey: "response_text", header: "Response", cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={60} /> },
    { accessorKey: "error_message", header: "Error", cell: ({ getValue }) => <TruncatedCell value={String(getValue() ?? "")} max={60} /> },
    { accessorKey: "temperature", header: "Temp", cell: ({ getValue }) => <span className="tabular-nums">{Number(getValue()).toFixed(2)}</span> },
    { accessorKey: "json_mode", header: "JSON", cell: ({ getValue }) => <Badge variant="outline">{String(getValue())}</Badge> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="LLM calls"
        description="Every inference attempt (including fallbacks) persisted for audit and cost tracking."
        actions={
          <div className="flex flex-wrap gap-2">
            <Input
              value={operation}
              onChange={(e) => setOperation(e.target.value)}
              placeholder="Operation"
              className="max-w-[160px]"
            />
            <Input
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder="Model"
              className="max-w-[200px]"
            />
            <Input
              value={outcome}
              onChange={(e) => setOutcome(e.target.value)}
              placeholder="Outcome (ok, http_...)"
              className="max-w-[180px]"
            />
          </div>
        }
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="llm-calls"
          searchPlaceholder="Search LLM calls…"
          initialPageSize={50}
          exportOmit={["request_messages", "response_text"]}
        />
      )}
    </div>
  )
}

export function LLMUsageRoute() {
  const [groupBy, setGroupBy] = useState("day")
  const [since, setSince] = useState(() => daysAgo(30))
  const { data, isLoading, error } = useLLMUsage(since, groupBy)

  const buckets = useMemo(() => {
    const rows = (data?.buckets ?? []).slice().reverse()
    return rows.map((b) => ({
      bucket: b.bucket,
      calls: b.calls,
      cost: Number(b.total_cost_usd) || 0,
      tokens: b.total_tokens,
      latency: b.avg_latency_ms,
      errors: b.error_calls,
    }))
  }, [data])

  const totals = data?.totals

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="LLM usage"
        description="Rolled up token, cost, latency and error counts."
        actions={
          <div className="flex flex-wrap gap-2">
            <Select value={groupBy} onValueChange={setGroupBy}>
              <SelectTrigger size="sm" className="min-w-[120px]">
                <SelectValue placeholder="Group by" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="day">Day</SelectItem>
                <SelectItem value="hour">Hour</SelectItem>
                <SelectItem value="model">Model</SelectItem>
                <SelectItem value="operation">Operation</SelectItem>
                <SelectItem value="outcome">Outcome</SelectItem>
              </SelectContent>
            </Select>
            <Select
              value={sinceToLabel(since)}
              onValueChange={(v) => setSince(daysAgo(Number(v)))}
            >
              <SelectTrigger size="sm" className="min-w-[120px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="1">Last 24h</SelectItem>
                <SelectItem value="7">Last 7 days</SelectItem>
                <SelectItem value="30">Last 30 days</SelectItem>
                <SelectItem value="90">Last 90 days</SelectItem>
              </SelectContent>
            </Select>
          </div>
        }
      />
      {error && <QueryError error={error} />}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <StatCard label="Calls" value={num(totals?.calls ?? 0)} />
        <StatCard
          label="Total cost"
          value={`$${Number(totals?.total_cost_usd ?? 0).toFixed(6)}`}
          accent="positive"
        />
        <StatCard
          label="Prompt tokens"
          value={num(totals?.prompt_tokens ?? 0)}
        />
        <StatCard
          label="Completion tokens"
          value={num(totals?.completion_tokens ?? 0)}
        />
        <StatCard
          label="Avg latency"
          value={`${num(totals?.avg_latency_ms ?? 0, 0)} ms`}
        />
        <StatCard
          label="Errors"
          value={num(totals?.error_calls ?? 0)}
          accent={totals?.error_calls ? "negative" : "default"}
        />
      </div>
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Cost</CardTitle>
            <CardDescription>USD per bucket</CardDescription>
          </CardHeader>
          <CardContent className="h-[280px]">
            {isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={buckets}>
                  <defs>
                    <linearGradient id="cost" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#6366f1" stopOpacity={0.4} />
                      <stop offset="100%" stopColor="#6366f1" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                  <XAxis dataKey="bucket" fontSize={10} tickLine={false} axisLine={false} />
                  <YAxis fontSize={10} tickLine={false} axisLine={false} width={40} />
                  <Tooltip
                    contentStyle={{
                      background: "var(--popover)",
                      border: "1px solid var(--border)",
                      borderRadius: 8,
                      fontSize: 12,
                    }}
                  />
                  <Area type="monotone" dataKey="cost" stroke="#6366f1" fill="url(#cost)" strokeWidth={2} />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Calls &amp; errors</CardTitle>
            <CardDescription>Volume per bucket</CardDescription>
          </CardHeader>
          <CardContent className="h-[280px]">
            {isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={buckets}>
                  <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                  <XAxis dataKey="bucket" fontSize={10} tickLine={false} axisLine={false} />
                  <YAxis fontSize={10} tickLine={false} axisLine={false} width={32} />
                  <Tooltip
                    contentStyle={{
                      background: "var(--popover)",
                      border: "1px solid var(--border)",
                      borderRadius: 8,
                      fontSize: 12,
                    }}
                  />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  <Bar dataKey="calls" name="Calls" fill="#06b6d4" radius={[4, 4, 0, 0]} />
                  <Bar dataKey="errors" name="Errors" fill="#ef4444" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Buckets</CardTitle>
          <CardDescription>Underlying rows.</CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable
            exportName="llm-usage"
            columns={[
              { accessorKey: "bucket", header: "Bucket", cell: ({ getValue }) => <span className={cn("font-mono text-xs")}>{String(getValue())}</span> },
              { accessorKey: "calls", header: "Calls", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()))}</span> },
              { accessorKey: "total_tokens", header: "Tokens", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()))}</span> },
              { accessorKey: "prompt_tokens", header: "Prompt", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()))}</span> },
              { accessorKey: "completion_tokens", header: "Completion", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()))}</span> },
              { accessorKey: "total_cost_usd", header: "Cost USD", cell: ({ getValue }) => <span className="tabular-nums">${Number(getValue()).toFixed(6)}</span> },
              { accessorKey: "avg_latency_ms", header: "Avg latency", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()), 0)} ms</span> },
              { accessorKey: "error_calls", header: "Errors", cell: ({ getValue }) => <span className="tabular-nums">{num(Number(getValue()))}</span> },
            ]}
            data={data?.buckets ?? []}
          />
        </CardContent>
      </Card>
    </div>
  )
}

function daysAgo(n: number): string {
  return new Date(Date.now() - n * 86400 * 1000).toISOString()
}

function sinceToLabel(since: string): string {
  const days = Math.round(
    (Date.now() - new Date(since).getTime()) / 86400 / 1000,
  )
  if (days <= 1) return "1"
  if (days <= 7) return "7"
  if (days <= 30) return "30"
  return "90"
}
