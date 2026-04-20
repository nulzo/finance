import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type {
  AnalyticsSummary,
  EquitySnapshot,
  EquityValuation,
  PositionPnL,
} from "@/types/api"

export const analyticsKeys = {
  all: ["analytics"] as const,
  summary: (id: string) => [...analyticsKeys.all, "summary", id] as const,
  equityLive: (id: string) => [...analyticsKeys.all, "equity", id] as const,
  equityHistory: (id: string, since: string) =>
    [...analyticsKeys.all, "equity-history", id, since] as const,
  positionsPnL: (id: string) =>
    [...analyticsKeys.all, "positions-pnl", id] as const,
}

/** Live valuation: cash, cost, MTM, realised/unrealised P&L, plus
 *  per-position breakdown. Refreshed every 30 seconds — matches the
 *  default quote cache TTL so we don't thrash the provider. */
export function useEquityLive(portfolioId: string | undefined) {
  return useQuery({
    queryKey: analyticsKeys.equityLive(portfolioId ?? ""),
    enabled: !!portfolioId,
    refetchInterval: 30_000,
    queryFn: () =>
      api.get<EquityValuation>(`/v1/portfolios/${portfolioId}/equity`),
  })
}

/** Time-series of persisted equity snapshots for charting. `since`
 *  accepts a Go duration (e.g. "24h", "720h", "30d" is not valid —
 *  use hours) or an RFC3339 timestamp. */
export function useEquityHistory(
  portfolioId: string | undefined,
  since = "720h", // 30d
) {
  return useQuery({
    queryKey: analyticsKeys.equityHistory(portfolioId ?? "", since),
    enabled: !!portfolioId,
    refetchInterval: 60_000,
    queryFn: async () => {
      const rows = await api.get<EquitySnapshot[]>(
        `/v1/portfolios/${portfolioId}/equity/history`,
        { params: { since } },
      )
      return rows ?? []
    },
  })
}

/** Per-position unrealised P&L. Same leg numbers as useEquityLive()
 *  .positions, but also available standalone so a widget doesn't
 *  have to over-fetch the full valuation. */
export function usePositionsPnL(portfolioId: string | undefined) {
  return useQuery({
    queryKey: analyticsKeys.positionsPnL(portfolioId ?? ""),
    enabled: !!portfolioId,
    refetchInterval: 30_000,
    queryFn: async () => {
      const rows = await api.get<PositionPnL[]>(
        `/v1/portfolios/${portfolioId}/positions/pnl`,
      )
      return rows ?? []
    },
  })
}

/** Single-round-trip header summary: combines cash, equity,
 *  realised/unrealised, and day-change into one payload for the
 *  Overview + Analytics stat cards. */
export function useAnalyticsSummary(portfolioId: string | undefined) {
  return useQuery({
    queryKey: analyticsKeys.summary(portfolioId ?? ""),
    enabled: !!portfolioId,
    refetchInterval: 30_000,
    queryFn: () =>
      api.get<AnalyticsSummary>(
        `/v1/portfolios/${portfolioId}/analytics/summary`,
      ),
  })
}
