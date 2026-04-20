import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { RiskLimits, RiskLimitsPatch } from "@/types/api"

export const riskKeys = {
  all: ["risk"] as const,
  limits: () => [...riskKeys.all, "limits"] as const,
}

/** Fetches the currently active risk limits. These are in-memory on
 *  the server — process restart reloads from env. The UI should refetch
 *  on focus so an operator sees any runtime changes another user made. */
export function useRiskLimits() {
  return useQuery({
    queryKey: riskKeys.limits(),
    staleTime: 5_000,
    queryFn: async () => {
      return api.get<RiskLimits>(`/v1/risk/limits`)
    },
  })
}

/** Applies a partial update to the risk engine's limits. Every field
 *  is optional — only send what changed. */
export function usePatchRiskLimits() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (patch: RiskLimitsPatch) => {
      return api.patch<RiskLimits>(`/v1/risk/limits`, patch)
    },
    onSuccess: (data) => {
      qc.setQueryData(riskKeys.limits(), data)
    },
  })
}
