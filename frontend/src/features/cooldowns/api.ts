import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Cooldown } from "@/types/api"

export const cooldownKeys = {
  all: ["cooldowns"] as const,
  byPortfolio: (id: string) => [...cooldownKeys.all, id] as const,
}

// Cooldowns change on every tick when the engine rejects; refetch often
// enough to feel live without hammering the server.
export function useCooldowns(portfolioId: string | undefined) {
  return useQuery({
    queryKey: cooldownKeys.byPortfolio(portfolioId ?? ""),
    enabled: !!portfolioId,
    refetchInterval: 15_000,
    queryFn: async () => {
      const data = await api.get<Cooldown[]>(
        `/v1/portfolios/${portfolioId}/cooldowns`,
      )
      return data ?? []
    },
  })
}

/** Clears a single symbol cooldown so the engine can resume trading it
 *  immediately — used from the Cooldowns page "Clear" button. Returns
 *  a mutation so the caller can disable the button while in-flight. */
export function useClearCooldown(portfolioId: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (symbol: string) => {
      if (!portfolioId) throw new Error("no portfolio selected")
      await api.del<void>(
        `/v1/portfolios/${portfolioId}/cooldowns/${encodeURIComponent(symbol)}`,
      )
      return symbol
    },
    onSuccess: () => {
      if (portfolioId) {
        qc.invalidateQueries({ queryKey: cooldownKeys.byPortfolio(portfolioId) })
      }
    },
  })
}
