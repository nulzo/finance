import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { AuditRow } from "@/types/api"

export function useAudit(limit = 200) {
  return useQuery({
    queryKey: ["audit", limit],
    queryFn: async () => {
      const data = await api.get<AuditRow[]>("/v1/audit", {
        params: { limit },
      })
      return data ?? []
    },
  })
}
