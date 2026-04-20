import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { LLMCall, UsageResponse } from "@/types/api"

export interface LLMCallsFilter {
  limit?: number
  offset?: number
  operation?: string
  model?: string
  outcome?: string
  since?: string
  until?: string
}

export function useLLMCalls(f: LLMCallsFilter = {}) {
  return useQuery({
    queryKey: ["llm-calls", f],
    queryFn: async () => {
      const data = await api.get<LLMCall[]>("/v1/llm/calls", {
        params: { ...f },
      })
      return data ?? []
    },
  })
}

export function useLLMUsage(since?: string, groupBy: string = "day") {
  return useQuery({
    queryKey: ["llm-usage", since, groupBy],
    queryFn: async () => {
      const data = await api.get<UsageResponse>("/v1/llm/usage", {
        params: { since, group_by: groupBy },
      })
      return data
    },
  })
}
