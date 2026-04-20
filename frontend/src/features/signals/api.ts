import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Signal } from "@/types/api"

export function useSignals(symbol?: string) {
  return useQuery({
    queryKey: ["signals", symbol ?? ""],
    queryFn: async () => {
      const data = await api.get<Signal[]>("/v1/signals", {
        params: symbol ? { symbol } : undefined,
      })
      return data ?? []
    },
  })
}
