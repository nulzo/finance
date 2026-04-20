import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { NewsItem } from "@/types/api"

export function useNews(limit = 200) {
  return useQuery({
    queryKey: ["news", limit],
    queryFn: async () => {
      const data = await api.get<NewsItem[]>("/v1/news", {
        params: { limit },
      })
      return data ?? []
    },
  })
}
