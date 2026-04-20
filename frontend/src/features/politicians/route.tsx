import { useState } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import { useForm } from "react-hook-form"
import { Plus } from "lucide-react"
import { toast } from "sonner"

import { DateCell } from "@/components/data-table/cells"
import { DataTable } from "@/components/data-table/data-table"
import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Badge } from "@/components/ui/badge"
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
import { Skeleton } from "@/components/ui/skeleton"
import {
  usePoliticians,
  useUpsertPolitician,
} from "@/features/politicians/api"
import type { Politician } from "@/types/api"

export function PoliticiansRoute() {
  const { data, isLoading, error } = usePoliticians()

  const columns: ColumnDef<Politician>[] = [
    { accessorKey: "name", header: "Name" },
    {
      accessorKey: "chamber",
      header: "Chamber",
      cell: ({ getValue }) => (
        <Badge variant="outline" className="capitalize">
          {String(getValue())}
        </Badge>
      ),
    },
    {
      accessorKey: "party",
      header: "Party",
      cell: ({ getValue }) => {
        const v = String(getValue() ?? "")
        return (
          <Badge
            className={
              v.toLowerCase().startsWith("d")
                ? "bg-blue-500/15 text-blue-500"
                : v.toLowerCase().startsWith("r")
                  ? "bg-red-500/15 text-red-500"
                  : "bg-muted text-muted-foreground"
            }
          >
            {v || "—"}
          </Badge>
        )
      },
    },
    { accessorKey: "state", header: "State" },
    {
      accessorKey: "track_weight",
      header: "Weight",
      cell: ({ getValue }) => (
        <span className="tabular-nums">{Number(getValue()).toFixed(2)}</span>
      ),
    },
    { accessorKey: "updated_at", header: "Updated", cell: ({ getValue }) => <DateCell value={String(getValue())} /> },
  ]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Politicians"
        description="Tracked congressional and senate members. Tune weights to amplify or dampen signal influence."
        actions={<UpsertPoliticianDialog />}
      />
      {error && <QueryError error={error} />}
      {isLoading ? (
        <Skeleton className="h-[300px] w-full" />
      ) : (
        <DataTable
          columns={columns}
          data={data ?? []}
          exportName="politicians"
          searchPlaceholder="Search politicians…"
        />
      )}
    </div>
  )
}

interface UpsertForm {
  name: string
  chamber: string
  party: string
  state: string
  track_weight: number
}

function UpsertPoliticianDialog() {
  const [open, setOpen] = useState(false)
  const mutation = useUpsertPolitician()
  const form = useForm<UpsertForm>({
    defaultValues: { track_weight: 1, chamber: "senate" },
  })

  const submit = form.handleSubmit(async (v) => {
    try {
      await mutation.mutateAsync(v)
      toast.success(`Saved ${v.name}`)
      form.reset()
      setOpen(false)
    } catch (e) {
      toast.error((e as { message?: string }).message ?? "Save failed")
    }
  })

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus /> Upsert politician
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle>Upsert politician</DialogTitle>
          <DialogDescription>
            Name is the match key. Existing rows will be updated.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="p-name">Name</Label>
            <Input id="p-name" required {...form.register("name", { required: true })} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <Label htmlFor="p-chamber">Chamber</Label>
              <Input id="p-chamber" placeholder="house / senate" {...form.register("chamber")} />
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="p-party">Party</Label>
              <Input id="p-party" placeholder="D / R / I" {...form.register("party")} />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <Label htmlFor="p-state">State</Label>
              <Input id="p-state" placeholder="TX" {...form.register("state")} />
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="p-weight">Weight</Label>
              <Input
                id="p-weight"
                type="number"
                step="0.01"
                {...form.register("track_weight", { valueAsNumber: true })}
              />
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={mutation.isPending}>
              {mutation.isPending ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
