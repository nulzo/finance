import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { Politician } from "@/types/api"

export function usePoliticians() {
  return useQuery({
    queryKey: ["politicians"],
    queryFn: async () => {
      const data = await api.get<Politician[]>("/v1/politicians")
      return data ?? []
    },
  })
}

export interface UpsertPoliticianInput {
  id?: string
  name: string
  chamber: string
  party: string
  state: string
  track_weight: number
}

export function useUpsertPolitician() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: UpsertPoliticianInput) => {
      const data = await api.post<Politician>("/v1/politicians", input)
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["politicians"] })
    },
  })
}
