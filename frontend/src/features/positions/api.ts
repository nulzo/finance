import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Position } from "@/types/api"

export const positionKeys = {
  all: ["positions"] as const,
  byPortfolio: (id: string) => [...positionKeys.all, id] as const,
}

export function usePositions(portfolioId: string | undefined) {
  return useQuery({
    queryKey: positionKeys.byPortfolio(portfolioId ?? ""),
    enabled: !!portfolioId,
    queryFn: async () => {
      const data = await api.get<Position[]>(
        `/v1/portfolios/${portfolioId}/positions`,
      )
      return data ?? []
    },
  })
}
