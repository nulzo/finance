import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { PoliticianTrade } from "@/types/api"

export function usePoliticianTrades(limit = 500) {
  return useQuery({
    queryKey: ["politician-trades", limit],
    queryFn: async () => {
      const data = await api.get<PoliticianTrade[]>(
        "/v1/politician-trades",
        { params: { limit } },
      )
      return data ?? []
    },
  })
}
