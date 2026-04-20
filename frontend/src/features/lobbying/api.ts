import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { LobbyingEvent } from "@/types/api"

export const lobbyingKeys = {
  all: ["lobbying"] as const,
  list: (sym: string, since: string, limit: number) =>
    [...lobbyingKeys.all, sym, since, limit] as const,
}

export interface UseLobbyingOptions {
  symbol?: string
  since?: string
  limit?: number
}

/** Lobbying Disclosure Act filings attached to public tickers. The
 *  LDA cycle is quarterly so we default to 180d; the ingest tiers
 *  this down to a hot cache on the engine side. */
export function useLobbying(opts: UseLobbyingOptions = {}) {
  const symbol = (opts.symbol ?? "").toUpperCase().trim()
  const since = opts.since ?? "180d"
  const limit = opts.limit ?? 200
  return useQuery({
    queryKey: lobbyingKeys.list(symbol, since, limit),
    refetchInterval: 120_000,
    queryFn: async () => {
      const data = await api.get<LobbyingEvent[]>("/v1/lobbying", {
        params: {
          symbol: symbol || undefined,
          since,
          limit,
        },
      })
      return data ?? []
    },
  })
}
