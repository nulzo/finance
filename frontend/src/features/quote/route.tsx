import { useState } from "react"
import { Search } from "lucide-react"

import { PageHeader } from "@/components/layouts/page-header"
import { StatCard } from "@/components/layouts/stat-card"
import { QueryError } from "@/components/errors/query-error"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { useQuote } from "@/features/quote/api"
import { dateTime, dollars } from "@/utils/format"

export function QuoteRoute() {
  const [input, setInput] = useState("AAPL")
  const [symbol, setSymbol] = useState("AAPL")
  const { data, isFetching, error } = useQuote(symbol)

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Quote lookup"
        description="Pull a live quote from the configured market data provider."
      />
      <Card>
        <CardHeader>
          <CardTitle>Symbol</CardTitle>
          <CardDescription>
            Tickers are case-insensitive. Example: AAPL, MSFT, TSLA.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              setSymbol(input.trim().toUpperCase())
            }}
          >
            <Input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="AAPL"
              className="max-w-[200px]"
            />
            <Button type="submit" size="sm">
              <Search /> Quote
            </Button>
          </form>
        </CardContent>
      </Card>

      {error && <QueryError error={error} />}
      {isFetching && !data ? (
        <Skeleton className="h-[120px] w-full" />
      ) : data ? (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard label="Symbol" value={data.symbol} />
          <StatCard label="Price" value={dollars(data.price)} accent="positive" />
          <StatCard label="Bid" value={dollars(data.bid)} />
          <StatCard label="Ask" value={dollars(data.ask)} />
          <StatCard
            label="Timestamp"
            value={<span className="text-sm font-normal">{dateTime(data.timestamp)}</span>}
            className="md:col-span-4"
          />
        </div>
      ) : null}
    </div>
  )
}
