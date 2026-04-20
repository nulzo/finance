import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Quote } from "@/types/api"

export function useQuote(symbol: string | undefined) {
  return useQuery({
    queryKey: ["quote", symbol ?? ""],
    enabled: !!symbol,
    queryFn: async () => {
      const data = await api.get<Quote>(
        `/v1/quotes/${encodeURIComponent((symbol ?? "").toUpperCase())}`,
      )
      return data
    },
  })
}
