import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Order, Side } from "@/types/api"

export const orderKeys = {
  all: ["orders"] as const,
  byPortfolio: (id: string, limit: number) => ["orders", id, limit] as const,
}

export function useOrders(portfolioId: string | undefined, limit = 250) {
  return useQuery({
    queryKey: orderKeys.byPortfolio(portfolioId ?? "", limit),
    enabled: !!portfolioId,
    queryFn: async () => {
      const data = await api.get<Order[]>(`/v1/portfolios/${portfolioId}/orders`, {
        params: { limit },
      })
      return data ?? []
    },
  })
}

export interface CreateOrderInput {
  portfolioId: string
  symbol: string
  side: Side
  quantity?: number
  notional_usd?: number
  reason?: string
}

export function useCreateOrder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: CreateOrderInput) => {
      const body: Record<string, unknown> = {
        symbol: input.symbol,
        side: input.side,
        reason: input.reason ?? "",
      }
      if (input.quantity) body.quantity = input.quantity
      if (input.notional_usd) body.notional_usd = input.notional_usd
      const data = await api.post<Order>(
        `/v1/portfolios/${input.portfolioId}/orders`,
        body,
      )
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: orderKeys.all })
      qc.invalidateQueries({ queryKey: ["portfolios"] })
      qc.invalidateQueries({ queryKey: ["positions"] })
    },
  })
}
