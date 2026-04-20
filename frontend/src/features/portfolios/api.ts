import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Portfolio, PortfolioDetail } from "@/types/api"

export const portfolioKeys = {
  all: ["portfolios"] as const,
  list: () => [...portfolioKeys.all, "list"] as const,
  detail: (id: string) => [...portfolioKeys.all, "detail", id] as const,
}

export function usePortfolios() {
  return useQuery({
    queryKey: portfolioKeys.list(),
    queryFn: async () => {
      const data = await api.get<Portfolio[]>("/v1/portfolios")
      return data ?? []
    },
  })
}

export function usePortfolio(id: string | undefined) {
  return useQuery({
    queryKey: portfolioKeys.detail(id ?? ""),
    enabled: !!id,
    queryFn: async () => {
      const data = await api.get<PortfolioDetail>(`/v1/portfolios/${id}`)
      return data
    },
  })
}

export function useDeposit(id: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (cents: number) => {
      const data = await api.post(`/v1/portfolios/${id}/deposit`, {
        amount_cents: cents,
      })
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: portfolioKeys.all })
    },
  })
}

export function useWithdraw(id: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (cents: number) => {
      const data = await api.post(`/v1/portfolios/${id}/withdraw`, {
        amount_cents: cents,
      })
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: portfolioKeys.all })
    },
  })
}
