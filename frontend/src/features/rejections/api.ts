import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Rejection, RejectionSource } from "@/types/api"

export const rejectionKeys = {
  all: ["rejections"] as const,
  byPortfolio: (id: string, source: string, since: string, limit: number) =>
    [...rejectionKeys.all, id, source, since, limit] as const,
}

export interface UseRejectionsOptions {
  /** Default "24h". Accepts a Go-style duration ("6h", "7d" → "168h")
   *  or an RFC3339 timestamp. Kept as a string so we pass through
   *  unchanged to the API layer; the backend handles both forms. */
  since?: string
  /** Optional source filter: "", "risk", "broker", "engine". */
  source?: RejectionSource | ""
  /** Hard cap on rows returned; backend default is 200. */
  limit?: number
}

/** Live feed of rejected trade attempts for a portfolio. Polls every
 *  30 s so operators watching the page see the risk/broker/engine
 *  events fan in without a manual refresh. */
export function useRejections(
  portfolioId: string | undefined,
  opts: UseRejectionsOptions = {},
) {
  const since = opts.since ?? "24h"
  const source = (opts.source ?? "") as string
  const limit = opts.limit ?? 200
  return useQuery({
    queryKey: rejectionKeys.byPortfolio(
      portfolioId ?? "",
      source,
      since,
      limit,
    ),
    enabled: !!portfolioId,
    refetchInterval: 30_000,
    queryFn: async () => {
      const data = await api.get<Rejection[]>(
        `/v1/portfolios/${portfolioId}/rejections`,
        { params: { since, source: source || undefined, limit } },
      )
      return data ?? []
    },
  })
}
