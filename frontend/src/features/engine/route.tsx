import { Download, Gauge, PauseCircle, PlayCircle, RefreshCw, Sparkles } from "lucide-react"
import { toast } from "sonner"

import { PageHeader } from "@/components/layouts/page-header"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  useEngineDecide,
  useEngineIngest,
  useEngineStatus,
  useEngineToggle,
  useHealthz,
  useReadyz,
  useVersion,
} from "@/features/engine/api"

export function EngineRoute() {
  const status = useEngineStatus()
  const toggle = useEngineToggle()
  const ingest = useEngineIngest()
  const decide = useEngineDecide()
  const version = useVersion()
  const health = useHealthz()
  const ready = useReadyz()

  const handleToggle = async () => {
    const next = !status.data?.enabled
    try {
      await toggle.mutateAsync(next)
      toast.success(next ? "Engine started" : "Engine paused")
    } catch (e) {
      toast.error((e as { message?: string }).message ?? "Toggle failed")
    }
  }
  const handleIngest = async () => {
    try {
      await ingest.mutateAsync()
      toast.success("Ingest complete")
    } catch (e) {
      toast.error((e as { message?: string }).message ?? "Ingest failed")
    }
  }
  const handleDecide = async () => {
    try {
      await decide.mutateAsync()
      toast.success("Decide cycle complete")
    } catch (e) {
      toast.error((e as { message?: string }).message ?? "Decide failed")
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Engine"
        description="Control the automated ingest / decide / trade loop."
      />
      {status.error && <QueryError error={status.error} />}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard
          label="Engine"
          icon={<Gauge className="size-3.5" />}
          value={
            status.isLoading ? (
              <Skeleton className="h-7 w-20" />
            ) : (
              <Badge
                className={
                  status.data?.enabled
                    ? "bg-emerald-500/15 text-emerald-600"
                    : "bg-muted text-muted-foreground"
                }
              >
                {status.data?.enabled ? "Running" : "Paused"}
              </Badge>
            )
          }
        />
        <StatCard
          label="Health"
          value={
            <Badge
              className={
                health.data?.status === "ok" || health.data?.status === "alive"
                  ? "bg-emerald-500/15 text-emerald-600"
                  : "bg-destructive/15 text-destructive"
              }
            >
              {health.data?.status ?? "—"}
            </Badge>
          }
        />
        <StatCard
          label="Ready"
          value={
            <Badge
              className={
                ready.data?.status === "ready"
                  ? "bg-emerald-500/15 text-emerald-600"
                  : "bg-destructive/15 text-destructive"
              }
            >
              {ready.data?.status ?? "—"}
            </Badge>
          }
          hint={ready.data?.error}
        />
        <StatCard
          label="Version"
          value={
            <span className="font-mono text-sm font-normal">
              {version.data?.version ?? "…"}
            </span>
          }
          hint={
            version.data ? (
              <span className="font-mono">{version.data.commit.slice(0, 12)}</span>
            ) : null
          }
        />
      </div>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              {status.data?.enabled ? <PauseCircle className="size-4" /> : <PlayCircle className="size-4" />}
              Toggle
            </CardTitle>
            <CardDescription>
              Flip the global kill switch. A paused engine won't ingest or
              auto-trade.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={handleToggle} disabled={toggle.isPending} size="sm">
              {status.data?.enabled ? "Pause engine" : "Start engine"}
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Download className="size-4" />
              Ingest now
            </CardTitle>
            <CardDescription>
              Pull news, politician trades and produce new signals.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              onClick={handleIngest}
              disabled={ingest.isPending}
              size="sm"
              variant="outline"
            >
              <RefreshCw className={ingest.isPending ? "animate-spin" : undefined} />
              Run ingest
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Sparkles className="size-4" /> Decide now
            </CardTitle>
            <CardDescription>
              Ask the LLM strategist to generate decisions and, if allowed,
              execute them.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              onClick={handleDecide}
              disabled={decide.isPending}
              size="sm"
              variant="outline"
            >
              <Sparkles /> Run decide
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
