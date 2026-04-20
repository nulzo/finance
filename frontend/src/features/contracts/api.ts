import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { GovContract } from "@/types/api"

export const contractsKeys = {
  all: ["gov-contracts"] as const,
  list: (sym: string, since: string, limit: number) =>
    [...contractsKeys.all, sym, since, limit] as const,
}

export interface UseContractsOptions {
  symbol?: string
  since?: string
  limit?: number
}

/** Federal contract awards tied to public tickers. Backed by
 *  Quiver's /beta/bulk/govcontractsall feed; small updates are
 *  pushed constantly but high-signal awards ($10M+) are rare. */
export function useContracts(opts: UseContractsOptions = {}) {
  const symbol = (opts.symbol ?? "").toUpperCase().trim()
  const since = opts.since ?? "90d"
  const limit = opts.limit ?? 200
  return useQuery({
    queryKey: contractsKeys.list(symbol, since, limit),
    refetchInterval: 120_000,
    queryFn: async () => {
      const data = await api.get<GovContract[]>("/v1/contracts", {
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
