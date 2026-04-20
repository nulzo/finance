import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { EngineStatus } from "@/types/api"

export function useEngineStatus() {
  return useQuery({
    queryKey: ["engine", "status"],
    queryFn: async () => {
      const data = await api.get<EngineStatus>("/v1/engine/status")
      return data
    },
    refetchInterval: 10_000,
  })
}

export function useEngineToggle() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (enabled: boolean) => {
      const data = await api.post<EngineStatus>("/v1/engine/toggle", {
        enabled,
      })
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["engine"] }),
  })
}

export function useEngineIngest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const data = await api.post("/v1/engine/ingest")
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["news"] })
      qc.invalidateQueries({ queryKey: ["politician-trades"] })
      qc.invalidateQueries({ queryKey: ["signals"] })
    },
  })
}

export function useEngineDecide() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const data = await api.post("/v1/engine/decide")
      return data
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["decisions"] })
      qc.invalidateQueries({ queryKey: ["orders"] })
      qc.invalidateQueries({ queryKey: ["portfolios"] })
    },
  })
}

export function useVersion() {
  return useQuery({
    queryKey: ["version"],
    queryFn: async () => {
      const data = await api.get<{ version: string; commit: string }>(
        "/v1/version",
      )
      return data
    },
  })
}

export function useHealthz() {
  return useQuery({
    queryKey: ["healthz"],
    queryFn: async () => {
      const data = await api.get<{ status: string }>("/healthz")
      return data
    },
    refetchInterval: 15_000,
  })
}

export function useReadyz() {
  return useQuery({
    queryKey: ["readyz"],
    queryFn: async () => {
      const data = await api.get<{ status: string; error?: string }>("/readyz")
      return data
    },
    refetchInterval: 15_000,
  })
}
