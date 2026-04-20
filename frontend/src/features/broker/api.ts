import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { BrokerAccount, BrokerPosition } from "@/types/api"

export function useBrokerAccount() {
  return useQuery({
    queryKey: ["broker", "account"],
    queryFn: async () => {
      const data = await api.get<BrokerAccount>("/v1/broker/account")
      return data
    },
  })
}

export function useBrokerPositions() {
  return useQuery({
    queryKey: ["broker", "positions"],
    queryFn: async () => {
      const data = await api.get<BrokerPosition[]>("/v1/broker/positions")
      return data ?? []
    },
  })
}
