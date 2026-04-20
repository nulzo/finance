import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Decision } from "@/types/api"

export function useDecisions(limit = 200) {
  return useQuery({
    queryKey: ["decisions", limit],
    queryFn: async () => {
      const data = await api.get<Decision[]>("/v1/decisions", {
        params: { limit },
      })
      return data ?? []
    },
  })
}

export function useExecuteDecision() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      const data = await api.post(`/v1/decisions/${id}/execute`)
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["decisions"] })
      qc.invalidateQueries({ queryKey: ["orders"] })
      qc.invalidateQueries({ queryKey: ["portfolios"] })
    },
  })
}
