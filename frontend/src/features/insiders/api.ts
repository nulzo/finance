import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { InsiderTrade, Side } from "@/types/api"

export const insiderKeys = {
  all: ["insiders"] as const,
  list: (sym: string, side: string, since: string, limit: number) =>
    [...insiderKeys.all, sym, side, since, limit] as const,
}

export interface UseInsidersOptions {
  symbol?: string
  /** "" means any side; applied server-side. */
  side?: Side | ""
  /** Go-style duration ("24h", "7d" → "168h") or RFC3339. */
  since?: string
  limit?: number
}

/** Recent SEC Form 4 insider filings. Backed by Quiver's
 *  /beta/bulk/insiders feed; polls every 60 s so operators see new
 *  filings without reloading the page. */
export function useInsiders(opts: UseInsidersOptions = {}) {
  const symbol = (opts.symbol ?? "").toUpperCase().trim()
  const side = (opts.side ?? "") as string
  const since = opts.since ?? "30d"
  const limit = opts.limit ?? 200
  return useQuery({
    queryKey: insiderKeys.list(symbol, side, since, limit),
    refetchInterval: 60_000,
    queryFn: async () => {
      const data = await api.get<InsiderTrade[]>("/v1/insiders", {
        params: {
          symbol: symbol || undefined,
          side: side || undefined,
          since,
          limit,
        },
      })
      return data ?? []
    },
  })
}
