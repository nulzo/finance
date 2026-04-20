import { PageHeader } from "@/components/layouts/page-header"
import { QueryError } from "@/components/errors/query-error"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useBrokerAccount, useBrokerPositions } from "@/features/broker/api"

export function BrokerRoute() {
  const acct = useBrokerAccount()
  const pos = useBrokerPositions()

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Broker"
        description="Live view through the configured broker (mock, Alpaca, etc.)."
      />
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Account</CardTitle>
            <CardDescription>Raw broker account payload.</CardDescription>
          </CardHeader>
          <CardContent>
            {acct.error ? (
              <QueryError error={acct.error} />
            ) : acct.isLoading ? (
              <Skeleton className="h-[200px] w-full" />
            ) : (
              <JsonBlock data={acct.data} />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Positions</CardTitle>
            <CardDescription>Raw broker positions payload.</CardDescription>
          </CardHeader>
          <CardContent>
            {pos.error ? (
              <QueryError error={pos.error} />
            ) : pos.isLoading ? (
              <Skeleton className="h-[200px] w-full" />
            ) : (
              <JsonBlock data={pos.data} />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function JsonBlock({ data }: { data: unknown }) {
  return (
    <pre className="bg-muted/40 max-h-[500px] overflow-auto rounded-lg p-3 font-mono text-xs">
      {JSON.stringify(data, null, 2)}
    </pre>
  )
}
