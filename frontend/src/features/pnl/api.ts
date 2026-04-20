import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { DailyPnL } from "@/types/api"

export const pnlKeys = {
  all: ["pnl"] as const,
  byPortfolio: (id: string, since: string) =>
    [...pnlKeys.all, id, since] as const,
}

/** Zero-filled daily realized P&L series for charting. `since` accepts
 *  either a Go duration ("30d" → "720h", "24h") or an RFC3339
 *  timestamp; the backend rounds days to UTC midnight. */
export function usePnLSeries(
  portfolioId: string | undefined,
  since = "720h", // ~30d
) {
  return useQuery({
    queryKey: pnlKeys.byPortfolio(portfolioId ?? "", since),
    enabled: !!portfolioId,
    refetchInterval: 60_000,
    queryFn: async () => {
      const data = await api.get<DailyPnL[]>(
        `/v1/portfolios/${portfolioId}/pnl`,
        { params: { since } },
      )
      return data ?? []
    },
  })
}
